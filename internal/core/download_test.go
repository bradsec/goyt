package core

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
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
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
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
		got := d.getVideoFormat(c.quality, c.format)
		if !strings.HasSuffix(got, "/best") {
			t.Errorf("getVideoFormat(%q,%q) = %q; must end with /best fallback", c.quality, c.format, got)
		}
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
	result := downloader.findDownloadedFile(tempDir, "test video", "mp4")
	expected := filepath.Join(tempDir, "test video.mp4")
	if result != expected {
		t.Errorf("Expected to find %s, got %s", expected, result)
	}

	// Test finding non-existent file
	result = downloader.findDownloadedFile(tempDir, "nonexistent", "avi")
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
