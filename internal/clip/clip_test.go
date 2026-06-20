package clip

import (
	"strings"
	"testing"
	"time"
)

func TestParseTimestamp(t *testing.T) {
	valid := []struct {
		in   string
		want time.Duration
	}{
		{"0:00", 0},
		{"00:00", 0},
		{"1:25", 85 * time.Second},
		{"01:25", 85 * time.Second},
		{"10:05", 10*time.Minute + 5*time.Second},
		{"120:45", 120*time.Minute + 45*time.Second},
		{"0:01", time.Second},
		{"0:59", 59 * time.Second},
	}
	for _, tc := range valid {
		t.Run("valid/"+tc.in, func(t *testing.T) {
			got, err := ParseTimestamp(tc.in)
			if err != nil {
				t.Fatalf("ParseTimestamp(%q) errored: %v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("ParseTimestamp(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}

	invalid := []string{
		"30",      // seconds only
		"0:01:25", // HH:MM:SS
		"1:25.5",  // fractional seconds
		"01:60",   // seconds out of range
		"01:5",    // seconds not two digits
		"abc",
		"-5",
		"",
		":30",
		"1:",
		"1:2",
		" 1:25",
		"1:25 ",
		"1:2a",
	}
	for _, in := range invalid {
		t.Run("invalid/"+in, func(t *testing.T) {
			_, err := ParseTimestamp(in)
			if err == nil {
				t.Errorf("ParseTimestamp(%q) should have errored", in)
				return
			}
			if !strings.Contains(err.Error(), "MM:SS") {
				t.Errorf("ParseTimestamp(%q) error should mention MM:SS, got: %v", in, err)
			}
		})
	}
}

func TestParseSpec(t *testing.T) {
	t.Run("both empty", func(t *testing.T) {
		got, err := ParseSpec("", "")
		if err != nil {
			t.Fatalf("ParseSpec errored: %v", err)
		}
		if got.IsRequested() {
			t.Errorf("empty spec should not be requested, got %+v", got)
		}
	})

	t.Run("only from", func(t *testing.T) {
		got, err := ParseSpec("0:10", "")
		if err != nil {
			t.Fatalf("ParseSpec errored: %v", err)
		}
		if !got.HasFrom || got.HasTo {
			t.Errorf("expected HasFrom=true, HasTo=false; got %+v", got)
		}
		if got.From != 10*time.Second {
			t.Errorf("From = %v, want 10s", got.From)
		}
	})

	t.Run("only to", func(t *testing.T) {
		got, err := ParseSpec("", "0:30")
		if err != nil {
			t.Fatalf("ParseSpec errored: %v", err)
		}
		if got.HasFrom || !got.HasTo {
			t.Errorf("expected HasFrom=false, HasTo=true; got %+v", got)
		}
		if got.To != 30*time.Second {
			t.Errorf("To = %v, want 30s", got.To)
		}
	})

	t.Run("both bad — error names --from (leftmost)", func(t *testing.T) {
		_, err := ParseSpec("bad-from-value", "bad-to-value")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "--from") {
			t.Errorf("error should name --from, got: %v", err)
		}
		if strings.Contains(err.Error(), "--to") {
			t.Errorf("error should NOT name --to (leftmost rule), got: %v", err)
		}
		if !strings.Contains(err.Error(), "bad-from-value") {
			t.Errorf("error should quote the offending value, got: %v", err)
		}
	})

	t.Run("only --from bad", func(t *testing.T) {
		_, err := ParseSpec("xx", "0:30")
		if err == nil || !strings.Contains(err.Error(), "--from") {
			t.Errorf("expected --from error, got: %v", err)
		}
	})

	t.Run("only --to bad", func(t *testing.T) {
		_, err := ParseSpec("0:05", "xx")
		if err == nil || !strings.Contains(err.Error(), "--to") {
			t.Errorf("expected --to error, got: %v", err)
		}
	})

	t.Run("from >= to errors at parse time", func(t *testing.T) {
		_, err := ParseSpec("0:30", "0:10")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "00:30") || !strings.Contains(err.Error(), "00:10") {
			t.Errorf("error should name both values, got: %v", err)
		}
	})

	t.Run("from == to errors at parse time", func(t *testing.T) {
		_, err := ParseSpec("0:10", "0:10")
		if err == nil {
			t.Error("expected error when from equals to")
		}
	})

	t.Run("from < to is fine", func(t *testing.T) {
		got, err := ParseSpec("0:05", "0:10")
		if err != nil {
			t.Fatalf("errored: %v", err)
		}
		if !got.HasFrom || !got.HasTo {
			t.Errorf("expected both Has flags set, got %+v", got)
		}
	})
}

func TestResolve(t *testing.T) {
	const sixty = 60 * time.Second

	t.Run("full range", func(t *testing.T) {
		got, err := Resolve(Spec{From: 10 * time.Second, To: 30 * time.Second, HasFrom: true, HasTo: true}, sixty)
		if err != nil {
			t.Fatalf("errored: %v", err)
		}
		want := Range{Start: 10 * time.Second, End: 30 * time.Second, Duration: 20 * time.Second}
		if got != want {
			t.Errorf("got %+v, want %+v", got, want)
		}
	})

	t.Run("only from defaults to end", func(t *testing.T) {
		got, err := Resolve(Spec{From: 10 * time.Second, HasFrom: true}, sixty)
		if err != nil {
			t.Fatalf("errored: %v", err)
		}
		want := Range{Start: 10 * time.Second, End: sixty, Duration: 50 * time.Second}
		if got != want {
			t.Errorf("got %+v, want %+v", got, want)
		}
	})

	t.Run("only to defaults from to zero", func(t *testing.T) {
		got, err := Resolve(Spec{To: 15 * time.Second, HasTo: true}, sixty)
		if err != nil {
			t.Fatalf("errored: %v", err)
		}
		want := Range{Start: 0, End: 15 * time.Second, Duration: 15 * time.Second}
		if got != want {
			t.Errorf("got %+v, want %+v", got, want)
		}
	})

	t.Run("from >= duration", func(t *testing.T) {
		_, err := Resolve(Spec{From: 99 * time.Minute, HasFrom: true}, sixty)
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "01:00") {
			t.Errorf("error should include actual duration as MM:SS, got: %v", err)
		}
	})

	t.Run("from == duration", func(t *testing.T) {
		_, err := Resolve(Spec{From: sixty, HasFrom: true}, sixty)
		if err == nil {
			t.Error("expected error when from equals duration")
		}
	})

	t.Run("from >= to", func(t *testing.T) {
		_, err := Resolve(Spec{From: 30 * time.Second, To: 10 * time.Second, HasFrom: true, HasTo: true}, sixty)
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "00:30") || !strings.Contains(err.Error(), "00:10") {
			t.Errorf("error should name both values, got: %v", err)
		}
	})

	t.Run("to within 2s of duration clamps silently", func(t *testing.T) {
		got, err := Resolve(Spec{To: 59500 * time.Millisecond, HasTo: true}, 58*time.Second) // To=59.5s, dur=58s
		if err != nil {
			t.Fatalf("errored: %v", err)
		}
		if got.End != 58*time.Second {
			t.Errorf("End should clamp to 58s, got %v", got.End)
		}
	})

	t.Run("to beyond 2s of duration errors", func(t *testing.T) {
		_, err := Resolve(Spec{To: 2 * time.Minute, HasTo: true}, 58*time.Second)
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "00:58") {
			t.Errorf("error should include actual duration, got: %v", err)
		}
	})

	t.Run("zero duration errors", func(t *testing.T) {
		_, err := Resolve(Spec{}, 0)
		if err == nil {
			t.Error("expected error for zero duration")
		}
	})
}

func TestIsRequested(t *testing.T) {
	cases := []struct {
		spec Spec
		want bool
	}{
		{Spec{}, false},
		{Spec{HasFrom: true}, true},
		{Spec{HasTo: true}, true},
		{Spec{HasFrom: true, HasTo: true}, true},
	}
	for _, tc := range cases {
		if got := tc.spec.IsRequested(); got != tc.want {
			t.Errorf("Spec %+v IsRequested() = %v, want %v", tc.spec, got, tc.want)
		}
	}
}

func TestFormatTimestamp(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want string
	}{
		{0, "00:00"},
		{10 * time.Second, "00:10"},
		{85 * time.Second, "01:25"},
		{60 * time.Second, "01:00"},
		{7245 * time.Second, "120:45"},
		{-5 * time.Second, "00:00"},
	}
	for _, tc := range cases {
		if got := FormatTimestamp(tc.in); got != tc.want {
			t.Errorf("FormatTimestamp(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestClipFilename(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"/x/u_123.mp4", "/x/u_123_clip.mp4"},
		{"/x/u_123.webm", "/x/u_123_clip.webm"},
		{"/x/u_123", "/x/u_123_clip"},
		{"u_123.mp4", "u_123_clip.mp4"},
		{"/x.y/u_123.mp4", "/x.y/u_123_clip.mp4"},
		{"video.tar.gz", "video.tar_clip.gz"}, // filepath.Ext returns ".gz" — documented quirk
	}
	for _, tc := range cases {
		if got := ClipFilename(tc.in); got != tc.want {
			t.Errorf("ClipFilename(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
