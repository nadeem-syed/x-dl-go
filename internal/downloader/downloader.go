// Package downloader streams an HTTP response to disk and emits
// throttled progress callbacks.
package downloader

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync/atomic"
	"time"
)

// ProgressFunc is invoked at most every 500ms during download (and
// once at completion). total is 0 if Content-Length was not provided.
type ProgressFunc func(percent float64, downloaded, total int64)

// Options configure a single download.
type Options struct {
	URL        string
	OutputPath string
	OnProgress ProgressFunc
}

// Download streams Options.URL to Options.OutputPath. Returns the
// final output path on success.
func Download(ctx context.Context, opts Options) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, opts.URL, nil)
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("http get: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("HTTP error! status: %d", resp.StatusCode)
	}

	total := resp.ContentLength // -1 when unknown
	if total < 0 {
		total = 0
	}

	out, err := os.Create(opts.OutputPath)
	if err != nil {
		return "", fmt.Errorf("create output: %w", err)
	}
	defer out.Close()

	var downloaded atomic.Int64
	var stopTicker chan struct{}
	if opts.OnProgress != nil && total > 0 {
		stopTicker = make(chan struct{})
		go func() {
			ticker := time.NewTicker(500 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-stopTicker:
					return
				case <-ticker.C:
					d := downloaded.Load()
					opts.OnProgress(float64(d)/float64(total)*100, d, total)
				}
			}
		}()
	}

	counter := &countingWriter{w: out, counter: &downloaded}
	_, copyErr := io.Copy(counter, resp.Body)

	if stopTicker != nil {
		close(stopTicker)
		// Final 100% tick so the UI ends on a clean number.
		d := downloaded.Load()
		if d > 0 && opts.OnProgress != nil {
			opts.OnProgress(float64(d)/float64(total)*100, d, total)
		}
	}

	if copyErr != nil {
		return "", fmt.Errorf("copy body: %w", copyErr)
	}
	return opts.OutputPath, nil
}

type countingWriter struct {
	w       io.Writer
	counter *atomic.Int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.counter.Add(int64(n))
	return n, err
}
