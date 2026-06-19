package tweet

import "testing"

func TestIsValidURL(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"https://twitter.com/user/status/123456", true},
		{"https://www.twitter.com/user/status/123456", true},
		{"http://twitter.com/user/status/123456", true},
		{"https://x.com/user/status/123456", true},
		{"https://www.x.com/user/status/123456", true},
		{"http://x.com/user/status/123456", true},
		{"https://google.com", false},
		{"https://twitter.com", false},
		{"https://twitter.com/user", false},
		{"not-a-url", false},
		{"", false},
		{"https://twitter.com/user/status/abc", false},
		{"https://twitter.com/user/status/", false},
	}
	for _, c := range cases {
		if got := IsValidURL(c.in); got != c.want {
			t.Errorf("IsValidURL(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestParseURL(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want *Info
	}{
		{
			"twitter",
			"https://twitter.com/user/status/123456",
			&Info{ID: "123456", Author: "user", URL: "https://twitter.com/user/status/123456"},
		},
		{
			"x.com",
			"https://x.com/user/status/789012",
			&Info{ID: "789012", Author: "user", URL: "https://x.com/user/status/789012"},
		},
		{
			"query params stripped",
			"https://twitter.com/user/status/123456?s=20",
			&Info{ID: "123456", Author: "user", URL: "https://twitter.com/user/status/123456"},
		},
		{
			"username with underscores",
			"https://x.com/some_user_name/status/123456",
			&Info{ID: "123456", Author: "some_user_name", URL: "https://x.com/some_user_name/status/123456"},
		},
		{"invalid: google", "https://google.com", nil},
		{"invalid: no status", "https://twitter.com/user", nil},
		{"invalid: not a url", "invalid-url", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ParseURL(c.in)
			if c.want == nil {
				if got != nil {
					t.Errorf("got %+v, want nil", got)
				}
				return
			}
			if got == nil || *got != *c.want {
				t.Errorf("got %+v, want %+v", got, c.want)
			}
		})
	}
}

func TestGenerateFilename(t *testing.T) {
	info := &Info{ID: "123456", Author: "user"}
	if got := GenerateFilename(info, "mp4"); got != "user_123456.mp4" {
		t.Errorf("got %q", got)
	}
	if got := GenerateFilename(info, "gif"); got != "user_123456.gif" {
		t.Errorf("got %q", got)
	}
	if got := GenerateFilename(&Info{ID: "123456", Author: "some_user"}, "mp4"); got != "some_user_123456.mp4" {
		t.Errorf("got %q", got)
	}
}

func TestSanitizeFilename(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"file/name.mp4", "file_name.mp4"},
		{"file@name.mp4", "file_name.mp4"},
		{"file name.mp4", "file_name.mp4"},
		{"file___name", "file_name"},
	}
	for _, c := range cases {
		if got := SanitizeFilename(c.in); got != c.want {
			t.Errorf("SanitizeFilename(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	long := make([]byte, 300)
	for i := range long {
		long[i] = 'a'
	}
	if got := SanitizeFilename(string(long)); len(got) > 255 {
		t.Errorf("length = %d, want <= 255", len(got))
	}
}

func TestGetFormat(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"https://video.twimg.com/tweet_video.mp4", "mp4"},
		{"https://video.twimg.com/tweet_video.MP4", "mp4"},
		{"https://video.twimg.com/playlist.m3u8", "m3u8"},
		{"https://video.twimg.com/tweet_video.gif", "gif"},
		{"https://example.com/video.webm", "webm"},
		{"https://example.com/video.WEBM", "webm"},
		{"https://example.com/video.mov", "mov"},
		{"https://example.com/video.mkv", "mkv"},
		{"https://pbs.twimg.com/tweet_video_thumb", "unknown"},
		{"https://video.twimg.com/video.mp4?tag=12", "mp4"},
		{"https://video.twimg.com/video.mp4#t=10", "mp4"},
		{"https://video.twimg.com/segment.m4s", "m4s"},
		{"https://video.twimg.com/audio.m4a", "m4a"},
		{"https://video.twimg.com/segment.ts", "ts"},
	}
	for _, c := range cases {
		if got := GetFormat(c.in); got != c.want {
			t.Errorf("GetFormat(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSelectBestMP4(t *testing.T) {
	mixed := []Video{
		{URL: "video1.m3u8", Format: "m3u8"},
		{URL: "video2.mp4", Format: "mp4"},
		{URL: "video3.gif", Format: "gif"},
	}
	if got := SelectBestMP4(mixed); got != "video2.mp4" {
		t.Errorf("got %q", got)
	}
	multi := []Video{
		{URL: "video1.mp4", Format: "mp4"},
		{URL: "video2.mp4", Format: "mp4"},
	}
	if got := SelectBestMP4(multi); got != "video1.mp4" {
		t.Errorf("got %q", got)
	}
	none := []Video{
		{URL: "video1.m3u8", Format: "m3u8"},
		{URL: "video2.gif", Format: "gif"},
	}
	if got := SelectBestMP4(none); got != "" {
		t.Errorf("got %q, want empty", got)
	}
	if got := SelectBestMP4(nil); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestFormatBytes(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0 B"},
		{500, "500 B"},
		{1024, "1 KB"},
		{1536, "1.5 KB"},
		{1048576, "1 MB"},
	}
	for _, c := range cases {
		if got := FormatBytes(c.in); got != c.want {
			t.Errorf("FormatBytes(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestFormatTime(t *testing.T) {
	cases := []struct {
		in   int
		want string
	}{
		{0, "0:00"},
		{5, "0:05"},
		{60, "1:00"},
		{75, "1:15"},
		{3600, "60:00"},
	}
	for _, c := range cases {
		if got := FormatTime(c.in); got != c.want {
			t.Errorf("FormatTime(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestResolveOutputPath(t *testing.T) {
	info := &Info{ID: "123", Author: "u"}
	if got := ResolveOutputPath("/tmp/x.mp4", info, "mp4"); got != "/tmp/x.mp4" {
		t.Errorf("explicit file path: got %q", got)
	}
	if got := ResolveOutputPath("/tmp", info, "mp4"); got != "/tmp/u_123.mp4" {
		t.Errorf("dir path: got %q", got)
	}
	if got := ResolveOutputPath("/tmp/", info, "mp4"); got != "/tmp/u_123.mp4" {
		t.Errorf("trailing slash: got %q", got)
	}
}
