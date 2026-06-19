// Package tweet handles tweet URL validation, parsing, filename
// generation, and output-path resolution. Pure functions, no I/O.
package tweet

import (
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

// Info is a parsed tweet URL.
type Info struct {
	ID     string
	Author string
	URL    string // origin + pathname (no query/fragment)
}

// Video describes a candidate video URL with its detected format.
type Video struct {
	URL    string
	Format string
	Width  int
	Height int
}

var validURLPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)^https?://(www\.)?twitter\.com/[\w]+/status/[\d]+`),
	regexp.MustCompile(`(?i)^https?://(www\.)?x\.com/[\w]+/status/[\d]+`),
	regexp.MustCompile(`(?i)^https?://(localhost|127\.0\.0\.1)(:\d+)?/[\w]+/status/[\d]+`),
}

// IsValidURL reports whether s looks like a tweet URL we can handle.
func IsValidURL(s string) bool {
	for _, p := range validURLPatterns {
		if p.MatchString(s) {
			return true
		}
	}
	return false
}

var numericID = regexp.MustCompile(`^\d+$`)

// ParseURL returns the parsed Info for a tweet URL, or nil if it's
// invalid or malformed.
func ParseURL(raw string) *Info {
	if !IsValidURL(raw) {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil
	}
	parts := strings.Split(u.Path, "/")
	statusIdx := -1
	for i, p := range parts {
		if p == "status" {
			statusIdx = i
			break
		}
	}
	if statusIdx <= 0 || statusIdx+1 >= len(parts) {
		return nil
	}
	author := parts[statusIdx-1]
	id := parts[statusIdx+1]
	if author == "" || id == "" || !numericID.MatchString(id) {
		return nil
	}
	return &Info{
		ID:     id,
		Author: author,
		URL:    u.Scheme + "://" + u.Host + u.Path,
	}
}

// GenerateFilename returns "<author>_<id>.<ext>".
func GenerateFilename(info *Info, ext string) string {
	if ext == "" {
		ext = "mp4"
	}
	return fmt.Sprintf("%s_%s.%s", info.Author, info.ID, ext)
}

var (
	invalidFilenameChars = regexp.MustCompile(`[^a-zA-Z0-9._-]`)
	multipleUnderscores  = regexp.MustCompile(`_{2,}`)
)

// SanitizeFilename replaces invalid characters with "_", collapses
// runs of "_", and caps the length at 255 bytes.
func SanitizeFilename(name string) string {
	out := invalidFilenameChars.ReplaceAllString(name, "_")
	out = multipleUnderscores.ReplaceAllString(out, "_")
	if len(out) > 255 {
		out = out[:255]
	}
	return out
}

var extensionPattern = regexp.MustCompile(`(?i)\.([a-z0-9]+)$`)

// GetFormat extracts the video format from a URL pathname (e.g. "mp4",
// "m3u8", "gif"), or "unknown" if it can't be determined. The
// twimg.com/tweet_video special case mirrors how X serves animated
// GIFs as MP4 with no file extension.
func GetFormat(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "unknown"
	}
	pathname := strings.ToLower(u.Path)
	if m := extensionPattern.FindStringSubmatch(pathname); m != nil {
		return strings.ToLower(m[1])
	}
	if strings.Contains(pathname, "twimg.com/tweet_video") {
		return "gif"
	}
	return "unknown"
}

// SelectBestMP4 returns the first MP4 URL in videos, or "" if none.
// Matches the TS implementation's behavior (first match wins).
func SelectBestMP4(videos []Video) string {
	for _, v := range videos {
		if v.Format == "mp4" {
			return v.URL
		}
	}
	return ""
}

var resolutionPattern = regexp.MustCompile(`/(\d+)x(\d+)/`)

// ExtractBitrate parses a "/WxH/" segment from a URL and returns W*H
// as a proxy for bitrate ranking, or 0 if no resolution segment is
// present.
func ExtractBitrate(rawURL string) int {
	m := resolutionPattern.FindStringSubmatch(rawURL)
	if m == nil {
		return 0
	}
	var w, h int
	fmt.Sscanf(m[1], "%d", &w)
	fmt.Sscanf(m[2], "%d", &h)
	return w * h
}

// FormatBytes renders a byte count as a human-readable string
// (B/KB/MB/GB) with up to two decimal places.
func FormatBytes(n int64) string {
	if n == 0 {
		return "0 B"
	}
	const k = 1024.0
	units := []string{"B", "KB", "MB", "GB"}
	i := int(math.Floor(math.Log(float64(n)) / math.Log(k)))
	if i >= len(units) {
		i = len(units) - 1
	}
	val := float64(n) / math.Pow(k, float64(i))
	return fmt.Sprintf("%g %s", round2(val), units[i])
}

func round2(v float64) float64 {
	return math.Round(v*100) / 100
}

// FormatTime renders seconds as "M:SS".
func FormatTime(seconds int) string {
	if seconds < 0 {
		seconds = 0
	}
	return fmt.Sprintf("%d:%02d", seconds/60, seconds%60)
}

// DefaultDownloadsDir returns ~/Downloads on macOS/Linux, cwd otherwise.
func DefaultDownloadsDir() string {
	if runtime.GOOS == "darwin" || runtime.GOOS == "linux" {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, "Downloads")
		}
	}
	if cwd, err := os.Getwd(); err == nil {
		return cwd
	}
	return "."
}

// ResolveOutputPath mirrors the TS getOutputPath logic:
//   - no outFlag → defaultDownloadsDir/<author_id.ext>
//   - outFlag has an extension → use as-is
//   - outFlag ends in "/" → append the generated filename
//   - otherwise → treat as a directory and join the generated filename
func ResolveOutputPath(outFlag string, info *Info, ext string) string {
	if info == nil {
		return filepath.Join(DefaultDownloadsDir(), "video."+ext)
	}
	filename := GenerateFilename(info, ext)
	if outFlag == "" {
		return filepath.Join(DefaultDownloadsDir(), filename)
	}
	if filepath.Ext(outFlag) != "" {
		return outFlag
	}
	if strings.HasSuffix(outFlag, "/") {
		return outFlag + filename
	}
	return filepath.Join(outFlag, filename)
}
