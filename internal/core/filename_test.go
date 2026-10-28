package core

import (
	"strings"
	"testing"
)

func TestSanitizeFilename(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain ascii lowercased", "Alpha Bravo.mp4", "alpha bravo.mp4"},
		{"strips emoji and symbols", "Alpha \U0001F525\U0001F525 [Bravo]!.mp4", "alpha bravo.mp4"},
		{"drops non-latin and collapses spaces", "Alpha (éèê) x Bravo 'Charlie'.mp4", "alpha x bravo charlie.mp4"},
		{"keeps digits", "Alpha 10 Bravo 2026.mp4", "alpha 10 bravo 2026.mp4"},
		{"empty after strip falls back", "\U0001F525\U0001F389.mp4", "download.mp4"},
		{"uppercase extension lowercased", "Alpha.MP4", "alpha.mp4"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := SanitizeFilename(c.in)
			if got != c.want {
				t.Errorf("SanitizeFilename(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestSanitizeFilenameRejectsUnsafeExtension(t *testing.T) {
	// Titles come from the remote site and become the yt-dlp output template,
	// so a fake "extension" must not smuggle in separators or template fields.
	for _, title := range []string{"Clip./x", "Clip.%(x)s", "Clip.a\\b", "Clip.a:b"} {
		got := SanitizeFilename(title)
		if strings.ContainsAny(got, "/\\%():") {
			t.Errorf("SanitizeFilename(%q) = %q; contains unsafe characters", title, got)
		}
	}
	if got := SanitizeFilename("Track.mp4"); got != "track.mp4" {
		t.Errorf("SanitizeFilename(%q) = %q; want %q", "Track.mp4", got, "track.mp4")
	}
}

func TestQualityFilename(t *testing.T) {
	for _, tc := range []struct {
		name, path string
		info       MediaInfo
		want       string
	}{
		{"4K HDR 5.1", "/d/dino.mp4", MediaInfo{VideoCodec: "vp9", Resolution: "4K", HDR: true, AudioChannels: "5.1", AudioBitrate: "384 kbps"}, "/d/dino [4K HDR 5.1 384kbps].mp4"},
		{"1080p stereo", "/d/clip.mkv", MediaInfo{VideoCodec: "h264", Resolution: "1080p", AudioChannels: "Stereo", AudioBitrate: "128 kbps"}, "/d/clip [1080p stereo 128kbps].mkv"},
		{"audio only", "/d/song.mp3", MediaInfo{AudioCodec: "mp3", AudioChannels: "Stereo", AudioBitrate: "245 kbps"}, "/d/song [stereo 245kbps].mp3"},
		{"replaces old suffix after convert", "/d/dino [4K HDR 5.1 384kbps].mp4", MediaInfo{VideoCodec: "h264", Resolution: "1080p", AudioChannels: "Stereo", AudioBitrate: "128 kbps"}, "/d/dino [1080p stereo 128kbps].mp4"},
		{"keeps title brackets that are not a suffix", "/d/live [remastered].mp4", MediaInfo{VideoCodec: "h264", Resolution: "720p"}, "/d/live [remastered] [720p].mp4"},
		{"nothing known", "/d/x.mp4", MediaInfo{}, "/d/x.mp4"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := QualityFilename(tc.path, tc.info); got != tc.want {
				t.Errorf("QualityFilename(%q) = %q, want %q", tc.path, got, tc.want)
			}
		})
	}
}
