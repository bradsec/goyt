package manager

import (
	"encoding/json"
	"sync"
	"testing"

	"goyt/internal/config"
	"goyt/internal/core"
)

// TestAddDownloadReturnsSnapshot guards against the data race where AddDownload
// returned the live *Download stored in the map, which the API layer then
// JSON-encoded without the manager lock while a worker mutated the same object.
// Run with -race: if the returned value aliases the stored object, the
// concurrent encode (no lock) and status mutation (lock) trip the detector.
func TestAddDownloadReturnsSnapshot(t *testing.T) {
	dir := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.DownloadPath = dir
	// Nonexistent binary so the dedup info probe fails fast and offline.
	d := core.NewDownloader("/nonexistent/yt-dlp", "/nonexistent/ffmpeg", "", false, false)
	dm := NewDownloadManager(d, 0, dir, cfg) // 0 workers: nothing drains the queue
	defer dm.Shutdown()

	got, err := dm.AddDownload(core.DownloadRequest{
		URL:       "https://example.com/watch?v=raceguard",
		Type:      core.VideoDownload,
		Quality:   "best",
		Format:    "mp4",
		OutputDir: dir,
	})
	if err != nil {
		t.Fatalf("AddDownload: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); _, _ = json.Marshal(got) }()
		go func() { defer wg.Done(); dm.UpdateDownloadStatus(got.ID, core.StatusDownloading) }()
	}
	wg.Wait()
}
