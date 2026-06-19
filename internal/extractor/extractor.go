// Package extractor drives a headless Chromium via chromedp to load a
// public tweet and pick the best video URL from network responses,
// DOM <video>/<source> elements, and the Performance API. Mirrors the
// scoring logic from src/extractor.ts.
package extractor

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"

	"github.com/nadeem-syed/x-dl-go/internal/tweet"
)

// Sentinel errors classify extraction failures for the CLI.
var (
	ErrInvalidURL = errors.New("invalid X/Twitter URL")
	ErrParse      = errors.New("failed to parse tweet URL")
	ErrLoginWall  = errors.New("tweet requires authentication")
	ErrNoVideo    = errors.New("no video found in tweet")
)

// Options configure a single extraction.
type Options struct {
	Timeout time.Duration // page load + network wait; defaults to 30s
	Headed  bool
}

// Result describes the chosen video and the parsed tweet.
type Result struct {
	VideoURL string
	Format   string // "mp4", "webm", "m3u8", "gif", ...
	Width    int
	Height   int
	Tweet    *tweet.Info
}

// Extract opens the tweet in Chromium and returns the best candidate
// video URL. Caller is responsible for the context lifecycle (e.g.
// signal-cancelled context for Ctrl-C).
func Extract(ctx context.Context, rawURL string, opts Options) (*Result, error) {
	if !tweet.IsValidURL(rawURL) {
		return nil, ErrInvalidURL
	}
	info := tweet.ParseURL(rawURL)
	if info == nil {
		return nil, ErrParse
	}
	if opts.Timeout == 0 {
		opts.Timeout = 30 * time.Second
	}

	allocOpts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", !opts.Headed),
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
	)
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(ctx, allocOpts...)
	defer cancelAlloc()

	browserCtx, cancelBrowser := chromedp.NewContext(allocCtx)
	defer cancelBrowser()

	// Listen for video.twimg.com responses BEFORE navigation so we
	// don't miss the first hits.
	var (
		netMu    sync.Mutex
		netSeen  = make(map[string]struct{})
		netURLs  []string
		htmlContent string
	)
	chromedp.ListenTarget(browserCtx, func(ev interface{}) {
		resp, ok := ev.(*network.EventResponseReceived)
		if !ok {
			return
		}
		u := resp.Response.URL
		if !strings.Contains(u, "video.twimg.com") {
			return
		}
		netMu.Lock()
		if _, dup := netSeen[u]; !dup {
			netSeen[u] = struct{}{}
			netURLs = append(netURLs, u)
		}
		netMu.Unlock()
	})

	// Run the page navigation under a separate timeout so a hung
	// network doesn't run away with the caller's context.
	navCtx, cancelNav := context.WithTimeout(browserCtx, opts.Timeout)
	defer cancelNav()

	if err := chromedp.Run(navCtx,
		network.Enable(),
		chromedp.Navigate(info.URL),
		chromedp.Sleep(1500*time.Millisecond),
		chromedp.OuterHTML("html", &htmlContent),
	); err != nil {
		return nil, fmt.Errorf("page load: %w", err)
	}

	// Best-effort: poke the video element so X starts streaming.
	_ = chromedp.Run(navCtx, chromedp.Evaluate(triggerPlaybackJS, nil))

	// Bounded wait for network candidates to show a known format.
	waitForCandidates(&netMu, &netURLs, 8*time.Second)

	// Collect fallback candidates from the page.
	var perfURLs, domURLs []string
	_ = chromedp.Run(navCtx, chromedp.Evaluate(getPerformanceJS, &perfURLs))
	_ = chromedp.Run(navCtx, chromedp.Evaluate(getDomURLsJS, &domURLs))

	netMu.Lock()
	allURLs := append([]string(nil), netURLs...)
	netMu.Unlock()
	allURLs = append(allURLs, perfURLs...)
	allURLs = append(allURLs, domURLs...)

	chosen := selectBest(allURLs)
	if chosen == nil {
		if hasLoginWall(htmlContent) {
			return nil, ErrLoginWall
		}
		return nil, ErrNoVideo
	}

	return &Result{
		VideoURL: chosen.url,
		Format:   chosen.format,
		Width:    chosen.width,
		Height:   chosen.height,
		Tweet:    info,
	}, nil
}

// ---------------------------------------------------------------------
// Selection logic — ported from selectBestVideoUrl + toCandidate +
// pickBestProgressiveCandidate in src/extractor.ts.
// ---------------------------------------------------------------------

type candidate struct {
	url       string
	format    string
	width     int
	height    int
	score     int
	audioOnly bool
}

var (
	resolutionRE = regexp.MustCompile(`/(\d+)x(\d+)/`)
	// Codec markers that only appear in variant playlists / streams,
	// not master playlists. /aud/ and /mp4a/ are audio-only, so they're
	// covered by audioOnly above; the rest mark video-only variants.
	codecPathRE = regexp.MustCompile(`/(avc1|h264|vp09|hevc)/`)
)

// isMasterPlaylist heuristically detects whether an X HLS URL points
// to the master playlist (lists all video+audio variants) vs. a
// variant playlist (single stream, usually video-only). Master:
//   .../pl/<hash>.m3u8
// Variant:
//   .../pl/avc1/1920x1920/<hash>.m3u8
func isMasterPlaylist(url string) bool {
	return !codecPathRE.MatchString(url) && !resolutionRE.MatchString(url)
}

func toCandidate(rawURL string) *candidate {
	clean := strings.SplitN(rawURL, "#", 2)[0]
	c := &candidate{
		url:       clean,
		format:    tweet.GetFormat(clean),
		audioOnly: strings.Contains(clean, "/aud/") || strings.Contains(clean, "/mp4a/") || strings.Contains(clean, "mp4a"),
	}
	if m := resolutionRE.FindStringSubmatch(clean); m != nil {
		fmt.Sscanf(m[1], "%d", &c.width)
		fmt.Sscanf(m[2], "%d", &c.height)
		c.score = c.width * c.height
	}
	if (c.format == "mp4" || c.format == "webm") && !c.audioOnly {
		c.score += 1_000_000_000
	}
	if c.format == "m3u8" && !c.audioOnly {
		// Strongly prefer the master playlist — it lists all variants,
		// and ffmpeg will merge video + audio. Picking a variant
		// playlist yields a video-only file (the audio-loss bug).
		if isMasterPlaylist(clean) {
			c.score += 3_000_000_000
		} else if strings.Contains(clean, "variant_version") {
			// Legacy URL pattern; treat as upper-tier variant.
			c.score += 2_000_000_000
		}
	}
	return c
}

func selectBest(allURLs []string) *candidate {
	seen := make(map[string]struct{})
	var parsed []*candidate
	for _, u := range allURLs {
		if _, dup := seen[u]; dup {
			continue
		}
		seen[u] = struct{}{}
		parsed = append(parsed, toCandidate(u))
	}

	var bestM3U8 *candidate
	for _, c := range parsed {
		if c.format != "m3u8" {
			continue
		}
		if bestM3U8 == nil || c.score > bestM3U8.score {
			bestM3U8 = c
		}
	}

	var progressive []*candidate
	for _, c := range parsed {
		if c.audioOnly {
			continue
		}
		switch c.format {
		case "m4s", "m4a", "ts", "m3u8":
			continue
		}
		progressive = append(progressive, c)
	}

	if len(progressive) > 0 {
		best := pickBestProgressive(progressive)
		size := contentLength(best.url)
		if size > 0 && size < 100*1024 && bestM3U8 != nil {
			return bestM3U8
		}
		return best
	}
	if bestM3U8 != nil {
		return bestM3U8
	}
	// Fallback: audio-only MP4 (matches TS behavior).
	for _, c := range parsed {
		if c.format == "mp4" {
			return c
		}
	}
	return nil
}

func pickBestProgressive(cands []*candidate) *candidate {
	// Sort by (has /pu/vid/ desc, score desc).
	scored := append([]*candidate(nil), cands...)
	sortCandidates(scored)

	best := scored[0]
	bestSize := int64(0)
	limit := len(scored)
	if limit > 6 {
		limit = 6
	}
	for i := 0; i < limit; i++ {
		size := contentLength(scored[i].url)
		if size <= 0 || size < 100*1024 {
			continue
		}
		if size > bestSize {
			best = scored[i]
			bestSize = size
		}
	}
	return best
}

// sortCandidates does an insertion sort (small N) by progressive-path
// preference then score.
func sortCandidates(cs []*candidate) {
	for i := 1; i < len(cs); i++ {
		j := i
		for j > 0 && less(cs[j], cs[j-1]) {
			cs[j-1], cs[j] = cs[j], cs[j-1]
			j--
		}
	}
}

func less(a, b *candidate) bool {
	ap, bp := 0, 0
	if strings.Contains(a.url, "/pu/vid/") {
		ap = 1
	}
	if strings.Contains(b.url, "/pu/vid/") {
		bp = 1
	}
	if ap != bp {
		return ap > bp
	}
	return a.score > b.score
}

// contentLength HEADs the URL with a 5s timeout and returns the
// Content-Length, or 0 if unknown.
func contentLength(rawURL string) int64 {
	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequest(http.MethodHead, rawURL, nil)
	if err != nil {
		return 0
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0
	}
	if resp.ContentLength > 0 {
		return resp.ContentLength
	}
	return 0
}

// waitForCandidates polls every 250ms for up to maxWait until a
// network candidate has a recognized video format.
func waitForCandidates(mu *sync.Mutex, urls *[]string, maxWait time.Duration) {
	deadline := time.Now().Add(maxWait)
	for time.Now().Before(deadline) {
		mu.Lock()
		for _, u := range *urls {
			f := tweet.GetFormat(u)
			if f == "mp4" || f == "m3u8" || f == "webm" || f == "gif" {
				mu.Unlock()
				return
			}
		}
		mu.Unlock()
		time.Sleep(250 * time.Millisecond)
	}
}

// hasLoginWall is a Go port of the TS heuristic.
func hasLoginWall(html string) bool {
	lower := strings.ToLower(html)
	indicators := []string{"log in", "sign up", "join the conversation", "sign in to view"}
	hasIndicator := false
	for _, ind := range indicators {
		if strings.Contains(lower, ind) {
			hasIndicator = true
			break
		}
	}
	hasAuth := strings.Contains(html, "Sign in") || strings.Contains(html, "Log in")
	return hasIndicator && hasAuth
}

// JS snippets injected into the page. Kept as raw strings so the
// double-quoted JS code stays readable.
const triggerPlaybackJS = `
(() => {
  try {
    const vids = Array.from(document.querySelectorAll('video'));
    for (const v of vids) {
      try { v.muted = true; const p = v.play && v.play(); if (p && p.catch) p.catch(() => {}); } catch (_) {}
    }
  } catch (_) {}
})();
`

const getPerformanceJS = `
(() => {
  try {
    const entries = performance.getEntriesByType('resource').map(r => String(r.name));
    return entries.filter(u => typeof u === 'string' && u.includes('video.twimg.com'));
  } catch (_) {
    return [];
  }
})()
`

const getDomURLsJS = `
(() => {
  try {
    const out = [];
    for (const v of Array.from(document.querySelectorAll('video'))) {
      if (v.currentSrc) out.push(v.currentSrc);
      if (v.src) out.push(v.src);
    }
    for (const s of Array.from(document.querySelectorAll('video source'))) {
      if (s.src) out.push(s.src);
    }
    return out.filter(u => typeof u === 'string' && u.includes('video.twimg.com'));
  } catch (_) {
    return [];
  }
})()
`
