package core

import (
	"strings"
	"testing"
)

func TestBuildConvertArgs(t *testing.T) {
	d := NewDownloader("yt-dlp", "ffmpeg", "", false, false)
	args := d.buildConvertArgs("/in/video.webm", "/in/video.mp4.tmp", "/tmp/prog.txt")

	joined := strings.Join(args, " ")
	mustContain := []string{
		"-i", "/in/video.webm",
		"-c:v", "-c:a", "aac",
		"-movflags", "+faststart",
		"-progress", "/tmp/prog.txt",
		"-y", "/in/video.mp4.tmp",
	}
	for _, tok := range mustContain {
		if !strings.Contains(joined, tok) {
			t.Fatalf("args missing %q; got: %s", tok, joined)
		}
	}
	if !strings.Contains(joined, "libx264") {
		t.Fatalf("expected libx264 video encoder; got: %s", joined)
	}
	// The temp output ends in ".mp4.tmp", so the mp4 muxer must be forced.
	if !strings.Contains(joined, "-f mp4") {
		t.Fatalf("expected forced mp4 muxer (-f mp4); got: %s", joined)
	}
	if args[len(args)-1] != "/in/video.mp4.tmp" {
		t.Fatalf("expected output path last; got: %s", args[len(args)-1])
	}
}

func TestParseCodecOutput(t *testing.T) {
	cases := []struct {
		name           string
		in             string
		wantV, wantA   string
	}{
		{"type first", "video,h264\naudio,aac\n", "h264", "aac"},
		{"name first (real ffprobe)", "h264,video\naac,audio\n", "h264", "aac"},
		{"vp9 opus name first", "vp9,video\nopus,audio\n", "vp9", "opus"},
		{"video only", "av1,video\n", "av1", ""},
		{"audio only", "mp3,audio\n", "", "mp3"},
		{"empty", "", "", ""},
		{"first wins", "h264,video\nmjpeg,video\naac,audio\n", "h264", "aac"},
		{"crlf and spaces", "h264 , video\r\naac , audio\r\n", "h264", "aac"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v, a := parseCodecOutput(c.in)
			if v != c.wantV || a != c.wantA {
				t.Fatalf("parseCodecOutput(%q) = (%q,%q), want (%q,%q)", c.in, v, a, c.wantV, c.wantA)
			}
		})
	}
}
