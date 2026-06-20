package ffmpeg

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// fixturePath is a 10s 128x72 black-frame mp4 generated once via:
//
//	ffmpeg -f lavfi -i color=c=black:s=128x72:r=10 -t 10 \
//	  -c:v libx264 -preset ultrafast -pix_fmt yuv420p \
//	  internal/ffmpeg/testdata/fixture_10s.mp4
const fixturePath = "testdata/fixture_10s.mp4"

func TestProbe_Fixture(t *testing.T) {
	if !IsAvailable() || !IsFfprobeAvailable() {
		t.Skip("ffmpeg/ffprobe not on PATH")
	}
	d, err := Probe(context.Background(), fixturePath)
	if err != nil {
		t.Fatalf("Probe errored: %v", err)
	}
	if d < 9*time.Second || d > 11*time.Second {
		t.Errorf("Probe returned %v, want ~10s", d)
	}
}

func TestTrimFile_FullRange(t *testing.T) {
	if !IsAvailable() || !IsFfprobeAvailable() {
		t.Skip("ffmpeg/ffprobe not on PATH")
	}
	out := filepath.Join(t.TempDir(), "full.mp4")
	err := TrimFile(context.Background(), TrimOptions{
		InputPath:   fixturePath,
		OutputPath:  out,
		StartOffset: 0,
		Duration:    10 * time.Second,
	})
	if err != nil {
		t.Fatalf("TrimFile errored: %v", err)
	}
	d, err := Probe(context.Background(), out)
	if err != nil {
		t.Fatalf("Probe(out) errored: %v", err)
	}
	if d < 9*time.Second || d > 11*time.Second {
		t.Errorf("trimmed duration = %v, want ~10s", d)
	}
}

func TestTrimFile_MiddleSlice(t *testing.T) {
	if !IsAvailable() || !IsFfprobeAvailable() {
		t.Skip("ffmpeg/ffprobe not on PATH")
	}
	out := filepath.Join(t.TempDir(), "slice.mp4")
	err := TrimFile(context.Background(), TrimOptions{
		InputPath:   fixturePath,
		OutputPath:  out,
		StartOffset: 2 * time.Second,
		Duration:    5 * time.Second,
	})
	if err != nil {
		t.Fatalf("TrimFile errored: %v", err)
	}
	d, err := Probe(context.Background(), out)
	if err != nil {
		t.Fatalf("Probe(out) errored: %v", err)
	}
	// Generous bounds: stream-copy snaps to keyframes, so the output
	// may be a couple seconds longer than the requested 5s.
	if d < 3*time.Second || d > 7*time.Second {
		t.Errorf("trimmed duration = %v, want ~5s ±2s", d)
	}
}

func TestTrimFile_RequiresDuration(t *testing.T) {
	if !IsAvailable() {
		t.Skip("ffmpeg not on PATH")
	}
	err := TrimFile(context.Background(), TrimOptions{
		InputPath:  fixturePath,
		OutputPath: filepath.Join(t.TempDir(), "x.mp4"),
		Duration:   0,
	})
	if err == nil {
		t.Error("expected error when Duration is zero")
	}
}
