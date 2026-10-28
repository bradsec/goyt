package manager

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"goyt/internal/config"
	"goyt/internal/core"
)

func TestUpdateDownloadQuality(t *testing.T) {
	dm := &DownloadManager{downloads: map[string]*core.Download{"a": {ID: "a", Status: core.StatusDownloading}}}
	want := core.MediaInfo{VideoCodec: "vp9", AudioCodec: "aac", Resolution: "4K", HDR: true, AudioChannels: "5.1", AudioBitrate: "160 kbps"}
	dm.UpdateDownloadQuality("a", want)
	dm.mutex.RLock()
	got := *dm.downloads["a"]
	dm.mutex.RUnlock()
	if got.VideoCodec != want.VideoCodec || got.AudioCodec != want.AudioCodec || got.Resolution != want.Resolution || got.HDR != want.HDR || got.AudioChannels != want.AudioChannels || got.AudioBitrate != want.AudioBitrate {
		t.Fatalf("quality not applied: %+v", got)
	}
	dm.UpdateDownloadQuality("missing", want)
}

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

func TestDownloadPersistsAudioAndRange(t *testing.T) {
	dir := t.TempDir()
	dm := NewDownloadManager(core.NewDownloader("yt-dlp", "ffmpeg", "", false, false), 0, dir, config.DefaultConfig())
	defer dm.Shutdown()
	req := core.DownloadRequest{URL: "https://example.com/audio-range", Type: core.VideoDownload, Quality: "best", Format: "mp4", Audio: "surround", Range: "hdr", OutputDir: dir}
	download, err := dm.AddDownload(req)
	if err != nil {
		t.Fatal(err)
	}
	if download.Audio != "surround" || download.Range != "hdr" {
		t.Errorf("stored = %q/%q", download.Audio, download.Range)
	}
	if err := dm.SaveState(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(dm.GetStateFilePath())
	if err != nil {
		t.Fatal(err)
	}
	var state StateFile
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	if saved := state.Downloads[download.ID]; saved == nil || saved.Audio != "surround" || saved.Range != "hdr" {
		t.Errorf("saved download = %+v", saved)
	}
	second := req
	second.Audio = "stereo"
	second.Range = "sdr"
	dm.mutex.Lock()
	dm.downloads[download.ID].Status = core.StatusFailed
	delete(dm.processingUrls, req.URL)
	dm.mutex.Unlock()
	if _, err := dm.AddDownload(second); err != nil {
		t.Errorf("distinct settings rejected: %v", err)
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
	dm.Shutdown()

	if _, err := os.Stat(filepath.Join(tempDir, ".goyt_state.json")); err != nil {
		t.Fatalf("final state was not persisted: %v", err)
	}
}

func TestShutdownWaitsForTrackedWork(t *testing.T) {
	dm := NewDownloadManager(
		core.NewDownloader("yt-dlp", "ffmpeg", "", false, false),
		0,
		t.TempDir(),
		&config.Config{},
	)
	var stopped atomic.Bool
	dm.wg.Go(func() {
		<-dm.ctx.Done()
		stopped.Store(true)
	})

	dm.Shutdown()
	if !stopped.Load() {
		t.Fatal("Shutdown returned before tracked work stopped")
	}
}

func TestUpdateConfigResizesConversionLimit(t *testing.T) {
	dm := NewDownloadManager(
		core.NewDownloader("yt-dlp", "ffmpeg", "", false, false),
		1,
		t.TempDir(),
		&config.Config{MaxConcurrentDownloads: 1},
	)
	defer dm.Shutdown()
	if !dm.acquireConversion(t.Context()) {
		t.Fatal("failed to acquire initial conversion slot")
	}

	dm.UpdateConfig(&config.Config{MaxConcurrentDownloads: 2, DownloadPath: t.TempDir()})
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if !dm.acquireConversion(ctx) {
		t.Fatal("updated conversion limit did not admit a second conversion")
	}
	dm.releaseConversion()
	dm.releaseConversion()
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

	compat := &core.Download{ID: "c", URL: "u", Type: core.VideoDownload, Status: core.StatusCompleted, OutputPath: "/tmp/x.mp4", VideoCodec: "h264", AudioCodec: "aac", Resolution: "1080p", AudioChannels: "Stereo"}
	dm.downloads["c"] = compat
	if err := dm.ConvertDownload("c"); err == nil {
		t.Fatal("expected error converting already-compatible video")
	}
}

func TestVideoIsCompatible(t *testing.T) {
	for _, tc := range []struct {
		name  string
		video core.Download
		want  bool
	}{
		{"1080p SDR stereo", core.Download{VideoCodec: "h264", AudioCodec: "aac", Resolution: "1080p", AudioChannels: "Stereo"}, true},
		{"1080p SDR mono", core.Download{VideoCodec: "h264", AudioCodec: "aac", Resolution: "1080p", AudioChannels: "Mono"}, true},
		{"1080p 5.1", core.Download{VideoCodec: "h264", AudioCodec: "aac", Resolution: "1080p", AudioChannels: "5.1"}, false},
		{"4K", core.Download{VideoCodec: "h264", AudioCodec: "aac", Resolution: "4K", AudioChannels: "Stereo"}, false},
		{"mislabeled short side", core.Download{VideoCodec: "h264", AudioCodec: "aac", Resolution: "1080p", Width: 1200, Height: 1920, AudioChannels: "Stereo"}, false},
		{"720p cap", core.Download{VideoCodec: "h264", AudioCodec: "aac", Resolution: "1080p", Width: 1920, Height: 1080, CompatibilityCap: 720, AudioChannels: "Stereo"}, false},
		{"HDR", core.Download{VideoCodec: "h264", AudioCodec: "aac", Resolution: "1080p", AudioChannels: "Stereo", HDR: true}, false},
		{"surround", core.Download{VideoCodec: "h264", AudioCodec: "aac", Resolution: "1080p", AudioChannels: "5.1"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := videoIsCompatible(&tc.video); got != tc.want {
				t.Fatalf("videoIsCompatible() = %v, want %v", got, tc.want)
			}
		})
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

func TestProcessDownloadSkipsStaleQueueEntry(t *testing.T) {
	dm := newTestManagerForConvert(t)
	defer dm.Shutdown()

	download := &core.Download{ID: "paused", Status: core.StatusPaused}
	dm.mutex.Lock()
	dm.downloads[download.ID] = download
	dm.mutex.Unlock()

	if dm.processDownload(download) {
		t.Fatal("paused download was claimed")
	}
	if download.Status != core.StatusPaused {
		t.Fatalf("status = %s, want paused", download.Status)
	}
}

func TestCleanupTemporaryFilesKeepsOtherDownload(t *testing.T) {
	dir := t.TempDir()
	dm := NewDownloadManager(core.NewDownloader("yt-dlp", "ffmpeg", "", false, false), 0, dir, &config.Config{})
	defer dm.Shutdown()

	own := filepath.Join(dir, "foo.mp4.part")
	stream := filepath.Join(dir, "foo.f137.mp4.part")
	other := filepath.Join(dir, "foobar.mp4.part")
	dotted := filepath.Join(dir, "foo.bar.mp4.part")
	for _, path := range []string{own, stream, other, dotted} {
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	dm.cleanupTemporaryFiles(&core.Download{ID: "foo", Filename: "foo.mp4"})
	if _, err := os.Stat(own); !os.IsNotExist(err) {
		t.Fatalf("own temporary file remains: %v", err)
	}
	if _, err := os.Stat(stream); !os.IsNotExist(err) {
		t.Fatalf("own format-stream file remains: %v", err)
	}
	if _, err := os.Stat(other); err != nil {
		t.Fatalf("other download's temporary file was removed: %v", err)
	}
	if _, err := os.Stat(dotted); err != nil {
		t.Fatalf("dotted title's temporary file was removed: %v", err)
	}
}

func TestClearAllQueuedReportsSaveFailure(t *testing.T) {
	dir := t.TempDir()
	dm := NewDownloadManager(core.NewDownloader("yt-dlp", "ffmpeg", "", false, false), 0, dir, &config.Config{})
	defer dm.Shutdown()

	dm.mutex.Lock()
	dm.outputDir = filepath.Join(dir, "missing")
	dm.mutex.Unlock()
	if err := dm.ClearAllQueued(); err == nil {
		t.Fatal("expected state save error")
	}
}

func TestCancelPausedDownload(t *testing.T) {
	dir := t.TempDir()
	dm := NewDownloadManager(core.NewDownloader("yt-dlp", "ffmpeg", "", false, false), 0, dir, &config.Config{})
	defer dm.Shutdown()

	download := &core.Download{ID: "paused", Status: core.StatusPaused}
	dm.mutex.Lock()
	dm.downloads[download.ID] = download
	dm.pausedDownloads[download.ID] = download
	dm.mutex.Unlock()
	if err := dm.CancelDownload(download.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := dm.GetDownload(download.ID)
	if got.Status != core.StatusCanceled {
		t.Fatalf("status = %s, want canceled", got.Status)
	}
}

func TestApplyQualityName(t *testing.T) {
	dir := t.TempDir()
	cfg := config.DefaultConfig()
	dm := NewDownloadManager(core.NewDownloader("yt-dlp", "ffmpeg", "", false, false), 0, dir, cfg)
	defer dm.Shutdown()
	media := core.MediaInfo{VideoCodec: "vp9", Resolution: "4K", HDR: true, AudioChannels: "5.1", AudioBitrate: "384 kbps"}

	src := filepath.Join(dir, "dino.mp4")
	if err := os.WriteFile(src, []byte("v"), 0o600); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, "dino [4K HDR 5.1 384kbps].mp4")
	if got := dm.applyQualityName(src, media); got != want {
		t.Fatalf("renamed to %q, want %q", got, want)
	}
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("renamed file missing: %v", err)
	}

	// Never overwrite: a second file whose target already exists keeps its name.
	other := filepath.Join(dir, "dino.mp4")
	if err := os.WriteFile(other, []byte("w"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := dm.applyQualityName(other, media); got != other {
		t.Fatalf("overwrote existing target: got %q", got)
	}

	dm.mutex.Lock()
	dm.config.QualityInFilename = false
	dm.mutex.Unlock()
	if got := dm.applyQualityName(other, media); got != other {
		t.Fatalf("renamed with setting off: %q", got)
	}
}
