package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"goyt/internal/config"
	"goyt/internal/core"
	"goyt/internal/manager"
	"goyt/internal/ui"
)

// newTestHandler builds a Handler backed by a real download manager pointed
// at a temp directory and a nonexistent yt-dlp binary.
func newTestHandler(t *testing.T) (*Handler, *manager.DownloadManager) {
	t.Helper()
	dir := t.TempDir()
	cfg := &config.Config{
		DownloadPath:           dir,
		MaxConcurrentDownloads: 1,
		YtDlpPath:              dir + "/yt-dlp-missing",
		FfmpegPath:             "ffmpeg-missing",
		Port:                   3000,
		DefaultVideoFormat:     "mp4",
		DefaultAudioFormat:     "mp3",
		DefaultVideoQuality:    "1080p",
	}
	downloader := core.NewDownloader(cfg.YtDlpPath, cfg.FfmpegPath, "", false, false)
	dm := manager.NewDownloadManager(downloader, 1, dir, cfg)
	t.Cleanup(dm.Shutdown)
	return NewHandler(cfg, dir+"/config.json", dm, nil), dm
}

func TestUpdateConfigIgnoresCookiesFilePath(t *testing.T) {
	// The web UI must not be able to repoint cookies_file_path: it is the target
	// for cookie upload (write) and delete (remove), so a web-settable value
	// would be an arbitrary file write/delete primitive.
	h, _ := newTestHandler(t)

	body := config.Config{
		DownloadPath:                t.TempDir(),
		MaxConcurrentDownloads:      1,
		Port:                        3000,
		DefaultVideoFormat:          "mp4",
		DefaultAudioFormat:          "mp3",
		DefaultVideoQuality:         "1080p",
		PlaylistLoadTimeoutSeconds:  180,
		DownloadStartTimeoutSeconds: 60,
		CookiesFilePath:             "/etc/passwd",
	}
	buf, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("POST", "/api/config", bytes.NewReader(buf))
	w := httptest.NewRecorder()
	h.UpdateConfig(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("UpdateConfig status = %d, body %s", w.Code, w.Body.String())
	}
	if got := h.currentConfig().CookiesFilePath; got != "" {
		t.Errorf("CookiesFilePath must be preserved, web set it to %q", got)
	}
}

func TestGetConfig(t *testing.T) {
	handler, _ := newTestHandler(t)

	req := httptest.NewRequest("GET", "/api/config", nil)
	w := httptest.NewRecorder()

	handler.GetConfig(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var response config.Config
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if response.Port != 3000 {
		t.Errorf("Expected port 3000, got %d", response.Port)
	}
}

func TestStartDownloadInvalidJSON(t *testing.T) {
	handler, _ := newTestHandler(t)

	req := httptest.NewRequest("POST", "/api/downloads", bytes.NewBufferString("invalid json"))
	w := httptest.NewRecorder()

	handler.StartDownload(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400, got %d", w.Code)
	}
}

func TestStartDownloadMissingURL(t *testing.T) {
	handler, _ := newTestHandler(t)

	jsonBody, _ := json.Marshal(map[string]string{
		"type": "video", "quality": "720p", "format": "mp4",
	})
	req := httptest.NewRequest("POST", "/api/downloads", bytes.NewBuffer(jsonBody))
	w := httptest.NewRecorder()

	handler.StartDownload(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400, got %d", w.Code)
	}
}

func TestStartDownloadRejectsNonHTTPURL(t *testing.T) {
	handler, _ := newTestHandler(t)

	for _, badURL := range []string{
		"file:///etc/passwd",
		"ftp://example.com/video",
		"javascript:alert(1)",
		"/local/path",
	} {
		jsonBody, _ := json.Marshal(map[string]string{
			"url": badURL, "type": "video", "quality": "720p", "format": "mp4",
		})
		req := httptest.NewRequest("POST", "/api/downloads", bytes.NewBuffer(jsonBody))
		w := httptest.NewRecorder()

		handler.StartDownload(w, req)

		if w.Code != http.StatusBadRequest {
			t.Errorf("URL %q: expected status 400, got %d", badURL, w.Code)
		}
	}
}

func TestGetDownloadsEmpty(t *testing.T) {
	handler, _ := newTestHandler(t)

	req := httptest.NewRequest("GET", "/api/downloads", nil)
	w := httptest.NewRecorder()

	handler.GetDownloads(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Expected application/json, got %q", ct)
	}
}

func TestDeleteDownloadUnknownID(t *testing.T) {
	handler, _ := newTestHandler(t)

	router := SetupRoutes(handler, ui.Assets)
	req := httptest.NewRequest("DELETE", "/api/downloads/does-not-exist", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400, got %d", w.Code)
	}
}

func TestHealthEndpoint(t *testing.T) {
	handler, _ := newTestHandler(t)

	router := SetupRoutes(handler, ui.Assets)
	for _, path := range []string{"/health", "/api/health"} {
		req := httptest.NewRequest("GET", path, nil)
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("%s: expected 200, got %d", path, w.Code)
		}
		var resp map[string]any
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatalf("%s: failed to decode: %v", path, err)
		}
		if resp["status"] != "healthy" {
			t.Errorf("%s: expected healthy, got %v", path, resp["status"])
		}
	}
}

func TestMethodNotAllowed(t *testing.T) {
	handler, _ := newTestHandler(t)

	router := SetupRoutes(handler, ui.Assets)
	req := httptest.NewRequest("PUT", "/api/config", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("Expected 405, got %d", w.Code)
	}
}

func TestMediaContentType(t *testing.T) {
	cases := map[string]string{
		"clip.mp4":  "video/mp4",
		"clip.MP4":  "video/mp4",
		"clip.webm": "video/webm",
		"song.mp3":  "audio/mpeg",
		"song.m4a":  "audio/mp4",
		"voice.ogg": "audio/ogg",
		"clip.m4v":  "video/mp4",
		"clip.mkv":  "video/x-matroska",
		"clip.mov":  "video/quicktime",
		"voice.opus": "audio/ogg",
		"voice.wav": "audio/wav",
		"clip.ogv":  "video/ogg",
		"weird.xyz": "application/octet-stream",
	}
	for name, want := range cases {
		if got := mediaContentType(name); got != want {
			t.Errorf("mediaContentType(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestPathWithinDir(t *testing.T) {
	dir := t.TempDir()
	inside := filepath.Join(dir, "a", "b.mp4")
	if !pathWithinDir(inside, dir) {
		t.Errorf("expected %q within %q", inside, dir)
	}
	outside := filepath.Join(dir, "..", "escape.mp4")
	if pathWithinDir(outside, dir) {
		t.Errorf("expected %q outside %q", outside, dir)
	}
	// The directory itself is not "within" itself.
	if pathWithinDir(dir, dir) {
		t.Errorf("expected dir %q not within itself", dir)
	}
	// Deeply nested escape resolves outside.
	deepEscape := filepath.Join(dir, "a", "..", "..", "escape.mp4")
	if pathWithinDir(deepEscape, dir) {
		t.Errorf("expected %q outside %q", deepEscape, dir)
	}
	// A sibling dir sharing a name prefix is not within.
	sibling := dir + "-sibling/file.mp4"
	if pathWithinDir(sibling, dir) {
		t.Errorf("expected sibling %q outside %q", sibling, dir)
	}
}

func TestPathWithinDirSymlinkEscape(t *testing.T) {
	dir := t.TempDir()
	outsideDir := t.TempDir()

	secret := filepath.Join(outsideDir, "secret.mp4")
	if err := os.WriteFile(secret, []byte("SECRET"), 0o644); err != nil {
		t.Fatalf("write secret: %v", err)
	}

	// A symlink inside dir that points outside must not be considered within.
	escapeLink := filepath.Join(dir, "escape.mp4")
	if err := os.Symlink(secret, escapeLink); err != nil {
		t.Skipf("symlinks unsupported: %v", err)
	}
	if pathWithinDir(escapeLink, dir) {
		t.Errorf("expected symlink %q (-> %q) to be rejected as outside %q", escapeLink, secret, dir)
	}

	// A real file inside dir is still within (symlink resolution must not break
	// the legitimate case).
	inside := filepath.Join(dir, "clip.mp4")
	if err := os.WriteFile(inside, []byte("DATA"), 0o644); err != nil {
		t.Fatalf("write inside: %v", err)
	}
	if !pathWithinDir(inside, dir) {
		t.Errorf("expected real file %q within %q", inside, dir)
	}
}

// waitUntilTerminal polls GetDownload until the record reaches a terminal
// state (failed or canceled), which means the worker goroutine is done
// writing to the struct. Fails fast if the download record is not found.
func waitUntilTerminal(t *testing.T, dm *manager.DownloadManager, id string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		cur, ok := dm.GetDownload(id)
		if !ok {
			t.Fatalf("waitUntilTerminal: download %s not found", id)
		}
		if cur.Status == core.StatusFailed || cur.Status == core.StatusCanceled {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Logf("warning: download %s did not reach terminal state within deadline", id)
}

// addCompletedDownload queues a download, waits for the worker to fail fast
// (the test yt-dlp binary is absent), then promotes the record to completed
// with a real file on disk so stream tests have a stable fixture.
//
// Both mutations (status and output path) go through lock-protected manager
// methods. The returned snapshot is re-fetched after the mutations so callers
// see consistent values.
func addCompletedDownload(t *testing.T, h *Handler, dm *manager.DownloadManager, dtype core.DownloadType, filename string) *core.Download {
	t.Helper()
	fixturePath := filepath.Join(h.currentConfig().DownloadPath, filename)
	if err := os.WriteFile(fixturePath, []byte("FAKE-MEDIA-BYTES-0123456789"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	d, err := dm.AddDownload(core.DownloadRequest{
		URL:    "https://example.com/" + filename,
		Type:   dtype,
		Format: strings.TrimPrefix(filepath.Ext(filename), "."),
	})
	if err != nil {
		t.Fatalf("AddDownload: %v", err)
	}
	// Wait for the worker to finish so no goroutine concurrently writes d.
	waitUntilTerminal(t, dm, d.ID)
	// Both mutations go through lock-protected manager methods.
	dm.UpdateDownloadStatus(d.ID, core.StatusCompleted)
	dm.UpdateDownloadOutputPath(d.ID, fixturePath)
	updated, _ := dm.GetDownload(d.ID)
	return updated
}

func TestStreamFileFullRequest(t *testing.T) {
	handler, dm := newTestHandler(t)
	d := addCompletedDownload(t, handler, dm, core.VideoDownload, "clip.mp4")

	router := SetupRoutes(handler, ui.Assets)
	req := httptest.NewRequest("GET", "/api/downloads/"+d.ID+"/stream", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "video/mp4" {
		t.Errorf("expected video/mp4, got %q", ct)
	}
	if ar := w.Header().Get("Accept-Ranges"); ar != "bytes" {
		t.Errorf("expected Accept-Ranges bytes, got %q", ar)
	}
}

func TestStreamFileRangeRequest(t *testing.T) {
	handler, dm := newTestHandler(t)
	d := addCompletedDownload(t, handler, dm, core.VideoDownload, "clip.mp4")

	router := SetupRoutes(handler, ui.Assets)
	req := httptest.NewRequest("GET", "/api/downloads/"+d.ID+"/stream", nil)
	req.Header.Set("Range", "bytes=0-3")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusPartialContent {
		t.Fatalf("expected 206, got %d", w.Code)
	}
	if cr := w.Header().Get("Content-Range"); cr == "" {
		t.Errorf("expected Content-Range header, got empty")
	}
	if w.Body.Len() != 4 {
		t.Errorf("expected 4 bytes, got %d", w.Body.Len())
	}
}

func TestStreamFileNotCompleted(t *testing.T) {
	handler, dm := newTestHandler(t)
	d, err := dm.AddDownload(core.DownloadRequest{
		URL: "https://example.com/pending.mp4", Type: core.VideoDownload, Format: "mp4",
	})
	if err != nil {
		t.Fatalf("AddDownload: %v", err)
	}
	// The worker will fail fast (missing yt-dlp binary). Wait for it so the
	// record reaches StatusFailed — a non-completed state the handler rejects.
	waitUntilTerminal(t, dm, d.ID)

	router := SetupRoutes(handler, ui.Assets)
	req := httptest.NewRequest("GET", "/api/downloads/"+d.ID+"/stream", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestStreamFileUnknownID(t *testing.T) {
	handler, _ := newTestHandler(t)
	router := SetupRoutes(handler, ui.Assets)
	req := httptest.NewRequest("GET", "/api/downloads/nope/stream", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestStreamFileOutsideDownloadDir(t *testing.T) {
	handler, dm := newTestHandler(t)
	// Add a download that fails fast, then promote it to completed but point
	// its OutputPath outside the configured download directory.
	d, err := dm.AddDownload(core.DownloadRequest{
		URL: "https://example.com/elsewhere.mp4", Type: core.VideoDownload, Format: "mp4",
	})
	if err != nil {
		t.Fatalf("AddDownload: %v", err)
	}
	waitUntilTerminal(t, dm, d.ID)
	dm.UpdateDownloadStatus(d.ID, core.StatusCompleted)
	dm.UpdateDownloadOutputPath(d.ID, filepath.Join(t.TempDir(), "elsewhere.mp4"))

	router := SetupRoutes(handler, ui.Assets)
	req := httptest.NewRequest("GET", "/api/downloads/"+d.ID+"/stream", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

// TestStartDownloadAlreadyDownloaded verifies that re-submitting a URL that is
// already present as a completed download returns 409 with a distinct code, so
// the UI can show an informational notice instead of a generic error.
func TestStartDownloadAlreadyDownloaded(t *testing.T) {
	handler, dm := newTestHandler(t)
	// Seed a download and promote it to completed. Format "mov" is a video
	// format that does not require ffmpeg, so the ffmpeg gate is not hit.
	d, err := dm.AddDownload(core.DownloadRequest{
		URL: "https://example.com/clip", Type: core.VideoDownload, Format: "mov",
	})
	if err != nil {
		t.Fatalf("AddDownload: %v", err)
	}
	waitUntilTerminal(t, dm, d.ID)
	dm.UpdateDownloadStatus(d.ID, core.StatusCompleted)

	router := SetupRoutes(handler, ui.Assets)
	body, _ := json.Marshal(map[string]string{
		"url": "https://example.com/clip", "type": "video", "format": "mov",
	})
	req := httptest.NewRequest("POST", "/api/downloads", bytes.NewReader(body))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d (body: %s)", w.Code, w.Body.String())
	}
	var resp ErrorResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if resp.Code != "ALREADY_DOWNLOADED" {
		t.Errorf("expected code ALREADY_DOWNLOADED, got %q", resp.Code)
	}
	if resp.Message == "" {
		t.Errorf("expected a human-readable message, got empty")
	}
}
