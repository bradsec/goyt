package api

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"goyt/internal/config"
)

func TestResolveCookiesPath(t *testing.T) {
	// Explicit path wins.
	cfg := &config.Config{CookiesFilePath: "/data/my-cookies.txt"}
	if got := resolveCookiesPath(cfg, "/etc/goyt/config.json"); got != "/data/my-cookies.txt" {
		t.Errorf("explicit path = %q, want /data/my-cookies.txt", got)
	}

	// Empty path falls back to config-dir/cookies.txt.
	cfg = &config.Config{CookiesFilePath: ""}
	want := filepath.Join("/etc/goyt", "cookies.txt")
	if got := resolveCookiesPath(cfg, "/etc/goyt/config.json"); got != want {
		t.Errorf("default path = %q, want %q", got, want)
	}
}

func TestValidateCookiesContent(t *testing.T) {
	netscapeHeader := "# Netscape HTTP Cookie File\n.example.com\tTRUE\t/\tFALSE\t0\tname\tvalue\n"
	tabLineOnly := ".example.com\tTRUE\t/\tFALSE\t0\tname\tvalue\n"
	httpHeader := "# HTTP Cookie File\n"

	valid := []string{netscapeHeader, tabLineOnly, httpHeader}
	for _, c := range valid {
		if err := validateCookiesContent([]byte(c)); err != nil {
			t.Errorf("validateCookiesContent(%q) = %v, want nil", c, err)
		}
	}

	// Junk text with no header and no tab-delimited line.
	if err := validateCookiesContent([]byte("just some random text\nno tabs here\n")); err == nil {
		t.Error("expected error for non-cookie text, got nil")
	}

	// Invalid UTF-8.
	if err := validateCookiesContent([]byte{0xff, 0xfe, 0x00}); err == nil {
		t.Error("expected error for invalid UTF-8, got nil")
	}

	// Too large.
	big := strings.Repeat("a", (1<<20)+1)
	if err := validateCookiesContent([]byte(big)); err == nil {
		t.Error("expected error for oversized content, got nil")
	}
}

// newCookiesTestHandler builds a Handler backed by a temp config dir.
func newCookiesTestHandler(t *testing.T) (*Handler, string) {
	t.Helper()
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	cfg := config.DefaultConfig()
	cfg.DownloadPath = dir
	if err := cfg.Save(configPath); err != nil {
		t.Fatalf("save config: %v", err)
	}
	h := NewHandler(cfg, configPath, nil, nil)
	return h, dir
}

func TestUploadCookiesWritesFileAndUpdatesConfig(t *testing.T) {
	h, dir := newCookiesTestHandler(t)

	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)
	fw, _ := mw.CreateFormFile("file", "cookies.txt")
	fw.Write([]byte("# Netscape HTTP Cookie File\n.example.com\tTRUE\t/\tFALSE\t0\tn\tv\n"))
	mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/cookies", body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()

	h.UploadCookies(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	want := filepath.Join(dir, "cookies.txt")
	if _, err := os.Stat(want); err != nil {
		t.Errorf("cookies file not written at %s: %v", want, err)
	}
	if got := h.currentConfig().CookiesFilePath; got != want {
		t.Errorf("config CookiesFilePath = %q, want %q", got, want)
	}
}

func TestUploadCookiesRejectsJunk(t *testing.T) {
	h, _ := newCookiesTestHandler(t)

	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)
	fw, _ := mw.CreateFormFile("file", "cookies.txt")
	fw.Write([]byte("not a cookies file"))
	mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/cookies", body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()

	h.UploadCookies(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestCookiesStatusAndDelete(t *testing.T) {
	h, dir := newCookiesTestHandler(t)
	path := filepath.Join(dir, "cookies.txt")
	if err := os.WriteFile(path, []byte("# Netscape HTTP Cookie File\n"), 0600); err != nil {
		t.Fatal(err)
	}
	// Point config at the file so resolveCookiesPath finds it.
	cfg := h.currentConfig()
	cfg.CookiesFilePath = path

	// Status: present.
	rec := httptest.NewRecorder()
	h.GetCookies(rec, httptest.NewRequest(http.MethodGet, "/api/cookies", nil))
	var status map[string]any
	json.Unmarshal(rec.Body.Bytes(), &status)
	if status["present"] != true {
		t.Errorf("present = %v, want true", status["present"])
	}

	// Delete.
	recDel := httptest.NewRecorder()
	h.DeleteCookies(recDel, httptest.NewRequest(http.MethodDelete, "/api/cookies", nil))
	if recDel.Code != http.StatusOK {
		t.Errorf("delete status = %d", recDel.Code)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("file still exists after delete: %v", err)
	}
	// The path was the managed default, so deleting also clears the config field.
	if got := h.currentConfig().CookiesFilePath; got != "" {
		t.Errorf("CookiesFilePath = %q after delete, want cleared", got)
	}
}

func TestDeleteCookiesKeepsCustomPath(t *testing.T) {
	h, dir := newCookiesTestHandler(t)
	custom := filepath.Join(dir, "custom-cookies.txt")
	if err := os.WriteFile(custom, []byte("# Netscape HTTP Cookie File\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := h.currentConfig()
	cfg.CookiesFilePath = custom // a custom path, not the managed default

	rec := httptest.NewRecorder()
	h.DeleteCookies(rec, httptest.NewRequest(http.MethodDelete, "/api/cookies", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("delete status = %d", rec.Code)
	}
	if _, err := os.Stat(custom); !os.IsNotExist(err) {
		t.Errorf("custom file still exists after delete: %v", err)
	}
	// A custom path is preserved so a later upload reuses it.
	if got := h.currentConfig().CookiesFilePath; got != custom {
		t.Errorf("CookiesFilePath = %q after delete, want preserved %q", got, custom)
	}
}
