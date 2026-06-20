# Plan: clip-video-time-range

Source spec: [`_specs/clip-video-time-range.md`](_specs/clip-video-time-range.md)
Branch: `claude/feature/clip-video-time-range`

## Context

Today `x-dl` downloads a tweet's full video. We're adding `--from MM:SS` and `--to MM:SS` flags so the user can keep only a chosen slice. Both flags are optional; omitting both leaves today's behavior byte-identical. Clipping uses `ffmpeg -c copy` (stream copy — fast, keyframe-aligned to within ~2s) and only runs after a pre-download `ffprobe` duration check so impossible ranges fail before any bandwidth is spent. For HLS sources the range is pushed into the existing ffmpeg invocation via `-ss`/`-t` so only the requested segments are fetched; for direct mp4/webm sources we download the full file to a temp path and trim with a second ffmpeg pass.

One product decision confirmed (not in spec):

- `--url-only` + clip flag: **silently ignore** the clip flags. No ffmpeg check, no probe, no warning.

Explicit `--output path.ext` + clip flag overwrites silently — same as today's non-clip path with an explicit `--output`. The spec is silent on overwrite behavior; matching repo convention beats inventing a new CLI flag for one combination.

## Approach

### New package: `internal/clip/`

Pure parser / validator / planner. No subprocess calls — those stay in `internal/ffmpeg`. One source file plus its test:

- [`internal/clip/clip.go`](internal/clip/clip.go) — types, MM:SS parser, flag-pair parser, validator, formatter, filename helper.
- [`internal/clip/clip_test.go`](internal/clip/clip_test.go) — table-driven tests.

(If the file grows past ~300 lines during implementation, split then. The whole API below is ~150 lines of pure functions; a 4-file layout is more directory than the code earns.)

Public API:

```go
type Spec struct {
    From    time.Duration
    To      time.Duration
    HasFrom bool
    HasTo   bool
}

type Range struct {
    Start    time.Duration
    End      time.Duration
    Duration time.Duration // End - Start
}

func (s Spec) IsRequested() bool                                  // true iff HasFrom || HasTo
func ParseTimestamp(s string) (time.Duration, error)              // strict MM:SS
func ParseSpec(fromFlag, toFlag string) (Spec, error)             // left-to-right error order
func Resolve(spec Spec, duration time.Duration) (Range, error)    // applies defaults + clamps + errors
func ClipFilename(path string) string                             // insert "_clip" before ext (auto paths only)
func FormatTimestamp(d time.Duration) string                      // zero-padded "MM:SS" / "MMM:SS"
```

Reuse `tweet.FormatTime` ([`internal/tweet/url.go:175`](internal/tweet/url.go#L175)) for the parenthesized `M:SS` length in the confirmation line — no need for a second formatter in `internal/clip`.

Parser rule: minutes is one-or-more digits (no upper bound), `:`, seconds is exactly two digits 00–59. Reject `30`, `0:01:25`, `1:25.5`, `01:60`, `01:5`, `abc`, `-5`, empty.

`Resolve` rules:
- Unset From → 0. Unset To → duration.
- From ≥ duration → error including actual duration as `MM:SS`.
- To > duration + 2s → error including actual duration.
- To within 2s of duration → silently clamp to duration.
- From ≥ To → error including both formatted values.

`ClipFilename` uses `filepath.Ext` (not naive last-dot) and is only ever called for auto-generated paths — the caller in `cmd/x-dl/main.go` gates on `opts.output == ""`.

### Extensions to `internal/ffmpeg/ffmpeg.go`

- `IsFfprobeAvailable() bool` — twin of `IsAvailable` ([`internal/ffmpeg/ffmpeg.go:37`](internal/ffmpeg/ffmpeg.go#L37)), uses `exec.LookPath("ffprobe")`.
- `Probe(ctx context.Context, videoURL string) (time.Duration, error)` — runs `ffprobe -v error -show_entries format=duration -of default=noprint_wrappers=1:nokey=1 <url>`, parses float seconds. Implementation: `probeCtx, cancel := context.WithTimeout(ctx, 15*time.Second); defer cancel()` so Ctrl-C from the caller still cancels the probe. **Do not** derive from `context.Background()` — that would break signal handling. Works for both progressive MP4 and HLS.
- Extend `HLSOptions` with optional `StartOffset time.Duration` and `Duration time.Duration`. When `Duration > 0`, splice `-ss <start>` BEFORE `-i` and `-t <duration>` AFTER `-i` into the existing arg list at [`internal/ffmpeg/ffmpeg.go:64-72`](internal/ffmpeg/ffmpeg.go#L64). Both zero → args byte-identical to today. Input-seek (`-ss` before `-i`) is intentional for HLS: it lets ffmpeg jump to the segment containing the timestamp instead of decoding-and-discarding from the start.
- `TrimFile(ctx context.Context, opts TrimOptions) error` for the mp4/webm path:
  ```go
  type TrimOptions struct {
      InputPath, OutputPath string
      StartOffset, Duration time.Duration
      Timeout               time.Duration // default 120s
      OnSizeChange          func(int64)
  }
  ```
  Args: `-y -hide_banner -loglevel error -ss <start> -i <input> -t <duration> -c copy <output>`. Reuses the same stall-watchdog pattern as `DownloadHLS`.

### Changes to `cmd/x-dl/main.go`

- `cliOptions` ([`cmd/x-dl/main.go:43`](cmd/x-dl/main.go#L43)) adds `from, to string`.
- `parseArgs` ([`cmd/x-dl/main.go:186`](cmd/x-dl/main.go#L186)):
  - Register `--from`, `--to` via `fs.StringVar` (no short forms).
  - After parsing, call `clip.ParseSpec(from, to)` so a bad value fails before Chromium launches. Store the parsed `Spec` on `cliOptions`.
- `helpText` ([`cmd/x-dl/main.go:23`](cmd/x-dl/main.go#L23)) gains:
  - `--from <MM:SS>`, `--to <MM:SS>` with the format rule and "defaults to start/end of video".
  - A note: "Clip starts may shift by ~1–3s due to keyframe alignment."
  - New EXAMPLES line.
- `run` ([`cmd/x-dl/main.go:75`](cmd/x-dl/main.go#L75)) ordering:
  1. URL validation (unchanged).
  2. If `spec.IsRequested() && !opts.urlOnly`: check `ffmpeg.IsAvailable() && ffmpeg.IsFfprobeAvailable()`; error with install instructions (mirror tone of existing [`cmd/x-dl/main.go:150-157`](cmd/x-dl/main.go#L150)) if missing. Fails before extraction so misconfigured systems don't wait for Chromium.
  3. `extractor.Extract` (unchanged).
  4. Print tweet info + URL (unchanged).
  5. If `opts.urlOnly`: print URL and return — clip flags inert by design.
  6. `outPath = tweet.ResolveOutputPath(...)` (unchanged).
  7. If `spec.IsRequested()`:
     - `duration, err := ffmpeg.Probe(ctx, result.VideoURL)`
     - `r, err := clip.Resolve(spec, duration)`
     - If `opts.output == ""`: `outPath = clip.ClipFilename(outPath)`. Otherwise leave the explicit path untouched — same overwrite semantics as a non-clip download to an explicit path.
  8. Print "📁 Output path" (unchanged; now reflects `_clip` suffix when applicable).
  9. Dispatch:
     - m3u8 → `downloadHLS(ctx, url, outPath, r)` — pass through to `ffmpeg.DownloadHLS` with `StartOffset`/`Duration` set when `spec.IsRequested()`, zero otherwise.
     - else → `downloadDirect(ctx, url, outPath, r)`. When clipping: download to `os.CreateTemp("", "x-dl-*."+ext)`, `defer os.Remove(tmpPath)`, then `ffmpeg.TrimFile` from temp to `outPath` with a `progress.NewSpinner("Trimming video...")` mirroring the HLS spinner block ([`cmd/x-dl/main.go:160-174`](cmd/x-dl/main.go#L160)).
  10. On success, if `spec.IsRequested()`, print `✂️  Clipped to <MM:SS> → <MM:SS> (<M:SS>)` using `clip.FormatTimestamp` for the endpoints and `tweet.FormatTime(int(r.Duration.Seconds()))` for the length.

No changes needed to `internal/tweet/url.go`, `internal/downloader/downloader.go`, `internal/progress/progress.go`, or `internal/extractor/`.

### Tests

Unit tests in [`internal/clip/clip_test.go`](internal/clip/clip_test.go), table-driven in the style of [`internal/tweet/url_test.go`](internal/tweet/url_test.go):

- `TestParseTimestamp` — valid: `0:00`, `00:00`, `1:25`, `01:25`, `10:05`, `120:45`. Invalid: `30`, `0:01:25`, `1:25.5`, `01:60`, `01:5`, `abc`, `-5`, ``, `:30`, `1:`, `1:2`.
- `TestParseSpec` — both empty → `IsRequested()==false`; both unparseable → error names `--from`; only one unparseable → error names that flag; only From/only To sets the right `HasX` bools.
- `TestResolve` — full range, only-From, only-To, From≥To, From≥duration, To within 2s (clamp), To > duration+2s (error). Each error must include duration as `MM:SS`.
- `TestClipFilename` — `.mp4`/`.webm`/no-ext/relative path/dot-in-dir-name.
- `TestFormatTimestamp` — `10s` → `"00:10"`, `85s` → `"01:25"`, `7245s` → `"120:45"`.

End-to-end trim test in [`internal/ffmpeg/ffmpeg_trim_test.go`](internal/ffmpeg/ffmpeg_trim_test.go) (new):
- Fixture at [`internal/ffmpeg/testdata/fixture_10s.mp4`](internal/ffmpeg/testdata/fixture_10s.mp4) (~10–20 KB). Generated once via `ffmpeg -f lavfi -i color=c=black:s=128x72:r=10 -t 10 -c:v libx264 -preset ultrafast -pix_fmt yuv420p internal/ffmpeg/testdata/fixture_10s.mp4` and committed.
- Skip if `!IsAvailable() || !IsFfprobeAvailable()`.
- Trim 2s→7s from fixture; assert output duration via `Probe` is in `[3s, 7s]` (allows for keyframe snapping).

`cmd/x-dl/main.go` has no existing tests — not adding one. The end-to-end trim test plus the unit tests cover the meaningful logic.

## Critical files

Modified:

- [`cmd/x-dl/main.go`](cmd/x-dl/main.go) — new flags, help text, `run` orchestration, dispatch for clipped paths.
- [`internal/ffmpeg/ffmpeg.go`](internal/ffmpeg/ffmpeg.go) — `IsFfprobeAvailable`, `Probe`, extended `HLSOptions`, new `TrimFile`.

New:

- `internal/clip/clip.go` + `internal/clip/clip_test.go`.
- `internal/ffmpeg/ffmpeg_trim_test.go` + `internal/ffmpeg/testdata/fixture_10s.mp4`.

## Reuse

- `progress.NewSpinner` / `Start` / `Update` / `StopOK` ([`internal/progress/progress.go`](internal/progress/progress.go)) for the trim step's spinner — same usage shape as the existing HLS download.
- `tweet.FormatTime` ([`internal/tweet/url.go:175`](internal/tweet/url.go#L175)) for the parenthesized `M:SS` length.
- `tweet.ResolveOutputPath` ([`internal/tweet/url.go:200`](internal/tweet/url.go#L200)) unchanged; the `_clip` insertion is layered on top by the caller.
- `ffmpeg.IsAvailable` ([`internal/ffmpeg/ffmpeg.go:37`](internal/ffmpeg/ffmpeg.go#L37)) and the install-instructions error block at [`cmd/x-dl/main.go:150-157`](cmd/x-dl/main.go#L150) — copy the tone, extend the wording to name ffprobe too.
- The stall-watchdog pattern in `DownloadHLS` ([`internal/ffmpeg/ffmpeg.go:82-117`](internal/ffmpeg/ffmpeg.go#L82)) — copy into `TrimFile` (one duplication is fine; extract a private helper if a third use appears).

## Verification

End-to-end (manual, after implementation):

- `go test ./...` — unit tests pass; trim e2e passes when ffmpeg/ffprobe are on PATH.
- `go build ./cmd/x-dl && ./x-dl --help` — new flags listed; keyframe caveat present.
- `./x-dl https://x.com/<user>/status/<id>` — unchanged behavior; full video saved at `~/Downloads/<user>_<id>.mp4`.
- `./x-dl --from 0:05 --to 0:15 https://x.com/<user>/status/<id>` (mp4 source) — saves `~/Downloads/<user>_<id>_clip.mp4` ~10s long; `ffprobe -i <out> -show_entries format=duration` reports ~10s.
- Same flags on an HLS source (look for one with `result.Format == "m3u8"` in the URL printout) — same outcome.
- `./x-dl --from 0:10 https://x.com/.../<short>` — error mentioning the actual duration.
- `./x-dl --from 0:30 --to 0:10 https://x.com/...` — error before any download.
- `./x-dl --from 99 https://x.com/...` — parse error showing `MM:SS` format.
- `./x-dl --url-only --from 0:05 https://x.com/...` — prints URL, no ffmpeg check.
- `./x-dl -o ~/Movies/foo.mp4 --from 0:05 https://x.com/...` twice — both runs succeed; second overwrites the first (matches non-clip explicit-output behavior).
- Temporarily rename `ffprobe` on PATH and run with `--from 0:05` — install-instructions error before Chromium launches.

After the spec is implemented and verified, copy this plan to `_plans/clip-video-time-range.md` in the repo for the maintainer's reference (per the user's request).
