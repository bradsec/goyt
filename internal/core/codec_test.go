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

func TestParseMediaOutput(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  MediaInfo
	}{
		{"4K VP9 HDR", `{"streams":[{"codec_type":"video","codec_name":"vp9","width":3840,"height":2160,"profile":"Profile 2","color_transfer":"smpte2084"},{"codec_type":"audio","codec_name":"opus","channels":2,"bit_rate":"128000"}]}`, MediaInfo{VideoCodec: "vp9", AudioCodec: "opus", Resolution: "4K", HDR: true, AudioChannels: "Stereo", AudioBitrate: "128 kbps"}},
		{"cinema crop 4K", `{"streams":[{"codec_type":"video","codec_name":"vp9","width":3840,"height":1608}]}`, MediaInfo{VideoCodec: "vp9", Resolution: "4K"}},
		{"wide 720p", `{"streams":[{"codec_type":"video","codec_name":"h264","width":1280,"height":536}]}`, MediaInfo{VideoCodec: "h264", Resolution: "720p"}},
		{"8K", `{"streams":[{"codec_type":"video","codec_name":"av1","width":7680,"height":4320}]}`, MediaInfo{VideoCodec: "av1", Resolution: "8K"}},
		{"10-bit SDR is not HDR", `{"streams":[{"codec_type":"video","codec_name":"vp9","width":1920,"height":1080,"profile":"Profile 2","color_transfer":"bt709"}]}`, MediaInfo{VideoCodec: "vp9", Resolution: "1080p"}},
		{"odd size", `{"streams":[{"codec_type":"video","codec_name":"h264","width":640,"height":360}]}`, MediaInfo{VideoCodec: "h264", Resolution: "360p"}},
		{"portrait", `{"streams":[{"codec_type":"video","codec_name":"av1","width":1080,"height":1920}]}`, MediaInfo{VideoCodec: "av1", Resolution: "1080p"}},
		{"audio format bitrate", `{"streams":[{"codec_type":"audio","codec_name":"aac","channels":6}],"format":{"bit_rate":"320000"}}`, MediaInfo{AudioCodec: "aac", AudioChannels: "5.1", AudioBitrate: "320 kbps"}},
		{"HLG AV1", `{"streams":[{"codec_type":"video","codec_name":"av1","width":2560,"height":1440,"color_transfer":"arib-std-b67"}]}`, MediaInfo{VideoCodec: "av1", Resolution: "1440p", HDR: true}},
		{"unknown metadata", `{"streams":[{"codec_type":"audio","codec_name":"mp3","channels":1,"bit_rate":"N/A"}]}`, MediaInfo{AudioCodec: "mp3", AudioChannels: "Mono"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseMediaOutput([]byte(tc.input))
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}
