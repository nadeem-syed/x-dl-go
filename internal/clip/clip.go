// Package clip parses, validates, and formats the --from/--to time
// range used by the x-dl clip feature. Pure functions, no I/O — the
// subprocess work lives in internal/ffmpeg.
package clip

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// clampTolerance is how much --to is allowed to exceed the video's
// actual duration before we error. Anything within this window is
// silently clamped to the duration.
const clampTolerance = 2 * time.Second

// Spec is what the user asked for on the command line, before we know
// the video's duration. Either field can be zero-valued if its flag
// wasn't set; HasFrom/HasTo are the source of truth for "was this set."
type Spec struct {
	From    time.Duration
	To      time.Duration
	HasFrom bool
	HasTo   bool
}

// IsRequested reports whether the user asked for any clipping. False
// means the rest of the pipeline runs unchanged.
func (s Spec) IsRequested() bool { return s.HasFrom || s.HasTo }

// Range is a validated, duration-resolved clip ready to hand to ffmpeg.
type Range struct {
	Start    time.Duration
	End      time.Duration
	Duration time.Duration // == End - Start
}

// mmssPattern accepts one-or-more-digit minutes and exactly two-digit
// seconds in 00-59. Spec rule.
var mmssPattern = regexp.MustCompile(`^(\d+):([0-5]\d)$`)

// ParseTimestamp parses a strict MM:SS string. Minutes: one or more
// digits, no upper bound. Seconds: exactly two digits, 00-59.
func ParseTimestamp(s string) (time.Duration, error) {
	m := mmssPattern.FindStringSubmatch(s)
	if m == nil {
		return 0, fmt.Errorf("invalid timestamp %q: expected MM:SS (e.g. 1:25 or 01:25), minutes are one or more digits, seconds are two digits 00-59", s)
	}
	mins, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, fmt.Errorf("invalid timestamp %q: %w", s, err)
	}
	secs, err := strconv.Atoi(m[2])
	if err != nil {
		return 0, fmt.Errorf("invalid timestamp %q: %w", s, err)
	}
	return time.Duration(mins)*time.Minute + time.Duration(secs)*time.Second, nil
}

// ParseSpec parses the raw --from / --to flag values. Empty string
// means "not set". If both are non-empty and unparseable, the error
// names --from (left-to-right on argv). When both are set, also
// enforces --from < --to here so the ordering bug fails at parse time
// instead of after extraction.
func ParseSpec(fromFlag, toFlag string) (Spec, error) {
	var spec Spec
	if fromFlag != "" {
		d, err := ParseTimestamp(fromFlag)
		if err != nil {
			return Spec{}, fmt.Errorf("--from: %w", err)
		}
		spec.From = d
		spec.HasFrom = true
	}
	if toFlag != "" {
		d, err := ParseTimestamp(toFlag)
		if err != nil {
			return Spec{}, fmt.Errorf("--to: %w", err)
		}
		spec.To = d
		spec.HasTo = true
	}
	if spec.HasFrom && spec.HasTo && spec.From >= spec.To {
		return Spec{}, fmt.Errorf("--from %s must be before --to %s", FormatTimestamp(spec.From), FormatTimestamp(spec.To))
	}
	return spec, nil
}

// Resolve combines a parsed Spec with the video's actual duration:
//   - Defaults: unset From -> 0; unset To -> duration.
//   - Clamps To to duration when To exceeds it by <= 2s.
//   - Errors:
//     From >= duration  (error includes actual duration as MM:SS)
//     To  >  duration + 2s tolerance (error includes actual duration)
//     From >= To        (error includes both values)
//
// The returned Range has Start = From, End = To, Duration = To - From.
func Resolve(spec Spec, duration time.Duration) (Range, error) {
	if duration <= 0 {
		return Range{}, errors.New("video duration is zero; cannot clip")
	}

	start := time.Duration(0)
	if spec.HasFrom {
		start = spec.From
	}
	end := duration
	if spec.HasTo {
		end = spec.To
	}

	if start >= duration {
		return Range{}, fmt.Errorf("--from %s is at or beyond video duration %s", FormatTimestamp(start), FormatTimestamp(duration))
	}
	if spec.HasTo && end > duration {
		if end-duration <= clampTolerance {
			end = duration
		} else {
			return Range{}, fmt.Errorf("--to %s exceeds video duration %s", FormatTimestamp(end), FormatTimestamp(duration))
		}
	}
	if start >= end {
		return Range{}, fmt.Errorf("--from %s must be before --to %s", FormatTimestamp(start), FormatTimestamp(end))
	}

	return Range{Start: start, End: end, Duration: end - start}, nil
}

// FormatTimestamp renders a duration as zero-padded MM:SS for clip
// endpoints: "00:10", "01:25", "120:45". Negative inputs are clamped
// to zero.
func FormatTimestamp(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	secs := int(d.Seconds())
	return fmt.Sprintf("%02d:%02d", secs/60, secs%60)
}

// ClipFilename inserts "_clip" before the extension of an auto-
// generated output path. "/x/u_123.mp4" -> "/x/u_123_clip.mp4".
// If the path has no extension, "_clip" is appended at the end.
// Callers MUST NOT pass user-supplied --output paths to this function.
func ClipFilename(path string) string {
	ext := filepath.Ext(path)
	if ext == "" {
		return path + "_clip"
	}
	base := strings.TrimSuffix(path, ext)
	return base + "_clip" + ext
}
