package manager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"goyt/internal/config"
	"goyt/internal/core"
)

func playlistTestManager(t *testing.T, urls []string) *DownloadManager {
	t.Helper()
	dir := t.TempDir()
	var script strings.Builder
	script.WriteString("#!/bin/sh\ncat <<'PLAYLIST_JSON'\n")
	for i, url := range urls {
		entry, err := json.Marshal(map[string]string{"id": fmt.Sprint(i), "url": url, "title": fmt.Sprint(i)})
		if err != nil {
			t.Fatal(err)
		}
		script.Write(entry)
		script.WriteByte('\n')
	}
	script.WriteString("PLAYLIST_JSON\n")
	path := filepath.Join(dir, "yt-dlp")
	if err := os.WriteFile(path, []byte(script.String()), 0o700); err != nil {
		t.Fatal(err)
	}
	dm := NewDownloadManager(core.NewDownloader(path, "ffmpeg", "", false, false), 0, dir, &config.Config{})
	t.Cleanup(dm.Shutdown)
	return dm
}

func receiveQueued(t *testing.T, dm *DownloadManager) *core.Download {
	t.Helper()
	select {
	case download := <-dm.queue:
		return download
	case <-time.After(2 * time.Second):
		t.Fatal("queued download never reached the worker channel")
		return nil
	}
}

func TestPlaylistOverflowKeepsAllItemsInOrder(t *testing.T) {
	urls := make([]string, 125)
	for i := range urls {
		urls[i] = fmt.Sprintf("https://example.com/%03d", i)
	}
	dm := playlistTestManager(t, urls)
	if _, _, err := dm.AddPlaylistDownload(core.DownloadRequest{URL: "https://example.com/playlist"}); err != nil {
		t.Fatal(err)
	}
	if got := len(dm.GetAllDownloads()); got != len(urls) {
		t.Fatalf("retained %d downloads, want %d", got, len(urls))
	}
	for _, url := range urls {
		if got := receiveQueued(t, dm); got.URL != url || got.Status != core.StatusQueued {
			t.Fatalf("got %+v, want queued %s", got, url)
		}
	}
}

func TestPlaylistSkipsDuplicates(t *testing.T) {
	dm := playlistTestManager(t, []string{"https://example.com/existing", "https://example.com/processing", "https://example.com/new", "https://example.com/new"})
	dm.mutex.Lock()
	dm.downloads["existing"] = &core.Download{ID: "existing", URL: "https://example.com/existing", Status: core.StatusCompleted}
	dm.processingUrls["https://example.com/processing"] = true
	dm.mutex.Unlock()
	req := core.DownloadRequest{URL: "https://example.com/playlist", Audio: "stereo", Range: "sdr"}
	first, skipped, err := dm.AddPlaylistDownload(req)
	if err != nil {
		t.Fatal(err)
	}
	if skipped != 3 {
		t.Fatalf("skipped %d, want 3", skipped)
	}
	if first == nil || first.URL != "https://example.com/new" || len(dm.GetAllDownloads()) != 2 {
		t.Fatalf("duplicates retained: first=%+v downloads=%d", first, len(dm.GetAllDownloads()))
	}
	if _, skipped, err := dm.AddPlaylistDownload(req); !errors.Is(err, ErrActiveDownload) || skipped != 4 {
		t.Fatalf("all-duplicate playlist: skipped=%d err=%v", skipped, err)
	}
}

func TestBackloggedDownloadControls(t *testing.T) {
	dm := playlistTestManager(t, []string{"https://example.com/pause", "https://example.com/cancel", "https://example.com/remove"})
	for range cap(dm.queue) {
		dm.queue <- &core.Download{}
	}
	if _, _, err := dm.AddPlaylistDownload(core.DownloadRequest{URL: "https://example.com/playlist"}); err != nil {
		t.Fatal(err)
	}
	items := dm.GetAllDownloads()
	if err := dm.PauseDownload(items[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := dm.CancelDownload(items[1].ID); err != nil {
		t.Fatal(err)
	}
	if err := dm.RemoveDownload(items[2].ID); err != nil {
		t.Fatal(err)
	}
	for range cap(dm.queue) {
		receiveQueued(t, dm)
	}
	for range 3 {
		if download := receiveQueued(t, dm); dm.processDownload(download) {
			t.Fatalf("claimed stale backlog entry %s", download.ID)
		}
	}
	if got, _ := dm.GetDownload(items[0].ID); got.Status != core.StatusPaused {
		t.Fatalf("paused item became %s", got.Status)
	}
	if got, _ := dm.GetDownload(items[1].ID); got.Status != core.StatusCanceled {
		t.Fatalf("canceled item became %s", got.Status)
	}
	if _, exists := dm.GetDownload(items[2].ID); exists {
		t.Fatal("removed item remains")
	}
}

func TestShutdownPersistsBlockedBacklog(t *testing.T) {
	dm := playlistTestManager(t, []string{"https://example.com/video"})
	for range cap(dm.queue) {
		dm.queue <- &core.Download{}
	}
	first, _, err := dm.AddPlaylistDownload(core.DownloadRequest{URL: "https://example.com/playlist"})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		dm.Shutdown()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown did not stop blocked backlog drainer")
	}
	data, err := os.ReadFile(dm.GetStateFilePath())
	if err != nil {
		t.Fatal(err)
	}
	var state StateFile
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	if saved := state.Downloads[first.ID]; saved == nil || saved.Status != core.StatusQueued {
		t.Fatalf("backlogged item not persisted as queued: %+v", saved)
	}
}

func TestRestoreQueueOrderAndOverflow(t *testing.T) {
	dir := t.TempDir()
	state := StateFile{Version: StateVersion, Downloads: make(map[string]*core.Download)}
	for i := range 125 {
		id := fmt.Sprintf("%03d", i)
		status := []core.DownloadStatus{core.StatusQueued, core.StatusDownloading, core.StatusPostProcessing}[i%3]
		state.Downloads[id] = &core.Download{ID: id, URL: "https://example.com/" + id, Status: status, CreatedAt: time.Unix(int64(i/2), 0)}
	}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".goyt_state.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	dm := NewDownloadManager(core.NewDownloader("yt-dlp", "ffmpeg", "", false, false), 0, dir, &config.Config{})
	t.Cleanup(dm.Shutdown)
	for _, download := range dm.GetAllDownloads() {
		if download.Status != core.StatusQueued {
			t.Fatalf("restored %s as %s", download.ID, download.Status)
		}
	}
	for i := range 125 {
		if got := receiveQueued(t, dm).ID; got != fmt.Sprintf("%03d", i) {
			t.Fatalf("position %d: got %s", i, got)
		}
	}
}

func TestRestoreInterruptedConversion(t *testing.T) {
	for _, tt := range []struct {
		name    string
		file    string
		status  core.DownloadStatus
		missing bool
	}{
		{name: "webm", file: "original.webm", status: core.StatusCompleted},
		{name: "mp4", file: "original.mp4", status: core.StatusCompleted},
		{name: "already_exists", file: "original.webm", status: core.StatusAlreadyExists},
		{name: "missing_original", file: "original.webm", status: core.StatusCompleted, missing: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, tt.file)
			if !tt.missing {
				if err := os.WriteFile(path, []byte("intact original"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			d := core.NewDownloader("missing-yt-dlp", "missing-ffmpeg", "", false, false)
			dm := NewDownloadManager(d, 0, dir, &config.Config{})
			t.Cleanup(dm.Shutdown)
			dm.mutex.Lock()
			dm.downloads["converting"] = &core.Download{
				ID: "converting", URL: "https://example.com/video", Type: core.VideoDownload,
				Status: tt.status, OutputPath: path, Filename: tt.file,
				VideoCodec: "vp9", AudioCodec: "opus",
			}
			dm.mutex.Unlock()
			// Zero worker slots hold the conversion before invoking an external tool.
			if err := dm.ConvertDownload("converting"); err != nil {
				t.Fatal(err)
			}
			dm.mutex.Lock()
			dm.downloads["converting"].Progress = core.DownloadProgress{Phase: "converting", Percentage: 42}
			dm.mutex.Unlock()
			// SaveState models the periodic snapshot during a long conversion.
			if err := dm.SaveState(); err != nil {
				t.Fatal(err)
			}
			checkpoint, err := os.ReadFile(dm.GetStateFilePath())
			if err != nil {
				t.Fatal(err)
			}
			dm.Shutdown()
			// Restore the crash checkpoint rather than the graceful shutdown snapshot.
			if err := os.WriteFile(dm.GetStateFilePath(), checkpoint, 0o600); err != nil {
				t.Fatal(err)
			}
			restored := NewDownloadManager(d, 0, dir, &config.Config{})
			t.Cleanup(restored.Shutdown)
			dl, ok := restored.GetDownload("converting")
			if !ok {
				t.Fatal("original record disappeared")
			}
			wantStatus := core.StatusCompleted
			if tt.missing {
				wantStatus = core.StatusFailed
			}
			if dl.Status != wantStatus {
				t.Fatalf("restored status=%s, want %s", dl.Status, wantStatus)
			}
			if dl.Progress != (core.DownloadProgress{}) {
				t.Errorf("stale conversion progress: %+v", dl.Progress)
			}
			if dl.OutputPath != path || dl.Filename != tt.file || dl.Converted || dl.VideoCodec != "vp9" || dl.AudioCodec != "opus" {
				t.Errorf("original metadata changed: %+v", dl)
			}
			if tt.missing {
				if dl.Error != "Output file not found after restart" {
					t.Errorf("missing original error=%q", dl.Error)
				}
				return
			}
			if data, err := os.ReadFile(path); err != nil || string(data) != "intact original" {
				t.Fatalf("original changed: data=%q err=%v", data, err)
			}
			if err := restored.ConvertDownload(dl.ID); err != nil {
				t.Fatalf("retry conversion: %v", err)
			}
		})
	}
}

func TestAddDownloadStillRejectsFullQueue(t *testing.T) {
	dm := playlistTestManager(t, nil)
	for range cap(dm.queue) {
		dm.queue <- &core.Download{}
	}
	if _, err := dm.AddDownload(core.DownloadRequest{URL: "https://example.com/video"}); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("got %v, want ErrQueueFull", err)
	}
}

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
