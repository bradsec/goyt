package api

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"goyt/internal/config"
)

// maxCookiesBytes caps an uploaded cookies file. Cookie jars are small; this
// guards against memory exhaustion and accidental wrong-file uploads.
const maxCookiesBytes = 1 << 20 // 1 MiB

// resolveCookiesPath returns where the cookies file lives: the configured
// cookies_file_path when set, otherwise a managed cookies.txt next to the
// config file.
func resolveCookiesPath(cfg *config.Config, configPath string) string {
	if cfg.CookiesFilePath != "" {
		return filepath.Clean(cfg.CookiesFilePath)
	}
	return defaultCookiesPath(configPath)
}

// defaultCookiesPath is the managed location used when no cookies_file_path is
// configured: cookies.txt next to the config file.
func defaultCookiesPath(configPath string) string {
	return filepath.Join(filepath.Dir(configPath), "cookies.txt")
}

// validateCookiesContent applies light validation to an uploaded cookies file:
// size cap, valid UTF-8, and a shape that looks like a Netscape cookies file.
// It is tolerant of format variants: a recognised header OR a single
// tab-delimited data line is enough.
func validateCookiesContent(data []byte) error {
	if len(data) > maxCookiesBytes {
		return fmt.Errorf("cookies file too large (max %d bytes)", maxCookiesBytes)
	}
	if !utf8.Valid(data) {
		return fmt.Errorf("cookies file is not valid UTF-8 text")
	}
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "# Netscape HTTP Cookie File") ||
			strings.HasPrefix(trimmed, "# HTTP Cookie File") {
			return nil
		}
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		// A Netscape data line is tab-separated: domain, flag, path, secure,
		// expiry, name, value (7 fields). Accept >= 6 to tolerate variants.
		if len(strings.Split(line, "\t")) >= 6 {
			return nil
		}
	}
	return fmt.Errorf("file does not look like a Netscape cookies.txt")
}

// cookiesStatus reports whether a cookies file is present and when it changed.
func (h *Handler) cookiesStatus() map[string]any {
	path := resolveCookiesPath(h.currentConfig(), h.configPath)
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	resp := map[string]any{"present": false, "path": path, "modified": ""}
	if info, err := os.Stat(path); err == nil && !info.IsDir() {
		resp["present"] = true
		resp["modified"] = info.ModTime().UTC().Format(time.RFC3339)
	}
	return resp
}

// GetCookies reports whether a cookies file is present and when it changed.
func (h *Handler) GetCookies(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.cookiesStatus())
}

// UploadCookies validates and stores an uploaded Netscape cookies file, then
// updates and persists config so downloads and URL validation use it at once.
func (h *Handler) UploadCookies(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxCookiesBytes+4096) // room for multipart overhead
	if err := r.ParseMultipartForm(maxCookiesBytes + 4096); err != nil {
		WriteValidationError(w, "Upload too large or malformed.")
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		WriteValidationError(w, "No file provided (expected form field \"file\").")
		return
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, maxCookiesBytes+1))
	if err != nil {
		WriteInternalError(w, "Failed to read uploaded file.")
		return
	}
	if err := validateCookiesContent(data); err != nil {
		WriteValidationError(w, err.Error())
		return
	}

	target := resolveCookiesPath(h.currentConfig(), h.configPath)
	if info, statErr := os.Stat(filepath.Dir(target)); statErr != nil || !info.IsDir() {
		log.Printf("[API] UploadCookies: target directory missing: %s", filepath.Dir(target))
		WriteValidationError(w, "Cookies file directory does not exist. Set cookies_file_path to a writable location.")
		return
	}
	if err := os.WriteFile(target, data, 0600); err != nil {
		log.Printf("[API] UploadCookies: write failed: %v", err)
		WriteInternalError(w, "Failed to save cookies file. Check directory permissions.")
		return
	}

	// Persist the resolved path and reconfigure, mirroring UpdateConfig, so the
	// downloader and validation probes use the file immediately.
	newCfg := *h.currentConfig()
	newCfg.CookiesFilePath = target
	if err := newCfg.Save(h.configPath); err != nil {
		log.Printf("[API] UploadCookies: config save failed: %v", err)
		WriteInternalError(w, "Cookies saved but config update failed.")
		return
	}
	h.mu.Lock()
	h.config = &newCfg
	h.mu.Unlock()
	if h.downloadManager != nil {
		h.downloadManager.UpdateConfig(&newCfg)
	}

	writeJSON(w, http.StatusOK, h.cookiesStatus())
}

// DeleteCookies removes the cookies file at the resolved path. A missing file
// is treated as success. When the path is the managed default (an uploaded
// file, not a user-set custom path), cookies_file_path is also cleared so the
// setting fully resets; a custom path is left in place so a later upload reuses
// it. cookieArgs already tolerates a missing file either way.
func (h *Handler) DeleteCookies(w http.ResponseWriter, r *http.Request) {
	cfg := h.currentConfig()
	path := resolveCookiesPath(cfg, h.configPath)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		log.Printf("[API] DeleteCookies: remove failed: %v", err)
		WriteInternalError(w, "Failed to remove cookies file.")
		return
	}

	if cfg.CookiesFilePath == defaultCookiesPath(h.configPath) {
		newCfg := *cfg
		newCfg.CookiesFilePath = ""
		if err := newCfg.Save(h.configPath); err != nil {
			log.Printf("[API] DeleteCookies: config save failed: %v", err)
			WriteInternalError(w, "Cookies removed but config update failed.")
			return
		}
		h.mu.Lock()
		h.config = &newCfg
		h.mu.Unlock()
		if h.downloadManager != nil {
			h.downloadManager.UpdateConfig(&newCfg)
		}
	}

	writeJSON(w, http.StatusOK, h.cookiesStatus())
}
