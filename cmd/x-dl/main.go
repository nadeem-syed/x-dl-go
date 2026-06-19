// x-dl CLI: extract and download videos from public X/Twitter tweets.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

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
  --timeout <seconds>  Page load timeout in seconds (default: 30)
  --headed             Show the browser window (debugging)
  --version, -v        Print version
  --help, -h           Show this message

EXAMPLES:
  x-dl https://x.com/user/status/123456
  x-dl --url-only https://x.com/user/status/123456
  x-dl -o ~/Movies https://x.com/user/status/123456
`

type cliOptions struct {
	url            string
	output         string
	urlOnly        bool
	timeoutSeconds int
	headed         bool
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
	fmt.Printf("📁 Output path: %s\n", outPath)

	if result.Format == "m3u8" {
		return downloadHLS(ctx, result.VideoURL, outPath)
	}
	return downloadDirect(ctx, result.VideoURL, outPath)
}

func downloadDirect(ctx context.Context, url, outPath string) error {
	fmt.Printf("📥 Downloading from: %s\n", url)
	start := time.Now()

	_, err := downloader.Download(ctx, downloader.Options{
		URL:        url,
		OutputPath: outPath,
		OnProgress: func(percent float64, downloaded, total int64) {
			progress.PrintProgress(percent, downloaded, total)
		},
	})
	progress.ClearLine()
	if err != nil {
		return fmt.Errorf("download failed: %w", err)
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

func downloadHLS(ctx context.Context, url, outPath string) error {
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
func parseArgs(args []string) (*cliOptions, error) {
	fs := flag.NewFlagSet("x-dl", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() { fmt.Fprint(os.Stderr, helpText) }

	var (
		urlLong, urlShort     string
		outLong, outShort     string
		urlOnly, headed       bool
		showVer, showHelp     bool
		verShort, helpShort   bool
		timeoutSeconds        int
	)
	fs.StringVar(&urlLong, "url", "", "tweet URL")
	fs.StringVar(&urlShort, "u", "", "tweet URL (short)")
	fs.StringVar(&outLong, "output", "", "output dir or file path")
	fs.StringVar(&outShort, "o", "", "output dir or file path (short)")
	fs.BoolVar(&urlOnly, "url-only", false, "print video URL only")
	fs.IntVar(&timeoutSeconds, "timeout", 30, "page load timeout in seconds")
	fs.BoolVar(&headed, "headed", false, "show the browser window")
	fs.BoolVar(&showVer, "version", false, "print version")
	fs.BoolVar(&verShort, "v", false, "print version (short)")
	fs.BoolVar(&showHelp, "help", false, "print help")
	fs.BoolVar(&helpShort, "h", false, "print help (short)")

	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	if showHelp || helpShort {
		fmt.Print(helpText)
		return nil, nil
	}
	if showVer || verShort {
		fmt.Println(version)
		return nil, nil
	}

	if timeoutSeconds <= 0 {
		return nil, fmt.Errorf("--timeout must be a positive integer (got %d)", timeoutSeconds)
	}

	url := firstNonEmpty(urlLong, urlShort)
	out := firstNonEmpty(outLong, outShort)

	if url == "" {
		// Look for a positional URL (must be the first non-flag remaining).
		rest := fs.Args()
		if len(rest) > 0 {
			url = rest[0]
		}
	}
	if url == "" {
		return nil, errors.New("no URL provided")
	}

	return &cliOptions{
		url:            url,
		output:         out,
		urlOnly:        urlOnly,
		timeoutSeconds: timeoutSeconds,
		headed:         headed,
	}, nil
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
