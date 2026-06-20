// Package ffmpeg drives ffmpeg / ffprobe subprocesses for the x-dl
// download pipeline: HLS (m3u8) downloads, MP4/WebM trimming, and
// duration probing. -c copy remux + aac_adtstoasc bitstream filter,
// with overall timeouts and a no-progress watchdog that kills a
// stalled ffmpeg.
package ffmpeg

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// ErrMissing is returned when ffmpeg is not found on PATH.
var ErrMissing = errors.New("ffmpeg not found on PATH")

// ErrFfprobeMissing is returned when ffprobe is not found on PATH.
var ErrFfprobeMissing = errors.New("ffprobe not found on PATH")

// HLSOptions configure a single HLS download.
type HLSOptions struct {
	PlaylistURL string
	OutputPath  string

	// Timeout bounds total wall-clock time. Defaults to 120s.
	Timeout time.Duration
	// NoProgressTimeout kills ffmpeg if the output file size hasn't
	// grown for this long. Defaults to 60s.
	NoProgressTimeout time.Duration

	// OnSizeChange fires every ~2s with the current output-file size
	// so the caller can update a spinner label. Optional.
	OnSizeChange func(bytes int64)

	// StartOffset and Duration, when Duration > 0, clip the download
	// to that range via "-ss <StartOffset>" before "-i" and
	// "-t <Duration>" after "-i". Both zero means "download the whole
	// playlist" — args are byte-for-byte the no-clip case.
	StartOffset time.Duration
	Duration    time.Duration
}

// TrimOptions configure a single mp4/webm trim invocation.
type TrimOptions struct {
	InputPath  string
	OutputPath string

	StartOffset time.Duration
	Duration    time.Duration

	// Timeout bounds total wall-clock time. Defaults to 120s.
	Timeout time.Duration
	// NoProgressTimeout kills ffmpeg if the output file size hasn't
	// grown for this long. Defaults to 60s.
	NoProgressTimeout time.Duration

	// OnSizeChange fires every ~2s with the current output-file size
	// so the caller can update a spinner label. Optional.
	OnSizeChange func(bytes int64)
}

// IsAvailable reports whether the ffmpeg binary exists on PATH.
func IsAvailable() bool {
	_, err := exec.LookPath("ffmpeg")
	return err == nil
}

// IsFfprobeAvailable reports whether the ffprobe binary exists on PATH.
func IsFfprobeAvailable() bool {
	_, err := exec.LookPath("ffprobe")
	return err == nil
}

// Probe runs ffprobe against the given video URL or file path and
// returns the duration. Adds a 15s deadline derived from the caller's
// ctx so Ctrl-C still cancels.
func Probe(ctx context.Context, videoURL string) (time.Duration, error) {
	if !IsFfprobeAvailable() {
		return 0, ErrFfprobeMissing
	}

	probeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	args := []string{
		"-v", "error",
		"-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1",
		videoURL,
	}
	cmd := exec.CommandContext(probeCtx, "ffprobe", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if errors.Is(probeCtx.Err(), context.DeadlineExceeded) {
			return 0, errors.New("ffprobe timed out after 15s")
		}
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		stderrTrim := bytes.TrimSpace(stderr.Bytes())
		if len(stderrTrim) > 0 {
			return 0, fmt.Errorf("ffprobe: %s", string(stderrTrim))
		}
		return 0, fmt.Errorf("ffprobe: %w", err)
	}

	line := strings.TrimSpace(stdout.String())
	if line == "" || line == "N/A" {
		return 0, fmt.Errorf("ffprobe returned no duration for %s", videoURL)
	}
	secs, err := strconv.ParseFloat(line, 64)
	if err != nil {
		return 0, fmt.Errorf("ffprobe: unparseable duration %q: %w", line, err)
	}
	return time.Duration(secs * float64(time.Second)), nil
}

// formatSeconds renders a duration as bare seconds with millisecond
// precision for ffmpeg's -ss / -t flags (e.g. "10.500").
func formatSeconds(d time.Duration) string {
	return strconv.FormatFloat(d.Seconds(), 'f', 3, 64)
}

// DownloadHLS shells out to ffmpeg to download a playlist as MP4.
// The output file is overwritten if it exists.
func DownloadHLS(ctx context.Context, opts HLSOptions) error {
	if !IsAvailable() {
		return ErrMissing
	}
	if opts.Timeout == 0 {
		opts.Timeout = 120 * time.Second
	}
	if opts.NoProgressTimeout == 0 {
		opts.NoProgressTimeout = 60 * time.Second
	}

	// Wipe any stale file before ffmpeg writes the new one.
	_ = os.Remove(opts.OutputPath)

	// Overall-timeout context. Stacks on top of the caller's ctx so
	// Ctrl-C still cancels.
	runCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()

	// Arguments are byte-for-byte the TS non-clipping case unless the
	// caller asked for a clip (Duration > 0), in which case "-ss" is
	// spliced before "-i" (input-seek: ffmpeg jumps to the segment
	// containing the timestamp instead of decoding-and-discarding from
	// the start — what we want for HLS) and "-t" is appended after.
	args := []string{
		"-y",
		"-hide_banner",
		"-loglevel", "error",
	}
	if opts.Duration > 0 {
		args = append(args, "-ss", formatSeconds(opts.StartOffset))
	}
	args = append(args, "-i", opts.PlaylistURL)
	if opts.Duration > 0 {
		args = append(args, "-t", formatSeconds(opts.Duration))
	}
	args = append(args,
		"-c", "copy",
		"-bsf:a", "aac_adtstoasc",
		opts.OutputPath,
	)

	cmd := exec.CommandContext(runCtx, "ffmpeg", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start ffmpeg: %w", err)
	}

	// No-progress watchdog. Polls the output file size every 2s and
	// cancels runCtx if the size hasn't grown for NoProgressTimeout.
	stallCh := make(chan struct{})
	watchdogDone := make(chan struct{})
	go func() {
		defer close(watchdogDone)
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		var lastSize int64
		lastProgress := time.Now()
		for {
			select {
			case <-stallCh:
				return
			case <-runCtx.Done():
				return
			case now := <-ticker.C:
				info, err := os.Stat(opts.OutputPath)
				if err == nil {
					size := info.Size()
					if opts.OnSizeChange != nil {
						opts.OnSizeChange(size)
					}
					if size > lastSize {
						lastSize = size
						lastProgress = now
						continue
					}
				}
				if now.Sub(lastProgress) > opts.NoProgressTimeout {
					cancel() // kills ffmpeg via runCtx
					return
				}
			}
		}
	}()

	waitErr := cmd.Wait()
	close(stallCh)
	<-watchdogDone

	if waitErr != nil {
		// Distinguish timeout/cancel vs. ffmpeg failure for clearer
		// error messages.
		if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			return fmt.Errorf("ffmpeg timed out after %s", opts.Timeout)
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		stderrTrim := bytes.TrimSpace(stderr.Bytes())
		if len(stderrTrim) > 0 {
			return fmt.Errorf("ffmpeg: %s", string(stderrTrim))
		}
		return fmt.Errorf("ffmpeg: %w", waitErr)
	}
	return nil
}

// TrimFile shells out to ffmpeg to stream-copy a clip out of an
// already-downloaded local file. Used by the mp4/webm clip path; the
// HLS clip path pushes -ss/-t into DownloadHLS directly so only the
// requested segments are fetched.
//
// The watchdog mirrors DownloadHLS — duplicated rather than extracted
// per the plan ("one duplication is fine; refactor on the third use").
func TrimFile(ctx context.Context, opts TrimOptions) error {
	if !IsAvailable() {
		return ErrMissing
	}
	if opts.Duration <= 0 {
		return errors.New("TrimFile: Duration must be > 0")
	}
	if opts.Timeout == 0 {
		opts.Timeout = 120 * time.Second
	}
	if opts.NoProgressTimeout == 0 {
		opts.NoProgressTimeout = 60 * time.Second
	}

	_ = os.Remove(opts.OutputPath)

	runCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()

	args := []string{
		"-y",
		"-hide_banner",
		"-loglevel", "error",
		"-ss", formatSeconds(opts.StartOffset),
		"-i", opts.InputPath,
		"-t", formatSeconds(opts.Duration),
		"-c", "copy",
		opts.OutputPath,
	}

	cmd := exec.CommandContext(runCtx, "ffmpeg", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start ffmpeg: %w", err)
	}

	stallCh := make(chan struct{})
	watchdogDone := make(chan struct{})
	go func() {
		defer close(watchdogDone)
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		var lastSize int64
		lastProgress := time.Now()
		for {
			select {
			case <-stallCh:
				return
			case <-runCtx.Done():
				return
			case now := <-ticker.C:
				info, err := os.Stat(opts.OutputPath)
				if err == nil {
					size := info.Size()
					if opts.OnSizeChange != nil {
						opts.OnSizeChange(size)
					}
					if size > lastSize {
						lastSize = size
						lastProgress = now
						continue
					}
				}
				if now.Sub(lastProgress) > opts.NoProgressTimeout {
					cancel()
					return
				}
			}
		}
	}()

	waitErr := cmd.Wait()
	close(stallCh)
	<-watchdogDone

	if waitErr != nil {
		if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			return fmt.Errorf("ffmpeg timed out after %s", opts.Timeout)
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		stderrTrim := bytes.TrimSpace(stderr.Bytes())
		if len(stderrTrim) > 0 {
			return fmt.Errorf("ffmpeg: %s", string(stderrTrim))
		}
		return fmt.Errorf("ffmpeg: %w", waitErr)
	}
	return nil
}
