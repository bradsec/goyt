package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestDecodeDownloadRequestValidation covers the format/quality/type allowlists
// added to block path traversal and argument injection through req.Format
// reaching yt-dlp's --output template (finding DL-1).
func TestDecodeDownloadRequestValidation(t *testing.T) {
	cases := []struct {
		name string
		body string
		ok   bool
	}{
		{"valid video", `{"url":"https://example.com/v","type":"video","format":"mp4","quality":"1080p"}`, true},
		{"valid audio", `{"url":"https://example.com/v","type":"audio","format":"mp3","quality":"best"}`, true},
		{"passthrough container mov", `{"url":"https://example.com/v","type":"video","format":"mov"}`, true},
		{"empty format and quality", `{"url":"https://example.com/v"}`, true},
		{"path traversal in format", `{"url":"https://example.com/v","format":"mp4/../../../tmp/evil"}`, false},
		{"backslash in format", `{"url":"https://example.com/v","format":"mp4\\..\\x"}`, false},
		{"dot in format", `{"url":"https://example.com/v","format":"mp4.evil"}`, false},
		{"output template in format", `{"url":"https://example.com/v","format":"%(title)s"}`, false},
		{"space in format", `{"url":"https://example.com/v","format":"mp4 x"}`, false},
		{"traversal in quality", `{"url":"https://example.com/v","quality":"1080p/.."}`, false},
		{"unknown type", `{"url":"https://example.com/v","type":"script"}`, false},
		{"missing url", `{"format":"mp4"}`, false},
		{"non-http scheme", `{"url":"file:///etc/passwd","format":"mp4"}`, false},
		{"ssrf localhost", `{"url":"http://localhost:8080/x"}`, false},
		{"ssrf loopback ip", `{"url":"http://127.0.0.1/x"}`, false},
		{"ssrf metadata", `{"url":"http://169.254.169.254/latest/meta-data/"}`, false},
		{"ssrf unspecified", `{"url":"http://0.0.0.0/x"}`, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/api/downloads", strings.NewReader(tc.body))
			w := httptest.NewRecorder()
			_, ok := decodeDownloadRequest(w, req)
			if ok != tc.ok {
				t.Fatalf("decodeDownloadRequest ok=%v, want %v (status %d)", ok, tc.ok, w.Code)
			}
			if !tc.ok && w.Code == 0 {
				t.Errorf("expected an error response to be written on rejection")
			}
		})
	}
}

// TestDecodeDownloadRequestBodyLimit verifies the MaxBytesReader cap rejects
// oversized request bodies (finding API-1).
func TestDecodeDownloadRequestBodyLimit(t *testing.T) {
	huge := `{"url":"https://example.com/` + strings.Repeat("a", 2<<20) + `"}`
	req := httptest.NewRequest("POST", "/api/downloads", strings.NewReader(huge))
	w := httptest.NewRecorder()

	if _, ok := decodeDownloadRequest(w, req); ok {
		t.Fatalf("expected oversized body (%d bytes) to be rejected", len(huge))
	}
}

// TestUpdateConfigPreservesExecutablePaths verifies the web config API cannot
// repoint yt-dlp/ffmpeg at an arbitrary binary (finding API-5).
func TestUpdateConfigPreservesExecutablePaths(t *testing.T) {
	handler, _ := newTestHandler(t)
	origYtDlp := handler.currentConfig().YtDlpPath
	origFfmpeg := handler.currentConfig().FfmpegPath

	body := `{"download_path":"./downloads","max_concurrent_downloads":3,` +
		`"default_video_format":"mp4","default_audio_format":"mp3","port":3000,` +
		`"playlist_load_timeout_seconds":180,"download_start_timeout_seconds":60,` +
		`"yt_dlp_path":"/tmp/evil","ffmpeg_path":"/tmp/evil-ffmpeg"}`
	req := httptest.NewRequest("POST", "/api/config", strings.NewReader(body))
	w := httptest.NewRecorder()
	handler.UpdateConfig(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("UpdateConfig status=%d body=%s", w.Code, w.Body.String())
	}
	if got := handler.currentConfig().YtDlpPath; got != origYtDlp {
		t.Errorf("YtDlpPath changed via API: got %q, want %q", got, origYtDlp)
	}
	if got := handler.currentConfig().FfmpegPath; got != origFfmpeg {
		t.Errorf("FfmpegPath changed via API: got %q, want %q", got, origFfmpeg)
	}
}
