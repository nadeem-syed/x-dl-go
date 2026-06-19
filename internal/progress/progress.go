// Package progress provides terminal spinner and progress-line
// helpers that overwrite the current line via \r. Every caller MUST
// Stop the spinner before printing anything else, or the spinner glyph
// will linger on the success/error line.
package progress

import (
	"fmt"
	"os"
	"sync"
	"time"
)

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// Spinner is a goroutine-driven terminal spinner. Not safe for
// concurrent use of Update — callers update the label from one
// goroutine and Stop from another.
type Spinner struct {
	mu      sync.Mutex
	label   string
	stop    chan struct{}
	done    chan struct{}
	running bool
}

// NewSpinner returns a spinner with the given initial label. It does
// not start animating until Start is called.
func NewSpinner(label string) *Spinner {
	return &Spinner{label: label}
}

// Start begins the spinner animation in its own goroutine.
func (s *Spinner) Start() {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return
	}
	s.stop = make(chan struct{})
	s.done = make(chan struct{})
	s.running = true
	s.mu.Unlock()

	go func() {
		defer close(s.done)
		ticker := time.NewTicker(80 * time.Millisecond)
		defer ticker.Stop()
		i := 0
		for {
			select {
			case <-s.stop:
				return
			case <-ticker.C:
				s.mu.Lock()
				label := s.label
				s.mu.Unlock()
				fmt.Fprintf(os.Stdout, "\r%s %s", spinnerFrames[i], label)
				i = (i + 1) % len(spinnerFrames)
			}
		}
	}()
}

// Update swaps the spinner's label without restarting the goroutine.
func (s *Spinner) Update(label string) {
	s.mu.Lock()
	s.label = label
	s.mu.Unlock()
}

// Stop halts the spinner and clears the current line. Safe to call
// multiple times; safe to call before Start.
func (s *Spinner) Stop() {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return
	}
	s.running = false
	close(s.stop)
	s.mu.Unlock()
	<-s.done
	ClearLine()
}

// StopOK stops the spinner, clears the line, and prints msg followed
// by a newline. Use this for the success line so the spinner glyph
// never bleeds into it.
func (s *Spinner) StopOK(msg string) {
	s.Stop()
	fmt.Fprintln(os.Stdout, msg)
}

// ClearLine writes the ANSI sequence to clear the current line back to
// the start of the line. Useful from non-spinner paths (e.g. before
// printing an error after a \r-overwritten progress line).
func ClearLine() {
	fmt.Fprint(os.Stdout, "\r\x1b[K")
}

// PrintProgress writes a one-line download progress update that
// overwrites the previous line. percent is 0..100.
func PrintProgress(percent float64, downloaded, total int64) {
	fmt.Fprintf(os.Stdout, "\r⏳ Progress: %.1f%% (%s/%s)",
		percent, humanBytes(downloaded), humanBytes(total))
}

// humanBytes is a small local copy of tweet.FormatBytes to avoid a
// circular import. Kept in sync intentionally.
func humanBytes(n int64) string {
	const k = 1024.0
	units := []string{"B", "KB", "MB", "GB"}
	if n < int64(k) {
		return fmt.Sprintf("%d %s", n, units[0])
	}
	v := float64(n)
	i := 0
	for v >= k && i < len(units)-1 {
		v /= k
		i++
	}
	return fmt.Sprintf("%g %s", round2(v), units[i])
}

func round2(v float64) float64 {
	return float64(int(v*100+0.5)) / 100
}
