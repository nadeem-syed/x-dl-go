# Spec: clip-video-time-range

branch: `claude/feature/clip-video-time-range`

## Summary

Allow the user to keep only a chosen portion of a tweet's video instead of saving the whole clip. The download command gains two optional flags, `--from` and `--to`, that specify the start and end of the desired range. When either flag is set, the saved file is trimmed to that range; when neither is set, behavior is unchanged from today (the full video is saved).

## Functional Requirements

### Flags

- `--from <MM:SS>` — start of the clip. If omitted, defaults to the beginning of the video (`00:00`).
- `--to <MM:SS>` — end of the clip. If omitted, defaults to the end of the video.
- If neither flag is provided, behavior is unchanged from today and the full video is saved.
- If only one is provided, the other defaults as above (start of video / end of video).

### CLI invocation

- Flag position must not change behavior. `x-dl <url> --from 0:10 --to 0:30` must produce the same result as `x-dl --from 0:10 --to 0:30 <url>`. Any other interleaving of flags and the positional URL is equally valid.
- This applies to every existing flag too (`--output`, `--url-only`, `--timeout`, `--headed`), not just the new ones — a side effect of fixing this for `--from`/`--to` is that the whole CLI gains position-independence.

### Timestamp format

- Format is `<minutes>:<seconds>` where:
  - **Minutes** is one or more digits with no upper bound (e.g. `0`, `1`, `01`, `120`).
  - **Seconds** is exactly two digits in `00`–`59`.
- Valid examples: `0:00`, `00:00`, `1:25`, `01:25`, `10:05`, `120:45`.
- Invalid examples (must be rejected with a parse error showing the accepted format):
  `30` (seconds only), `0:01:25` (HH:MM:SS), `1:25.5` (fractional seconds), `01:60` (seconds out of range), `01:5` (seconds not two digits), `abc`, `-5`, empty string.
- If both `--from` and `--to` are unparseable, fail on the first one encountered (left-to-right on argv).

### Tool requirements

- Clipping always requires `ffmpeg` (and `ffprobe`, which is bundled with it). This is a first-class requirement: if `--from` or `--to` is present and `ffmpeg`/`ffprobe` is missing, the command exits non-zero with the install instructions — even for tweets whose video format would normally download via direct HTTP (no ffmpeg).

### Trim mode (fast / stream-copy)

- Default and only mode is **stream copy** (`ffmpeg -c copy`) — fast, no re-encode.
- Stream copy snaps cuts to the nearest keyframe, so the actual clip start may be off from the requested `--from` by up to one GOP (~1–3 seconds for typical X videos). The CLI must surface this in `--help` so users aren't surprised.
- No `--precise` / re-encode toggle in this version.

### Duration validation

- Before downloading, run `ffprobe` against the selected video URL to read its duration.
- Validate:
  - `--from` < video duration (else error with actual duration formatted as `MM:SS`).
  - `--to` ≤ video duration. If `--to` slightly exceeds duration (e.g. by < 2s), clamp to the duration silently. Beyond that, error.
  - `--from` < `--to` (else error with both values).
- Pre-validation lets us reject impossible ranges without wasting bandwidth.

### Output filename

- When *any* clip flag is supplied (even `--from 00:00` alone), append `_clip` before the extension. Example: `username_123456_clip.mp4`.
- This guarantees a clipped download never silently overwrites a previously-saved full version of the same tweet.
- The user-supplied `--output` path, if given, is honored as-is (no automatic `_clip` insertion) — the user is responsible for naming the file when they pass an explicit path with an extension.

### User-facing output

- After a successful clip, print a confirmation line of the form:
  `✂️  Clipped to 00:10 → 01:25 (1:15)`
  showing the start, end, and resulting duration.
- During the ffmpeg trim step, display the standard progress spinner from `internal/progress` so the UX matches the existing HLS download flow.

### Scope limitations

- Single-video tweets only. Multi-video tweet support is not in scope for this feature — the existing extractor returns one video per tweet, and multi-video extraction is a separate concern.

## Pipeline Integration

The existing pipeline is: `extract → choose URL/format → download (direct HTTP for mp4/webm, ffmpeg for m3u8)`.

With clipping enabled, the new branches are:

- **m3u8 source**: pass `-ss <from>` and `-t <duration>` directly to the existing ffmpeg HLS download so only the requested range is fetched and remuxed.
- **mp4/webm source**: download the full file via the existing HTTP downloader to a temp path, then run `ffmpeg -ss <from> -i <tmp> -t <duration> -c copy <output>_clip.<ext>`, then delete the temp.

In both cases the trim step is stream-copy, and the spinner runs during the ffmpeg invocation.

## Acceptance Criteria

- With no clip flags, the produced output file is byte-identical to the pre-feature behavior (no ffmpeg trim invoked when none was needed).
- Flag position invariance: `x-dl <url> --from 0:10 --to 0:30` and `x-dl --from 0:10 --to 0:30 <url>` produce identical output. The clipped file exists, has the `_clip` suffix, and has the expected duration in both invocations.
- `--from 00:10 --to 00:30` on a video longer than 30 seconds produces a file whose duration (per `ffprobe`) is `20s ± 2s` and starts within `~2s` of the 10-second mark. (Tolerance reflects keyframe alignment in stream-copy mode.)
- Only `--from 00:05` → output starts within `~2s` of 5 seconds, ends at the original end.
- Only `--to 00:15` → output starts at 0, ends within `~2s` of 15 seconds.
- `--from 00:30 --to 00:10` exits non-zero **before any download** with an error naming both values.
- `--from 99:59` on a 60-second video exits non-zero **before any download** with an error that includes the actual duration in `MM:SS`.
- `--to 01:00` on a 58-second video clamps to 58s silently (within 2s tolerance) and succeeds.
- `--to 02:00` on a 58-second video exits non-zero with the actual duration in the error.
- Any value violating `MM:SS` (e.g. `30`, `1:25.5`, `0:01:25`, `01:60`, `01:5`, `abc`, empty) exits non-zero with a parse error naming the offending flag and showing the accepted format.
- `ffmpeg` or `ffprobe` missing → exits non-zero with install instructions identical in tone to the existing HLS-missing-ffmpeg error.
- The clipped output filename ends in `_clip.<ext>` (when no explicit `--output` path is given) so it cannot overwrite the full-video download for the same tweet.
- `--help` lists `--from` and `--to`, states the `MM:SS` format with the actual rule (one-or-more-digit minutes, two-digit seconds 00–59), notes the keyframe-alignment caveat, and describes default behavior when either flag is omitted.

## Testing Guidelines

Place tests in `internal/clip/clip_test.go` (or wherever the clipping code lives — a separate package from the existing download/ffmpeg code so the parser can be unit-tested without ffmpeg). Cover:

- **Parser**: table-driven tests for the `MM:SS` parser — accepts the valid examples above, rejects the invalid ones with a useful error message in each case.
- **Range validation**: rejects `--from >= --to`, rejects `--from` and `--to` greater than a known duration, accepts `--to` within 2s of duration (clamps silently).
- **No-flag passthrough**: assert that when neither flag is set, the code path does NOT call into the trim step. Verify via a mockable interface, not by running ffmpeg.
- **One-flag defaulting**: only `--from` defaults `--to` to duration; only `--to` defaults `--from` to 0.
- **Filename**: assert `_clip` suffix is inserted before the extension on the auto-generated filename, and is NOT inserted when `--output` is an explicit path.
- **End-to-end on a fixture**: a small mp4 fixture committed to `testdata/` with known duration (e.g. `fixture_10s.mp4`, exactly 10s). Run the trim path on it, check `ffprobe`-reported duration of the output is within tolerance.
- **Error-reporting order**: when both `--from` and `--to` are unparseable, the error names the leftmost one only.

Keep the test suite focused — don't try to cover every ffmpeg invariant. The fixture-based end-to-end test is the safety net for "did we wire ffmpeg correctly"; the parser/validation tests are the safety net for "did we wire the CLI correctly."

## Out of Scope

- Multi-video tweets.
- Precise (re-encode) trim mode and a `--precise` toggle to switch into it.
- Trimming an already-downloaded local file (e.g. a separate `clip` subcommand).
- Clipping in `--url-only` mode. When `--url-only` is set, `--from`/`--to` are silently ignored (no ffmpeg check, no probe, no warning) — `--url-only` doesn't download, so there's nothing to clip.
- Image-only / pure-GIF tweets — the extractor errors with `ErrNoVideo` before clip flags are consulted, so the "ignore with warning" path is unreachable.

## Changes from the Original Draft

Each Open Question in the original draft is now resolved inline in the requirements above. The following ambiguities were also closed:

- Dropped `1:25` from the invalid-examples list (the resolved minutes rule makes it valid).
- Pinned the output suffix to `_clip` (was "`-clip` or the range — pick one").
- Made `ffmpeg`+`ffprobe` an explicit first-class requirement (previously only listed under Edge Cases).
- Stated the keyframe-alignment tolerance (±~2s) so Acceptance Criteria are testable, not vague.
- Specified when duration validation happens (pre-download via `ffprobe`) so the implementation knows whether to bring in `ffprobe`.
- Fail-on-first for chained unparseable flags.
- Filename rule clarified: auto-generated path gets `_clip`; user-supplied `--output` does not.
- Multi-video support and pure-image edge case moved to Out of Scope (unreachable today).
