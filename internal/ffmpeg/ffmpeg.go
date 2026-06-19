// Package ffmpeg drives an ffmpeg subprocess to download HLS (m3u8)
// playlists. Mirrors the TS downloadHlsWithFfmpeg behavior: -c copy
// remux + aac_adtstoasc bitstream filter, with an overall timeout and
// a no-progress watchdog that kills a stalled ffmpeg.
package ffmpeg

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"
)

// ErrMissing is returned when ffmpeg is not found on PATH.
var ErrMissing = errors.New("ffmpeg not found on PATH")

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
}

// IsAvailable reports whether the ffmpeg binary exists on PATH.
func IsAvailable() bool {
	_, err := exec.LookPath("ffmpeg")
	return err == nil
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

	// Arguments are byte-for-byte the TS non-clipping case.
	args := []string{
		"-y",
		"-hide_banner",
		"-loglevel", "error",
		"-i", opts.PlaylistURL,
		"-c", "copy",
		"-bsf:a", "aac_adtstoasc",
		opts.OutputPath,
	}

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
