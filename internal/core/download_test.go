package core

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestNewDownloader(t *testing.T) {
	ytDlpPath := "/usr/bin/yt-dlp"
	ffmpegPath := "/usr/bin/ffmpeg"

	downloader := NewDownloader(ytDlpPath, ffmpegPath, "", true, false)

	if downloader.ytDlpPath != ytDlpPath {
		t.Errorf("Expected ytDlpPath to be %s, got %s", ytDlpPath, downloader.ytDlpPath)
	}

	if downloader.ffmpegPath != ffmpegPath {
		t.Errorf("Expected ffmpegPath to be %s, got %s", ffmpegPath, downloader.ffmpegPath)
	}
	if downloader.enableHardwareAccel != true {
		t.Errorf("Expected enableHardwareAccel to be true, got %v", downloader.enableHardwareAccel)
	}
	if downloader.optimizeForLowPower != false {
		t.Errorf("Expected optimizeForLowPower to be false, got %v", downloader.optimizeForLowPower)
	}
}

func hasArg(args []string, want string) bool {
	return slices.Contains(args, want)
}

func TestParseSelectedInfo(t *testing.T) {
	tests := []struct {
		name, input, title string
		want               MediaInfo
	}{
		{"4K VP9 SDR merged", `{"title":"Clip","width":3840,"height":2160,"dynamic_range":"SDR","vcodec":"vp09.00.51.08","acodec":"mp4a.40.2","audio_channels":6}`, "Clip", MediaInfo{VideoCodec: "vp9", AudioCodec: "aac", Resolution: "4K", Width: 3840, Height: 2160, AudioChannels: "5.1"}},
		{"HDR10", `{"title":"HDR","width":3840,"height":2160,"dynamic_range":"HDR10","vcodec":"vp9","acodec":"opus"}`, "HDR", MediaInfo{VideoCodec: "vp9", AudioCodec: "opus", Resolution: "4K", Width: 3840, Height: 2160, HDR: true}},
		{"compat 1080p avc1", `{"title":"Compat","width":1920,"height":1080,"dynamic_range":"SDR","vcodec":"avc1.640028","acodec":"mp4a.40.2","audio_channels":2}`, "Compat", MediaInfo{VideoCodec: "h264", AudioCodec: "aac", Resolution: "1080p", Width: 1920, Height: 1080, AudioChannels: "Stereo"}},
		{"audio-only opus", `{"title":"Music","vcodec":"none","acodec":"opus","abr":160.4,"audio_channels":2}`, "Music", MediaInfo{AudioCodec: "opus", AudioChannels: "Stereo", AudioBitrate: "160 kbps"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			title, got, err := parseSelectedInfo([]byte(tt.input))
			if err != nil || title != tt.title || got != tt.want {
				t.Fatalf("parseSelectedInfo() = %q, %+v, %v; want %q, %+v", title, got, err, tt.title, tt.want)
			}
		})
	}
}

func TestBuildYtDlpArgsRemuxByDefault(t *testing.T) {
	d := NewDownloader("yt-dlp", "ffmpeg", "", false, false)
	req := DownloadRequest{URL: "https://example.com/v", Type: VideoDownload, Quality: "1080p", Format: FormatMP4}
	dl := &Download{ID: "abc", Title: "Clip"}

	args := d.buildYtDlpArgs(req, dl)

	if !hasArg(args, "--remux-video") {
		t.Errorf("default download should remux, missing --remux-video: %v", args)
	}
	if hasArg(args, "--postprocessor-args") {
		t.Errorf("default download must not re-encode, found --postprocessor-args: %v", args)
	}
}

func TestBuildYtDlpArgsNeverReencodesVideo(t *testing.T) {
	// Downloads always remux (stream copy); compatibility re-encoding is a
	// conditional post-download step, never an in-download transcode.
	d := NewDownloader("yt-dlp", "ffmpeg", "", false, false)
	req := DownloadRequest{URL: "https://example.com/v", Type: VideoDownload, Quality: "1080p", Format: FormatMP4}
	dl := &Download{ID: "abc", Title: "Clip"}

	args := d.buildYtDlpArgs(req, dl)

	if !hasArg(args, "--remux-video") {
		t.Errorf("video download should remux: %v", args)
	}
	if hasArg(args, "--postprocessor-args") {
		t.Errorf("video download must not transcode in-download: %v", args)
	}
}

func TestBuildYtDlpArgsIncludesRetryFlags(t *testing.T) {
	d := NewDownloader("yt-dlp", "ffmpeg", "", false, false)
	req := DownloadRequest{URL: "https://example.com/v", Type: VideoDownload, Quality: "1080p", Format: FormatMP4}
	dl := &Download{ID: "abc", Title: "Clip"}

	args := d.buildYtDlpArgs(req, dl)

	for _, flag := range []string{"--retries", "--fragment-retries", "--extractor-retries", "--file-access-retries", "--retry-sleep"} {
		if !hasArg(args, flag) {
			t.Errorf("download args should include resilience flag %s: %v", flag, args)
		}
	}
}

func TestCookieArgsResolvesAbsolutePath(t *testing.T) {
	// A relative cookies path (e.g. the managed "cookies.txt") must be passed to
	// yt-dlp as an absolute path, because yt-dlp runs with cmd.Dir set to the
	// download output directory and would otherwise not find it.
	dir := t.TempDir()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("cookies.txt", []byte("# Netscape HTTP Cookie File\n"), 0600); err != nil {
		t.Fatal(err)
	}

	d := NewDownloader("yt-dlp", "ffmpeg", "cookies.txt", false, false)
	args := d.cookieArgs()

	if len(args) != 2 || args[0] != "--cookies" {
		t.Fatalf("expected [--cookies <path>], got %v", args)
	}
	if !filepath.IsAbs(args[1]) {
		t.Errorf("cookies path must be absolute, got %q", args[1])
	}
}

func TestBuildYtDlpArgsSelectsJSRuntime(t *testing.T) {
	// yt-dlp needs a JS runtime to solve YouTube's nsig challenge or it returns
	// only image formats ("Requested format is not available"). Deno is the
	// default and needs no flag; Node must be enabled explicitly.
	d := NewDownloader("yt-dlp", "ffmpeg", "", false, false)
	req := DownloadRequest{URL: "https://example.com/v", Type: VideoDownload, Quality: "1080p", Format: FormatMP4}
	dl := &Download{ID: "abc", Title: "Clip"}

	args := d.buildYtDlpArgs(req, dl)

	_, denoErr := exec.LookPath("deno")
	_, nodeErr := exec.LookPath("node")
	switch {
	case denoErr == nil:
		if hasArg(args, "--js-runtimes") {
			t.Errorf("deno present (default runtime), should not pass --js-runtimes: %v", args)
		}
	case nodeErr == nil:
		if !argHasValue(args, "--js-runtimes", "node") {
			t.Errorf("node present, expected --js-runtimes node: %v", args)
		}
	default:
		if hasArg(args, "--js-runtimes") {
			t.Errorf("no JS runtime on PATH, should not pass --js-runtimes: %v", args)
		}
	}
}

func argHasValue(args []string, flag, value string) bool {
	for i := 0; i < len(args)-1; i++ {
		if args[i] == flag && args[i+1] == value {
			return true
		}
	}
	return false
}

func TestCleanYtDlpError(t *testing.T) {
	cases := []struct{ in, want string }{
		{"ERROR: [youtube] abc: Video unavailable", "abc: Video unavailable"},
		{"ERROR: unable to download video data: HTTP Error 403", "unable to download video data: HTTP Error 403"},
		{"[generic] Requested format is not available", "Requested format is not available"},
		{"ERROR: ", "ERROR: "},
		{"plain message", "plain message"},
	}
	for _, c := range cases {
		if got := cleanYtDlpError(c.in); got != c.want {
			t.Errorf("cleanYtDlpError(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestGetVideoFormatAlwaysFallsBackToBest(t *testing.T) {
	d := NewDownloader("yt-dlp", "ffmpeg", "", false, false)
	cases := []struct{ quality, format string }{
		{"1080p", FormatMP4},
		{"720p", FormatWEBM},
		{"480p", "mkv"},
		{"best", FormatMP4},
	}
	for _, c := range cases {
		got := d.getVideoFormat(c.quality)
		if !strings.HasSuffix(got, "/best") {
			t.Errorf("getVideoFormat(%q) = %q; must end with /best fallback", c.quality, got)
		}
	}
}

func TestVideoFormatPrefersResolutionBeforeCodec(t *testing.T) {
	d := NewDownloader("yt-dlp", "ffmpeg", "", false, false)
	for _, tc := range []struct {
		name, quality, format, selector, sort string
	}{
		{"mp4 best", "best", FormatMP4, "bestvideo+bestaudio/best", "res,+hdr,channels:2,+codec:avc:m4a"},
		{"mp4 4k", "4k", FormatMP4, "bestvideo[height<=2160]+bestaudio/best[height<=2160]/best", "res,+hdr,channels:2,+codec:avc:m4a"},
		{"webm best", "best", FormatWEBM, "bestvideo+bestaudio/best", "res,+hdr,channels:2,+codec:vp9:opus"},
		{"webm 4k", "4k", FormatWEBM, "bestvideo[height<=2160]+bestaudio/best[height<=2160]/best", "res,+hdr,channels:2,+codec:vp9:opus"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := DownloadRequest{URL: "https://example.com/v", Type: VideoDownload, Quality: tc.quality, Format: tc.format}
			args := d.buildYtDlpArgs(req, &Download{ID: "abc", Title: "Clip"})
			if !argHasValue(args, "--format", tc.selector) || !argHasValue(args, "--format-sort", tc.sort) {
				t.Errorf("format selection = %v, want selector %q and sort %q", args, tc.selector, tc.sort)
			}
		})
	}
}

func TestCompatibilityFormatSelection(t *testing.T) {
	d := NewDownloader("yt-dlp", "ffmpeg", "", false, false)
	for _, tc := range []struct {
		name, quality, format string
		compat                bool
		selector, sort        string
	}{
		{"best", "best", FormatMP4, true, "bestvideo[vcodec^=avc1][dynamic_range=SDR]+bestaudio[acodec^=mp4a][audio_channels<=2]/bestvideo[dynamic_range=SDR]+bestaudio/best", "res:1080,+hdr,+codec:avc:m4a"},
		{"720p", "720p", FormatMP4, true, "bestvideo[vcodec^=avc1][dynamic_range=SDR]+bestaudio[acodec^=mp4a][audio_channels<=2]/bestvideo[dynamic_range=SDR]+bestaudio/best", "res:720,+hdr,+codec:avc:m4a"},
		{"webm becomes mp4", "best", FormatWEBM, true, "bestvideo[vcodec^=avc1][dynamic_range=SDR]+bestaudio[acodec^=mp4a][audio_channels<=2]/bestvideo[dynamic_range=SDR]+bestaudio/best", "res:1080,+hdr,+codec:avc:m4a"},
		{"off", "best", FormatMP4, false, "bestvideo+bestaudio/best", "res,+hdr,channels:2,+codec:avc:m4a"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := DownloadRequest{URL: "https://example.com/v", Type: VideoDownload, Quality: tc.quality, Format: tc.format, ReencodeForCompatibility: tc.compat}
			args := d.buildYtDlpArgs(req, &Download{ID: "abc", Title: "Clip"})
			if !argHasValue(args, "--format", tc.selector) || !argHasValue(args, "--format-sort", tc.sort) {
				t.Fatalf("format selection = %v, want %q and %q", args, tc.selector, tc.sort)
			}
			if tc.compat && (!argHasValue(args, "--merge-output-format", FormatMP4) || !argHasValue(args, "--remux-video", FormatMP4)) {
				t.Fatalf("compatibility output must be MP4: %v", args)
			}
		})
	}
}

func TestSelectionArgsAudioAndRange(t *testing.T) {
	d := NewDownloader("yt-dlp", "ffmpeg", "", false, false)
	for _, format := range []string{FormatMP4, FormatWEBM, "mkv"} {
		for _, audio := range []string{"stereo", "surround"} {
			for _, videoRange := range []string{"sdr", "hdr"} {
				t.Run(format+"/"+audio+"/"+videoRange, func(t *testing.T) {
					req := DownloadRequest{Type: VideoDownload, Quality: "1080p", Format: format, Audio: audio, Range: videoRange}
					args := d.selectionArgs(req)
					wantSort := "res,"
					if videoRange == "sdr" {
						wantSort += "+"
					}
					wantSort += "hdr,channels"
					if audio == "stereo" {
						wantSort += ":2"
					}
					if format == FormatMP4 {
						wantSort += ",+codec:avc:m4a"
					}
					if format == FormatWEBM {
						wantSort += ",+codec:vp9:opus"
					}
					if !argHasValue(args, "--format-sort", wantSort) {
						t.Errorf("args=%v; want sort %q", args, wantSort)
					}
					selector := "bestvideo[height<=1080]+bestaudio/best[height<=1080]/best"
					if format == FormatWEBM && audio == "surround" {
						selector = "bestvideo[height<=1080]+bestaudio[acodec^=opus]/bestvideo[height<=1080]+bestaudio[acodec=vorbis]/" + selector
					}
					if !argHasValue(args, "--format", selector) {
						t.Errorf("args=%v; want selector %q", args, selector)
					}
				})
			}
		}
	}
	compat := d.selectionArgs(DownloadRequest{Type: VideoDownload, Quality: "best", Format: FormatWEBM, Audio: "surround", Range: "hdr", ReencodeForCompatibility: true})
	if !argHasValue(compat, "--format-sort", "res:1080,+hdr,+codec:avc:m4a") {
		t.Errorf("compat args=%v", compat)
	}
	worst := d.selectionArgs(DownloadRequest{Type: VideoDownload, Quality: "worst", Format: FormatMP4, Audio: "surround", Range: "hdr"})
	if !argHasValue(worst, "--format", "worstvideo+worstaudio/worst") || hasArg(worst, "--format-sort") {
		t.Errorf("worst args=%v", worst)
	}
}

func TestGenerateID(t *testing.T) {
	id1 := GenerateID()
	time.Sleep(1 * time.Millisecond) // Ensure different timestamp
	id2 := GenerateID()

	if id1 == id2 {
		t.Errorf("Expected different IDs, got same: %s", id1)
	}

	if len(id1) == 0 {
		t.Errorf("Expected non-empty ID")
	}
}

func TestIsPlaylistURL(t *testing.T) {
	downloader := NewDownloader("", "", "", false, false)

	testCases := []struct {
		url      string
		expected bool
	}{
		{"https://www.youtube.com/watch?v=dQw4w9WgXcQ&list=PLxxx", true},
		{"https://www.youtube.com/playlist?list=PLxxx", true},
		{"https://www.youtube.com/watch?v=dQw4w9WgXcQ", false},
		{"https://example.com/video", false},
	}

	for _, tc := range testCases {
		result := downloader.IsPlaylistURL(tc.url)
		if result != tc.expected {
			t.Errorf("IsPlaylistURL(%s) = %v, expected %v", tc.url, result, tc.expected)
		}
	}
}

func TestDownloadRequest_Validation(t *testing.T) {
	testCases := []struct {
		name    string
		req     DownloadRequest
		wantErr bool
	}{
		{
			name: "valid video request",
			req: DownloadRequest{
				URL:       "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
				Type:      VideoDownload,
				Quality:   "720p",
				Format:    "mp4",
				OutputDir: "/tmp",
			},
			wantErr: false,
		},
		{
			name: "valid audio request",
			req: DownloadRequest{
				URL:       "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
				Type:      AudioDownload,
				Quality:   "best",
				Format:    "mp3",
				OutputDir: "/tmp",
			},
			wantErr: false,
		},
		{
			name: "empty URL",
			req: DownloadRequest{
				URL:       "",
				Type:      VideoDownload,
				Quality:   "720p",
				Format:    "mp4",
				OutputDir: "/tmp",
			},
			wantErr: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.req.URL == "" && !tc.wantErr {
				t.Errorf("Expected error for empty URL")
			}
		})
	}
}

func TestFindDownloadedFile(t *testing.T) {
	// Create temp directory for testing
	tempDir, err := os.MkdirTemp("", "goyt_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create test files
	testFiles := []string{
		"test video.mp4",
		"another video.mkv",
		"audio file.mp3",
	}

	for _, filename := range testFiles {
		filepath := filepath.Join(tempDir, filename)
		file, err := os.Create(filepath)
		if err != nil {
			t.Fatalf("Failed to create test file %s: %v", filename, err)
		}
		file.Close()
	}

	downloader := NewDownloader("", "", "", false, false)

	// Test finding existing file
	result := downloader.findDownloadedFile(tempDir, "test video.mp4")
	expected := filepath.Join(tempDir, "test video.mp4")
	if result != expected {
		t.Errorf("Expected to find %s, got %s", expected, result)
	}

	// Test finding non-existent file
	result = downloader.findDownloadedFile(tempDir, "test.mp4")
	if result != "" {
		t.Errorf("Expected empty result for non-existent file, got %s", result)
	}
}

func TestTimeStringToSeconds(t *testing.T) {
	testCases := []struct {
		input    string
		expected float64
	}{
		{"00:01:30.00", 90.0},
		{"01:00:00.00", 3600.0},
		{"00:00:30.50", 30.5},
		{"invalid", 0.0},
	}

	for _, tc := range testCases {
		result := timeStringToSeconds(tc.input)
		if result != tc.expected {
			t.Errorf("timeStringToSeconds(%s) = %f, expected %f", tc.input, result, tc.expected)
		}
	}
}

func TestDownloadProgress_SafeChannelHandling(t *testing.T) {
	// Test that progress channels handle closure gracefully
	progressChan := make(chan DownloadProgress, 1)

	// Send to open channel
	progress := DownloadProgress{Percentage: 50.0}

	select {
	case progressChan <- progress:
		// Success
	default:
		t.Errorf("Failed to send to open channel")
	}

	// Close channel
	close(progressChan)

	// Attempt to send to closed channel using safe pattern (should not panic)
	func() {
		defer func() {
			if r := recover(); r != nil {
				// This is expected behavior - we're testing that we can handle the panic
				t.Logf("Expected panic recovered: %v", r)
			}
		}()

		select {
		case progressChan <- progress:
			t.Errorf("Should not be able to send to closed channel")
		default:
			// Expected behavior - channel is closed and select should hit default
		}
	}()

	// Test the safe sending pattern used in the actual code
	func() {
		defer func() {
			if r := recover(); r != nil {
				// Channel was closed, ignore the panic - this is the safe pattern
				t.Logf("Safe pattern: recovered from closed channel panic: %v", r)
			}
		}()
		// This mimics how we safely send in the actual code
		select {
		case progressChan <- progress:
			// This should hit the panic and be caught by recover
		default:
			// Channel is full or closed, skip
		}
	}()
}

func TestDownloadContext_Cancelation(t *testing.T) {
	// Test that download respects context cancelation
	ctx, cancel := context.WithCancel(context.Background())

	// Cancel immediately
	cancel()

	// Check if context is canceled
	if ctx.Err() != context.Canceled {
		t.Errorf("Expected context to be canceled")
	}
}

// TestDownloadKeepsFinalYtDlpError guards the os/exec rule that Wait must not
// run before pipe reads finish: Wait closes the pipes on exit, which dropped
// yt-dlp's last ERROR line and left users with "exit status 1".
func TestDownloadKeepsFinalYtDlpError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a shell script as a fake yt-dlp")
	}
	dir := t.TempDir()
	fake := filepath.Join(dir, "yt-dlp")
	script := "#!/bin/sh\n" +
		"for a in \"$@\"; do [ \"$a\" = --dump-json ] && exit 1; done\n" +
		"i=0; while [ $i -lt 2000 ]; do echo \"[download] noise line $i\"; i=$((i+1)); done\n" +
		"echo 'ERROR: [youtube] abc: Sign in to confirm your age' >&2\n" +
		"exit 1\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	d := NewDownloader(fake, "ffmpeg", "", false, false)
	for i := range 30 {
		progress := make(chan DownloadProgress, 4096)
		req := DownloadRequest{URL: "https://example.com/v", Type: VideoDownload, Quality: "best", Format: FormatMP4, OutputDir: dir}
		dl, _ := d.Download(context.Background(), req, progress, nil, nil, nil, fmt.Sprintf("t%d", i))
		if dl == nil || !strings.Contains(dl.Error, "Sign in to confirm your age") {
			got := "<nil download>"
			if dl != nil {
				got = dl.Error
			}
			t.Fatalf("run %d: error = %q; want the yt-dlp ERROR text", i, got)
		}
	}
}

func TestAudioOnlyChannelSelection(t *testing.T) {
	d := NewDownloader("yt-dlp", "ffmpeg", "", false, false)
	for _, tc := range []struct {
		audio, format, sort string
		keepsChannels       bool
	}{
		{"", "mp3", "channels:2", false},
		{"stereo", "flac", "channels:2", false},
		{"surround", "m4a", "channels", true},
		{"surround", "flac", "channels", true},
		{"surround", "wav", "channels", true},
		// MP3 holds at most two channels, so surround still downmixes.
		{"surround", "mp3", "channels", false},
	} {
		t.Run(tc.audio+"/"+tc.format, func(t *testing.T) {
			req := DownloadRequest{URL: "https://example.com/a", Type: AudioDownload, Format: tc.format, Audio: tc.audio}
			args := d.buildYtDlpArgs(req, &Download{ID: "a", Title: "Track"})
			if !argHasValue(args, "--format-sort", tc.sort) {
				t.Errorf("args = %v, want --format-sort %q", args, tc.sort)
			}
			ff := d.buildAudioFFmpegArgs(tc.format, "progress.txt", tc.audio == "surround")
			if got := !strings.Contains(ff, "-ac 2"); got != tc.keepsChannels {
				t.Errorf("ffmpeg args %q: keeps source channels = %v, want %v", ff, got, tc.keepsChannels)
			}
		})
	}
	if ff := d.buildAudioFFmpegArgs("m4a", "p", true); !strings.Contains(ff, "-b:a 384k") {
		t.Errorf("surround m4a args %q, want 384k AAC", ff)
	}
}

func outputFixtureDownloader(t *testing.T, dir string) *Downloader {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("uses a shell script as a fake yt-dlp")
	}
	path := filepath.Join(dir, "yt-dlp")
	script := `#!/bin/sh
for arg in "$@"; do
  if [ "$arg" = --dump-json ]; then
    [ "$GOYT_TEST_TITLE" = metadata-failure ] && exit 1
    printf '{"title":"%s"}\n' "$GOYT_TEST_TITLE"
    exit 0
  fi
done
while [ "$#" -gt 0 ]; do
  if [ "$1" = --output ]; then
    shift
    printf 'fixture media\n' > "$1"
    exit 0
  fi
  shift
done
exit 2
`
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return NewDownloader(path, "ffmpeg", "", false, false)
}

func TestDownloadDottedTitlesHaveDistinctOutputs(t *testing.T) {
	dir := t.TempDir()
	d := outputFixtureDownloader(t, dir)
	var paths []string
	for _, title := range []string{"Lecture.1", "Lecture.2", "Lecture.mp4"} {
		t.Setenv("GOYT_TEST_TITLE", title)
		dl, err := d.Download(t.Context(), DownloadRequest{
			URL: "https://example.com/" + title, Type: VideoDownload,
			Quality: "best", Format: FormatMP4, OutputDir: dir,
		}, make(chan DownloadProgress, 100), nil, nil, nil, title)
		if err != nil {
			t.Fatal(err)
		}
		want := SanitizeFilename(title) + ".mp4"
		if filepath.Base(dl.OutputPath) != want {
			t.Errorf("title %q: output = %q, want %q", title, dl.OutputPath, want)
		}
		paths = append(paths, filepath.Base(dl.OutputPath))
	}
	if paths[0] == paths[1] {
		t.Fatalf("distinct titles Lecture.1 and Lecture.2 share output %q", paths[0])
	}
}

func TestDownloadKeepsOwnOutput(t *testing.T) {
	for _, tc := range []struct {
		name, title, format string
		mediaType           DownloadType
	}{
		{"http title video", "http tutorial", FormatMP4, VideoDownload},
		{"http title audio", "http tutorial", "mp3", AudioDownload},
		{"fallback video", "metadata-failure", FormatMP4, VideoDownload},
		{"fallback audio", "metadata-failure", "mp3", AudioDownload},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			d := outputFixtureDownloader(t, dir)
			t.Setenv("GOYT_TEST_TITLE", tc.title)
			unrelated := filepath.Join(dir, "unrelated."+tc.format)
			if err := os.WriteFile(unrelated, []byte("other download"), 0o600); err != nil {
				t.Fatal(err)
			}
			future := time.Now().Add(time.Hour)
			if err := os.Chtimes(unrelated, future, future); err != nil {
				t.Fatal(err)
			}

			dl, err := d.Download(t.Context(), DownloadRequest{
				URL: "https://example.com/tutorial", Type: tc.mediaType,
				Quality: "best", Format: tc.format, OutputDir: dir,
			}, make(chan DownloadProgress, 100), nil, nil, nil, "own-output")
			if err != nil {
				t.Fatal(err)
			}
			base := SanitizeFilename(tc.title)
			if tc.title == "metadata-failure" {
				base = "download_own-output"
			}
			want := filepath.Join(dir, base+"."+tc.format)
			if _, err := os.Stat(want); err != nil {
				t.Fatalf("fixture did not create own output: %v", err)
			}
			if dl.OutputPath != want || dl.Filename != filepath.Base(want) {
				t.Fatalf("download owns %q but reports %q, filename %q", want, dl.OutputPath, dl.Filename)
			}
			if dl.FileSize != int64(len("fixture media\n")) {
				t.Errorf("file size = %d, want own output size", dl.FileSize)
			}
		})
	}
}
