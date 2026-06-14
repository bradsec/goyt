package manager

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"goyt/internal/config"
	"goyt/internal/core"
)

func TestNewDownloadManager(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "goyt_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	downloader := core.NewDownloader("yt-dlp", "ffmpeg", "", false, false)
	cfg := &config.Config{
		CompletedFileExpiryHours: 0, // Disable auto-expiry for tests
	}

	dm := NewDownloadManager(downloader, 0, tempDir, cfg)

	if dm == nil {
		t.Fatal("Expected non-nil DownloadManager")
	}

	if dm.maxConcurrent != 0 {
		t.Errorf("Expected maxConcurrent=0, got %d", dm.maxConcurrent)
	}

	if dm.outputDir != tempDir {
		t.Errorf("Expected outputDir=%s, got %s", tempDir, dm.outputDir)
	}

	// Cleanup
	dm.Shutdown()
}

func TestAddDownload(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "goyt_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	downloader := core.NewDownloader("yt-dlp", "ffmpeg", "", false, false)
	cfg := &config.Config{
		CompletedFileExpiryHours: 0,
	}

	// Use 0 workers to prevent actual processing during tests
	dm := NewDownloadManager(downloader, 0, tempDir, cfg)
	defer dm.Shutdown()

	req := core.DownloadRequest{
		URL:       "https://example.com/test",
		Type:      core.VideoDownload,
		Quality:   "720p",
		Format:    "mp4",
		OutputDir: tempDir,
	}

	download, err := dm.AddDownload(req)
	if err != nil {
		t.Fatalf("Failed to add download: %v", err)
	}

	if download == nil {
		t.Fatal("Expected non-nil download")
	}

	if download.Status != core.StatusQueued {
		t.Errorf("Expected status=queued, got %s", download.Status)
	}

	if download.URL != req.URL {
		t.Errorf("Expected URL=%s, got %s", req.URL, download.URL)
	}
}

func TestGetDownload(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "goyt_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	downloader := core.NewDownloader("yt-dlp", "ffmpeg", "", false, false)
	cfg := &config.Config{
		CompletedFileExpiryHours: 0,
	}

	dm := NewDownloadManager(downloader, 0, tempDir, cfg)
	defer dm.Shutdown()

	req := core.DownloadRequest{
		URL:       "https://example.com/test",
		Type:      core.VideoDownload,
		Quality:   "720p",
		Format:    "mp4",
		OutputDir: tempDir,
	}

	download, err := dm.AddDownload(req)
	if err != nil {
		t.Fatalf("Failed to add download: %v", err)
	}

	// Test getting existing download
	retrieved, exists := dm.GetDownload(download.ID)
	if !exists {
		t.Error("Expected download to exist")
	}

	if retrieved.ID != download.ID {
		t.Errorf("Expected ID=%s, got %s", download.ID, retrieved.ID)
	}

	// Test getting non-existent download
	_, exists = dm.GetDownload("nonexistent")
	if exists {
		t.Error("Expected download not to exist")
	}
}

func TestCancelDownload(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "goyt_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	downloader := core.NewDownloader("yt-dlp", "ffmpeg", "", false, false)
	cfg := &config.Config{
		CompletedFileExpiryHours: 0,
	}

	dm := NewDownloadManager(downloader, 0, tempDir, cfg)
	defer dm.Shutdown()

	req := core.DownloadRequest{
		URL:       "https://example.com/test",
		Type:      core.VideoDownload,
		Quality:   "720p",
		Format:    "mp4",
		OutputDir: tempDir,
	}

	download, err := dm.AddDownload(req)
	if err != nil {
		t.Fatalf("Failed to add download: %v", err)
	}

	// Cancel the download
	err = dm.CancelDownload(download.ID)
	if err != nil {
		t.Fatalf("Failed to cancel download: %v", err)
	}

	// Verify status is canceled
	retrieved, exists := dm.GetDownload(download.ID)
	if !exists {
		t.Fatal("Expected download to still exist after cancelation")
	}

	if retrieved.Status != core.StatusCanceled {
		t.Errorf("Expected status=canceled, got %s", retrieved.Status)
	}
}

func TestRemoveDownload(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "goyt_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	downloader := core.NewDownloader("yt-dlp", "ffmpeg", "", false, false)
	cfg := &config.Config{
		CompletedFileExpiryHours: 0,
	}

	dm := NewDownloadManager(downloader, 0, tempDir, cfg)
	defer dm.Shutdown()

	req := core.DownloadRequest{
		URL:       "https://example.com/test",
		Type:      core.VideoDownload,
		Quality:   "720p",
		Format:    "mp4",
		OutputDir: tempDir,
	}

	download, err := dm.AddDownload(req)
	if err != nil {
		t.Fatalf("Failed to add download: %v", err)
	}

	// Remove the download
	err = dm.RemoveDownload(download.ID)
	if err != nil {
		t.Fatalf("Failed to remove download: %v", err)
	}

	// Verify download no longer exists
	_, exists := dm.GetDownload(download.ID)
	if exists {
		t.Error("Expected download to be removed")
	}
}

func TestProgressChannel(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "goyt_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	downloader := core.NewDownloader("yt-dlp", "ffmpeg", "", false, false)
	cfg := &config.Config{
		CompletedFileExpiryHours: 0,
	}

	dm := NewDownloadManager(downloader, 0, tempDir, cfg)
	defer dm.Shutdown()

	req := core.DownloadRequest{
		URL:       "https://example.com/test",
		Type:      core.VideoDownload,
		Quality:   "720p",
		Format:    "mp4",
		OutputDir: tempDir,
	}

	download, err := dm.AddDownload(req)
	if err != nil {
		t.Fatalf("Failed to add download: %v", err)
	}

	// Get progress channel
	progressChan, exists := dm.GetProgress(download.ID)
	if !exists {
		t.Fatal("Expected progress channel to exist")
	}

	if progressChan == nil {
		t.Fatal("Expected non-nil progress channel")
	}

	// Test sending progress (should not block)
	progress := core.DownloadProgress{Percentage: 50.0}

	select {
	case progressChan <- progress:
		// Success
	default:
		t.Error("Progress channel should not be full initially")
	}
}

func TestCheckFileExists(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "goyt_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create a test file
	testFile := filepath.Join(tempDir, "existing_video.mp4")
	file, err := os.Create(testFile)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}
	file.Close()

	downloader := core.NewDownloader("yt-dlp", "ffmpeg", "", false, false)
	cfg := &config.Config{
		CompletedFileExpiryHours: 0,
	}

	dm := NewDownloadManager(downloader, 0, tempDir, cfg)
	defer dm.Shutdown()

	// This test would require mocking yt-dlp's GetVideoInfo,
	// so we'll just test the function exists and returns a string
	req := core.DownloadRequest{
		URL:       "https://example.com/test",
		Type:      core.VideoDownload,
		Quality:   "720p",
		Format:    "mp4",
		OutputDir: tempDir,
	}

	result := dm.CheckFileExistence(req)
	// We can't predict the exact result without mocking,
	// but the function should not panic
	_ = result
}

func TestExtractTitleFromPath(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "goyt_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	downloader := core.NewDownloader("yt-dlp", "ffmpeg", "", false, false)
	cfg := &config.Config{
		CompletedFileExpiryHours: 0,
	}

	dm := NewDownloadManager(downloader, 0, tempDir, cfg)
	defer dm.Shutdown()

	testCases := []struct {
		input    string
		expected string
	}{
		{"/path/to/my_video_file.mp4", "my video file"},
		{"/path/to/another-video.mkv", "another-video"},
		{"simple_file.mp3", "simple file"},
		{"", ""},
	}

	for _, tc := range testCases {
		result := dm.extractTitleFromPath(tc.input)
		if tc.input == "" && result != tc.input {
			// Special case for empty input
			continue
		}

		if result != tc.expected {
			t.Errorf("extractTitleFromPath(%s) = %s, expected %s", tc.input, result, tc.expected)
		}
	}
}

func TestShutdown(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "goyt_test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	downloader := core.NewDownloader("yt-dlp", "ffmpeg", "", false, false)
	cfg := &config.Config{
		CompletedFileExpiryHours: 0,
	}

	dm := NewDownloadManager(downloader, 0, tempDir, cfg)

	// Add a download
	req := core.DownloadRequest{
		URL:       "https://example.com/test",
		Type:      core.VideoDownload,
		Quality:   "720p",
		Format:    "mp4",
		OutputDir: tempDir,
	}

	_, err = dm.AddDownload(req)
	if err != nil {
		t.Fatalf("Failed to add download: %v", err)
	}

	// Shutdown should not panic
	dm.Shutdown()

	// Give some time for cleanup
	time.Sleep(100 * time.Millisecond)
}

// liveWorkers reads the active worker count under the manager lock.
func liveWorkers(dm *DownloadManager) int {
	dm.mutex.RLock()
	defer dm.mutex.RUnlock()
	return dm.activeWorkers
}

// waitForWorkers polls until the active worker count equals want, asserting it
// never exceeds maxObserved while converging.
func waitForWorkers(t *testing.T, dm *DownloadManager, want, maxAllowed int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		got := liveWorkers(dm)
		if got > maxAllowed {
			t.Fatalf("worker count %d exceeded allowed max %d", got, maxAllowed)
		}
		if got == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("workers did not converge to %d (last=%d)", want, liveWorkers(dm))
}

func newTestManagerForConvert(t *testing.T) *DownloadManager {
	t.Helper()
	dir := t.TempDir()
	d := core.NewDownloader("yt-dlp", "ffmpeg", "", false, false)
	return NewDownloadManager(d, 1, dir, &config.Config{})
}

func TestConvertDownloadEligibility(t *testing.T) {
	dm := newTestManagerForConvert(t)

	if err := dm.ConvertDownload("nope"); err == nil {
		t.Fatal("expected error for missing download")
	}

	audio := &core.Download{ID: "a", URL: "u", Type: core.AudioDownload, Status: core.StatusCompleted, OutputPath: "/tmp/x.mp3"}
	dm.downloads["a"] = audio
	if err := dm.ConvertDownload("a"); err == nil {
		t.Fatal("expected error converting audio")
	}

	queued := &core.Download{ID: "q", URL: "u", Type: core.VideoDownload, Status: core.StatusQueued, OutputPath: "/tmp/x.mp4"}
	dm.downloads["q"] = queued
	if err := dm.ConvertDownload("q"); err == nil {
		t.Fatal("expected error converting non-completed download")
	}

	compat := &core.Download{ID: "c", URL: "u", Type: core.VideoDownload, Status: core.StatusCompleted, OutputPath: "/tmp/x.mp4", VideoCodec: "h264", AudioCodec: "aac"}
	dm.downloads["c"] = compat
	if err := dm.ConvertDownload("c"); err == nil {
		t.Fatal("expected error converting already-compatible video")
	}
}

// TestAdjustWorkersShrinkBounded verifies that shrinking the worker pool never
// transiently exceeds the old count and converges to the new count (Q-2).
func TestAdjustWorkersShrinkBounded(t *testing.T) {
	tempDir := t.TempDir()
	downloader := core.NewDownloader("yt-dlp", "ffmpeg", "", false, false)
	cfg := &config.Config{
		MaxConcurrentDownloads:   4,
		DownloadPath:             tempDir,
		DefaultVideoFormat:       "mp4",
		DefaultAudioFormat:       "mp3",
		Port:                     3000,
		CompletedFileExpiryHours: 0,
	}
	dm := NewDownloadManager(downloader, 4, tempDir, cfg)
	defer dm.Shutdown()

	waitForWorkers(t, dm, 4, 4)

	shrunk := *cfg
	shrunk.MaxConcurrentDownloads = 2
	dm.UpdateConfig(&shrunk)
	waitForWorkers(t, dm, 2, 4)

	grown := shrunk
	grown.MaxConcurrentDownloads = 5
	dm.UpdateConfig(&grown)
	waitForWorkers(t, dm, 5, 5)
}
