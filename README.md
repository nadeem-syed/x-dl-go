# x-dl

A small Go CLI that extracts and downloads videos from public
X/Twitter tweets. Ships as a single standalone binary — the only
runtime dependencies are Chrome (always) and `ffmpeg` (for HLS
downloads).

## Roadmap

Currently supports public-tweet extraction and download. Possible
future additions:

- Authenticated downloads for private / login-walled tweets
- Clipping a downloaded video to a specific time range
- Auto-installer for `ffmpeg`

## Features

- Extract videos from public X/Twitter tweets
- Direct download for MP4 / WebM
- HLS (m3u8) download via `ffmpeg`, with master-playlist selection so
  video and audio are merged into one file
- Optional `--url-only` mode (prints the video URL, no download)
- Bounded timeout, signal-aware (Ctrl-C cleans up the browser)
- Single ~11 MB binary; only system dependencies are Chrome/Chromium
  and (for HLS) `ffmpeg`

## Prerequisites

- **Go** 1.21+ (for building from source)
- **Google Chrome** or Chromium — chromedp finds them automatically on
  the standard system paths
- **ffmpeg** — only required for HLS (`.m3u8`) downloads; direct MP4
  downloads work without it

## Install

Currently from source only — there are no pre-built release binaries
yet.

```sh
git clone https://github.com/nadeem-syed/x-dl-go.git
cd x-dl-go
make install                # builds and copies to ~/.local/bin/x-dl
```

Ensure `~/.local/bin` is on your `PATH`:

```sh
echo 'export PATH="$HOME/.local/bin:$PATH"' >> ~/.zshrc   # or ~/.bashrc
source ~/.zshrc
x-dl --help
```

Optionally pre-install ffmpeg (and verify Chrome is present):

```sh
make deps-system
```

## Usage

```
x-dl [OPTIONS] <URL>
```

| Flag                | Default        | Description                                    |
|---------------------|----------------|------------------------------------------------|
| `--url`, `-u <url>` | —              | Tweet URL (alternative to positional argument) |
| `--output`, `-o`    | `~/Downloads`  | Output directory or file path                  |
| `--url-only`        | false          | Print the video URL and exit; don't download   |
| `--timeout <secs>`  | 30             | Page-load timeout                              |
| `--headed`          | false          | Show the browser window (debugging)            |
| `--version`, `-v`   | —              | Print version                                  |
| `--help`, `-h`      | —              | Show help                                      |

### Examples

Download to the default location (`~/Downloads`):

```sh
x-dl https://x.com/Remotion/status/2013626968386765291
```

Print the video URL without downloading:

```sh
x-dl --url-only https://x.com/Remotion/status/2013626968386765291
```

Choose an output directory or file:

```sh
x-dl -o ~/Movies https://x.com/user/status/123456
x-dl -o ~/Movies/clip.mp4 https://x.com/user/status/123456
```

## How it works

1. Launch headless Chromium via [chromedp][chromedp].
2. Listen for `video.twimg.com` responses while loading the tweet, and
   collect candidates from the DOM `<video>` / `<source>` elements and
   the Performance API as a fallback.
3. Score candidates:
   - Strongly prefer **master HLS playlists** (no `/avc1/`, `/h264/`,
     or `/WxH/` segments in the URL) — these list both video and audio
     tracks and let ffmpeg merge them.
   - Fall back to progressive MP4/WebM if available.
   - Use the highest-resolution candidate within a tier.
4. Download:
   - `.mp4` / `.webm` / `.gif` → direct streaming HTTP download.
   - `.m3u8` → spawn `ffmpeg -c copy -bsf:a aac_adtstoasc` to remux into MP4.

[chromedp]: https://github.com/chromedp/chromedp

## Project structure

```
cmd/x-dl/main.go              CLI entry, arg parsing, orchestration
internal/tweet/url.go         URL validation, parsing, filename / output path
internal/extractor/           chromedp browser automation + selection scoring
internal/downloader/          Streaming HTTP download with progress callback
internal/ffmpeg/              HLS download subprocess with no-progress watchdog
internal/progress/            Terminal spinner + progress-line helpers
```

## Development

```sh
make help          # list all targets
make               # default: vet + test + build
make build         # → dist/x-dl
make test          # go test ./...
make vet           # go vet ./...
make tidy          # go mod tidy
make clean         # rm -rf dist/
make install       # build + copy to ~/.local/bin/x-dl
make cross         # cross-compile for macos-arm64, macos-intel, linux-x64
make deps          # go mod download
make deps-system   # install ffmpeg (brew / apt / dnf / ...) and check for Chrome
```

## Limitations

- **Public tweets only** — private and login-walled tweets are not
  supported.
- **HLS requires ffmpeg** — without it, only direct MP4/WebM tweets
  download. The CLI prints clear install instructions when ffmpeg is
  needed and missing.
- **Time-limited URLs** — X video URLs may expire after a few hours.
- **Rate limiting** — X may throttle excessive requests.

## License

MIT
