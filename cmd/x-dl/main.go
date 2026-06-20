// x-dl CLI: extract and download videos from public X/Twitter tweets.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/pflag"

	"github.com/nadeem-syed/x-dl-go/internal/clip"
	"github.com/nadeem-syed/x-dl-go/internal/downloader"
	"github.com/nadeem-syed/x-dl-go/internal/extractor"
	"github.com/nadeem-syed/x-dl-go/internal/ffmpeg"
	"github.com/nadeem-syed/x-dl-go/internal/progress"
	"github.com/nadeem-syed/x-dl-go/internal/tweet"
)

const version = "0.1.0"

const helpText = `x-dl - Download videos from X/Twitter tweets

USAGE:
  x-dl [OPTIONS] <URL>

OPTIONS:
  --url, -u <url>      Tweet URL to extract from
  --output, -o <path>  Output directory or file path (default: ~/Downloads)
  --url-only           Print the video URL and exit, don't download
  --from <MM:SS>       Clip start (default: start of video). Ignored with --url-only.
  --to <MM:SS>         Clip end   (default: end of video).   Ignored with --url-only.
                       Minutes: one or more digits. Seconds: two digits 00-59.
                       Clip starts may shift by ~1-3s due to keyframe alignment.
                       Requires ffmpeg and ffprobe on PATH.
  --timeout <seconds>  Page load timeout in seconds (default: 30)
  --headed             Show the browser window (debugging)
  --version, -v        Print version
  --help, -h           Show this message

EXAMPLES:
  x-dl https://x.com/user/status/123456
  x-dl --url-only https://x.com/user/status/123456
  x-dl -o ~/Movies https://x.com/user/status/123456
  x-dl --from 0:10 --to 1:25 https://x.com/user/status/123456
`

type cliOptions struct {
	url            string
	output         string
	urlOnly        bool
	timeoutSeconds int
	headed         bool
	clipSpec       clip.Spec
}

func main() {
	opts, err := parseArgs(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, "❌", err)
		fmt.Fprintln(os.Stderr, "\nRun: x-dl --help for usage")
		os.Exit(1)
	}
	if opts == nil {
		// --help or --version already handled.
		return
	}

	// Signal-aware context so Ctrl-C cancels both the browser and any
	// in-flight ffmpeg/download.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, *opts); err != nil {
		progress.ClearLine()
		fmt.Fprintln(os.Stderr, "❌", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, opts cliOptions) error {
	if !tweet.IsValidURL(opts.url) {
		return fmt.Errorf("invalid X/Twitter URL: %s", opts.url)
	}

	// Fail fast on missing ffmpeg/ffprobe when clipping is requested,
	// so a misconfigured system doesn't wait for Chromium to start.
	// --url-only doesn't download, so clip flags are silently inert there.
	if opts.clipSpec.IsRequested() && !opts.urlOnly {
		if !ffmpeg.IsAvailable() || !ffmpeg.IsFfprobeAvailable() {
			return errors.New(`--from/--to require both ffmpeg and ffprobe on PATH.
Please install ffmpeg (ffprobe ships with it):
  macOS:   brew install ffmpeg
  Linux:   sudo apt-get install ffmpeg`)
		}
	}

	fmt.Println("🎬 x-dl - X/Twitter Video Extractor")
	fmt.Println()
	fmt.Printf("🎥 Extracting video from: %s\n", opts.url)

	result, err := extractor.Extract(ctx, opts.url, extractor.Options{
		Timeout: time.Duration(opts.timeoutSeconds) * time.Second,
		Headed:  opts.headed,
	})
	if err != nil {
		switch {
		case errors.Is(err, extractor.ErrLoginWall):
			return fmt.Errorf("this tweet requires authentication (CDP mode not in v1)")
		case errors.Is(err, extractor.ErrNoVideo):
			return errors.New("no video found in this tweet")
		default:
			return err
		}
	}

	fmt.Printf("📝 Tweet: @%s (ID: %s)\n", result.Tweet.Author, result.Tweet.ID)
	fmt.Printf("✅ Video extracted: %s\n", result.VideoURL)

	if opts.urlOnly {
		fmt.Println()
		fmt.Println(result.VideoURL)
		return nil
	}

	ext := "mp4"
	if result.Format == "webm" || result.Format == "gif" {
		ext = result.Format
	}
	outPath := tweet.ResolveOutputPath(opts.output, result.Tweet, ext)

	// Resolve the clip range against the actual video duration, and
	// insert the "_clip" suffix in auto-generated paths so a clipped
	// download doesn't silently overwrite a previously-saved full
	// version. Explicit --output paths are left untouched (same as
	// non-clip explicit-output behavior).
	var clipRange clip.Range
	if opts.clipSpec.IsRequested() {
		duration, err := ffmpeg.Probe(ctx, result.VideoURL)
		if err != nil {
			return fmt.Errorf("probe duration: %w", err)
		}
		r, err := clip.Resolve(opts.clipSpec, duration)
		if err != nil {
			return err
		}
		clipRange = r
		if opts.output == "" {
			outPath = clip.ClipFilename(outPath)
		}
	}

	fmt.Printf("📁 Output path: %s\n", outPath)

	if result.Format == "m3u8" {
		if err := downloadHLS(ctx, result.VideoURL, outPath, clipRange); err != nil {
			return err
		}
	} else {
		if err := downloadDirect(ctx, result.VideoURL, outPath, ext, clipRange); err != nil {
			return err
		}
	}

	if opts.clipSpec.IsRequested() {
		fmt.Printf("✂️  Clipped to %s → %s (%s)\n",
			clip.FormatTimestamp(clipRange.Start),
			clip.FormatTimestamp(clipRange.End),
			tweet.FormatTime(int(clipRange.Duration.Seconds())),
		)
	}
	return nil
}

// downloadDirect downloads the video via HTTP. When clipRange.Duration
// is non-zero, the file is first downloaded to a temp path, then
// trimmed via ffmpeg into outPath; otherwise it's written straight to
// outPath as before.
func downloadDirect(ctx context.Context, url, outPath, ext string, clipRange clip.Range) error {
	downloadPath := outPath
	if clipRange.Duration > 0 {
		tmpFile, err := os.CreateTemp("", "x-dl-*."+ext)
		if err != nil {
			return fmt.Errorf("create temp file: %w", err)
		}
		downloadPath = tmpFile.Name()
		tmpFile.Close()
		defer os.Remove(downloadPath)
	}

	fmt.Printf("📥 Downloading from: %s\n", url)
	start := time.Now()

	_, err := downloader.Download(ctx, downloader.Options{
		URL:        url,
		OutputPath: downloadPath,
		OnProgress: func(percent float64, downloaded, total int64) {
			progress.PrintProgress(percent, downloaded, total)
		},
	})
	progress.ClearLine()
	if err != nil {
		return fmt.Errorf("download failed: %w", err)
	}

	if clipRange.Duration > 0 {
		spin := progress.NewSpinner("Trimming video...")
		spin.Start()
		trimErr := ffmpeg.TrimFile(ctx, ffmpeg.TrimOptions{
			InputPath:   downloadPath,
			OutputPath:  outPath,
			StartOffset: clipRange.Start,
			Duration:    clipRange.Duration,
			OnSizeChange: func(bytes int64) {
				spin.Update(fmt.Sprintf("Trimming video... (%s)", tweet.FormatBytes(bytes)))
			},
		})
		if trimErr != nil {
			spin.Stop()
			return fmt.Errorf("trim failed: %w", trimErr)
		}
		spin.StopOK("✅ Trim completed")
	}

	info, _ := os.Stat(outPath)
	size := int64(0)
	if info != nil {
		size = info.Size()
	}
	fmt.Printf("✅ Download completed in %s\n", tweet.FormatTime(int(time.Since(start).Seconds())))
	fmt.Printf("📦 Final size: %s\n", tweet.FormatBytes(size))
	fmt.Printf("\n✅ Video saved to: %s\n", outPath)
	return nil
}

func downloadHLS(ctx context.Context, url, outPath string, clipRange clip.Range) error {
	if !ffmpeg.IsAvailable() {
		return errors.New(`ffmpeg is required to download HLS (m3u8) videos.
Please install ffmpeg:
  macOS:   brew install ffmpeg
  Linux:   sudo apt-get install ffmpeg

Playlist URL:
` + url)
	}

	fmt.Println("📥 Downloading HLS video via ffmpeg...")
	spin := progress.NewSpinner("Downloading HLS...")
	spin.Start()

	err := ffmpeg.DownloadHLS(ctx, ffmpeg.HLSOptions{
		PlaylistURL: url,
		OutputPath:  outPath,
		StartOffset: clipRange.Start,
		Duration:    clipRange.Duration,
		OnSizeChange: func(bytes int64) {
			spin.Update(fmt.Sprintf("Downloading HLS... (%s)", tweet.FormatBytes(bytes)))
		},
	})
	if err != nil {
		spin.Stop()
		return fmt.Errorf("HLS download failed: %w", err)
	}
	spin.StopOK("✅ HLS download completed")

	info, _ := os.Stat(outPath)
	if info != nil {
		fmt.Printf("📦 Final size: %s\n", tweet.FormatBytes(info.Size()))
	}
	fmt.Printf("\n✅ Video saved to: %s\n", outPath)
	return nil
}

// parseArgs returns a nil *cliOptions when --help or --version was
// handled (caller should exit 0).
//
// Uses pflag (GNU-style) so flags may appear before, after, or
// interleaved with the positional URL — stdlib flag stops parsing at
// the first positional, which would silently drop flags-after-URL.
func parseArgs(args []string) (*cliOptions, error) {
	fs := pflag.NewFlagSet("x-dl", pflag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() { fmt.Fprint(os.Stderr, helpText) }

	var (
		urlFlag, outFlag string
		fromFlag, toFlag string
		urlOnly, headed  bool
		showVer, showHelp bool
		timeoutSeconds   int
	)
	fs.StringVarP(&urlFlag, "url", "u", "", "tweet URL")
	fs.StringVarP(&outFlag, "output", "o", "", "output dir or file path")
	fs.StringVar(&fromFlag, "from", "", "clip start (MM:SS)")
	fs.StringVar(&toFlag, "to", "", "clip end (MM:SS)")
	fs.BoolVar(&urlOnly, "url-only", false, "print video URL only")
	fs.IntVar(&timeoutSeconds, "timeout", 30, "page load timeout in seconds")
	fs.BoolVar(&headed, "headed", false, "show the browser window")
	fs.BoolVarP(&showVer, "version", "v", false, "print version")
	fs.BoolVarP(&showHelp, "help", "h", false, "print help")

	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	if showHelp {
		fmt.Print(helpText)
		return nil, nil
	}
	if showVer {
		fmt.Println(version)
		return nil, nil
	}

	if timeoutSeconds <= 0 {
		return nil, fmt.Errorf("--timeout must be a positive integer (got %d)", timeoutSeconds)
	}

	url := urlFlag
	if url == "" {
		// Look for a positional URL (first non-flag remaining).
		rest := fs.Args()
		if len(rest) > 0 {
			url = rest[0]
		}
	}
	if url == "" {
		return nil, errors.New("no URL provided")
	}

	clipSpec, err := clip.ParseSpec(fromFlag, toFlag)
	if err != nil {
		return nil, err
	}

	return &cliOptions{
		url:            url,
		output:         outFlag,
		urlOnly:        urlOnly,
		timeoutSeconds: timeoutSeconds,
		headed:         headed,
		clipSpec:       clipSpec,
	}, nil
}
