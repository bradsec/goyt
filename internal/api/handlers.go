package api

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"goyt/internal/auth"
	"goyt/internal/config"
	"goyt/internal/core"
	"goyt/internal/manager"
)

// Version is the application version, settable at build time via
// -ldflags "-X goyt/internal/api.Version=v1.2.3".
var Version = "dev"

// Type constants
const (
	TypeAudio = "audio"
	TypeVideo = "video"
)

// safeTokenRe gates the user-supplied format and quality values. The format
// reaches yt-dlp's --output filename template and both reach yt-dlp/ffmpeg
// argument selectors, so values are restricted to short alphanumeric tokens.
// This blocks path separators, "..", and output-template metacharacters
// ("%", "(", ")", "/") that would allow path traversal or argument injection
// (finding DL-1). yt-dlp/ffmpeg reject any token that is not a real format.
var safeTokenRe = regexp.MustCompile(`^[a-zA-Z0-9]{1,10}$`)

// blockedDownloadHost reports whether a download target should be refused to
// limit SSRF. It blocks loopback, the unspecified address, and the cloud
// instance-metadata endpoints by literal host. Hostnames that resolve to those
// addresses via DNS are not caught here; yt-dlp resolves the name itself, so
// this is a best-effort guard against the obvious internal targets, not a
// complete SSRF defense (finding API-3).
func blockedDownloadHost(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	if ip.IsLoopback() || ip.IsUnspecified() {
		return true
	}
	// Cloud instance-metadata service addresses.
	switch host {
	case "169.254.169.254", "fd00:ec2::254", "[fd00:ec2::254]":
		return true
	}
	return false
}

// maxBodyBytes caps JSON request bodies to guard against memory-exhaustion DoS.
const maxBodyBytes = 1 << 20 // 1 MiB

type Handler struct {
	mu              sync.RWMutex
	config          *config.Config
	configPath      string
	downloadManager *manager.DownloadManager
	updater         *core.YtDlpUpdater
	sessionSecret   []byte
}

func NewHandler(
	cfg *config.Config,
	configPath string,
	dm *manager.DownloadManager,
	updater *core.YtDlpUpdater,
) *Handler {
	return &Handler{
		config:          cfg,
		configPath:      configPath,
		downloadManager: dm,
		updater:         updater,
		sessionSecret:   cfg.ResolveSessionSecret(),
	}
}

// SessionSecret returns the secret used to sign session cookies, resolved once
// at construction. Exposed so the main package's page gate validates sessions
// against the same secret the API issues them with.
func (h *Handler) SessionSecret() []byte {
	return h.sessionSecret
}

// currentConfig returns the active configuration under a read lock.
func (h *Handler) currentConfig() *config.Config {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.config
}

// writeJSON encodes v as a JSON response.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("[API] Failed to encode response: %v", err)
	}
}

// downloadRequest is the shared payload for download and validation endpoints.
type downloadRequest struct {
	URL     string `json:"url"`
	Type    string `json:"type"`    // "video" or "audio"
	Quality string `json:"quality"` // "best", "worst", "720p", etc.
	Format  string `json:"format"`  // "mp4", "mp3", etc.
}

// decodeDownloadRequest parses and validates the request body shared by the
// download endpoints. It returns false if a response was already written.
func decodeDownloadRequest(w http.ResponseWriter, r *http.Request) (downloadRequest, bool) {
	var req downloadRequest
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteValidationError(w, "Invalid JSON format in request body")
		return req, false
	}
	if req.URL == "" {
		WriteValidationError(w, "URL is required")
		return req, false
	}
	parsed, err := url.Parse(req.URL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		WriteValidationError(w, "URL must be a valid http or https address")
		return req, false
	}
	if blockedDownloadHost(parsed.Hostname()) {
		WriteValidationError(w, "URL host is not allowed")
		return req, false
	}
	if req.Type != "" && req.Type != TypeVideo && req.Type != TypeAudio {
		WriteValidationError(w, "type must be \"video\" or \"audio\"")
		return req, false
	}
	if req.Format != "" && !safeTokenRe.MatchString(req.Format) {
		WriteValidationError(w, "invalid format")
		return req, false
	}
	if req.Quality != "" && !safeTokenRe.MatchString(req.Quality) {
		WriteValidationError(w, "invalid quality")
		return req, false
	}
	return req, true
}

func (r downloadRequest) downloadType() core.DownloadType {
	if r.Type == TypeAudio {
		return core.AudioDownload
	}
	return core.VideoDownload
}

func (h *Handler) toCoreRequest(req downloadRequest) core.DownloadRequest {
	return core.DownloadRequest{
		URL:       req.URL,
		Type:      req.downloadType(),
		Quality:   req.Quality,
		Format:    req.Format,
		OutputDir: h.currentConfig().DownloadPath,
	}
}

// requireFfmpeg writes an error response and returns false when the request
// needs ffmpeg but it is not available.
func (h *Handler) requireFfmpeg(w http.ResponseWriter, req downloadRequest) bool {
	if core.RequiresFfmpeg(req.downloadType(), req.Format) &&
		!core.CheckFfmpegAvailable(h.currentConfig().FfmpegPath) {
		WriteValidationError(w,
			"ffmpeg is required for this download format but is not available. "+
				"Please configure a valid ffmpeg path in settings.")
		return false
	}
	return true
}

func (h *Handler) Health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "healthy",
		"version": Version,
	})
}

// writeSanitizedConfig writes the config as a JSON response with secret fields
// (password hash, session secret) stripped, plus the derived auth_enabled flag.
// Used by both GetConfig and UpdateConfig so neither leaks credentials.
// Any warnings (e.g. a yt-dlp/ffmpeg path that does not resolve) are included
// so the UI can surface them without blocking the save.
func writeSanitizedConfig(w http.ResponseWriter, cfg *config.Config, warnings ...string) {
	resp := struct {
		*config.Config
		WebUIPasswordHash string   `json:"webui_password_hash,omitempty"`
		SessionSecret     string   `json:"session_secret,omitempty"`
		AuthEnabled       bool     `json:"auth_enabled"`
		Warnings          []string `json:"warnings,omitempty"`
		Version           string   `json:"version"`
	}{
		Config:      cfg,
		AuthEnabled: cfg.AuthEnabled(),
		Warnings:    warnings,
		Version:     Version,
	}
	writeJSON(w, http.StatusOK, resp)
}

// dependencyWarnings returns non-fatal warnings for binary paths that do not
// resolve, so a saved config that points at a missing yt-dlp or ffmpeg is
// flagged in the UI instead of failing silently at download time.
func dependencyWarnings(cfg *config.Config) []string {
	var warnings []string
	if !core.CheckYtDlpAvailable(cfg.YtDlpPath) {
		warnings = append(warnings, fmt.Sprintf(
			"yt-dlp not found at %q or on PATH. Downloads will fail until the binary exists there.",
			cfg.YtDlpPath))
	}
	if !core.CheckFfmpegAvailable(cfg.FfmpegPath) {
		warnings = append(warnings, fmt.Sprintf(
			"ffmpeg not found at %q or on PATH. Downloads that need conversion will fail.",
			cfg.FfmpegPath))
	}
	return warnings
}

// applyTimeouts pushes the configured network timeouts onto a downloader so
// playlist enumeration and the pre-download video-info probe honor the values
// set in the settings UI.
func applyTimeouts(d *core.Downloader, cfg *config.Config) {
	d.SetTimeouts(
		time.Duration(cfg.DownloadStartTimeoutSeconds)*time.Second,
		time.Duration(cfg.PlaylistLoadTimeoutSeconds)*time.Second,
	)
}

func (h *Handler) GetConfig(w http.ResponseWriter, r *http.Request) {
	writeSanitizedConfig(w, h.currentConfig())
}

func (h *Handler) UpdateConfig(w http.ResponseWriter, r *http.Request) {
	var newConfig config.Config
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(&newConfig); err != nil {
		WriteValidationError(w, "Invalid JSON format in request body")
		return
	}

	// Preserve fields the web UI must not control. Auth secrets are never sent
	// by the form. The executable paths are infrastructure config: allowing the
	// web UI to repoint yt-dlp/ffmpeg at an arbitrary binary would be a code
	// execution vector, so they are settable only via config.json or the CLI.
	// CookiesFilePath is preserved for the same reason: it is the target for
	// UploadCookies (write) and DeleteCookies (remove), so a web-settable value
	// would be an arbitrary file write/delete primitive. Cookies are managed
	// through Upload/Remove (which write to the managed cookies.txt next to the
	// config) or via a trusted path set directly in config.json.
	current := h.currentConfig()
	newConfig.WebUIPasswordHash = current.WebUIPasswordHash
	newConfig.SessionSecret = current.SessionSecret
	newConfig.YtDlpPath = current.YtDlpPath
	newConfig.FfmpegPath = current.FfmpegPath
	newConfig.CookiesFilePath = current.CookiesFilePath

	if err := newConfig.Validate(); err != nil {
		WriteValidationError(w, fmt.Sprintf("Configuration validation failed: %v", err))
		return
	}

	if err := newConfig.Save(h.configPath); err != nil {
		log.Printf("[API] UpdateConfig: Failed to save config: %v", err)
		WriteInternalError(w, "Failed to save configuration. Please check file permissions and try again.")
		return
	}

	h.mu.Lock()
	h.config = &newConfig
	h.mu.Unlock()

	if h.downloadManager != nil {
		h.downloadManager.UpdateConfig(&newConfig)
	}

	writeSanitizedConfig(w, &newConfig, dependencyWarnings(&newConfig)...)
}

const sessionCookieName = "goyt_session"

const sessionTTL = 7 * 24 * time.Hour

type loginRequest struct {
	Password string `json:"password"`
}

// Login verifies the submitted password against the WEBUI_PASSWORD env override
// (if set) or the stored hash, and on success sets a signed session cookie.
func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteValidationError(w, "Invalid JSON format in request body")
		return
	}

	cfg := h.currentConfig()
	ok := false
	if envPW := os.Getenv("WEBUI_PASSWORD"); envPW != "" {
		ok = subtle.ConstantTimeCompare([]byte(req.Password), []byte(envPW)) == 1
	} else if cfg.WebUIPasswordHash != "" {
		ok = auth.Verify(req.Password, cfg.WebUIPasswordHash)
	}
	if !ok {
		WriteErrorResponse(w, http.StatusUnauthorized,
			"Unauthorized", "Incorrect password.", "AUTH_INVALID")
		return
	}

	token, err := auth.Issue(h.sessionSecret, sessionTTL)
	if err != nil {
		WriteInternalError(w, "Failed to create session.")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   isHTTPS(r),
		MaxAge:   int(sessionTTL / time.Second),
	})
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

// Logout clears the session cookie.
func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

// isHTTPS reports whether the request reached us over TLS, directly or via a
// reverse proxy that set X-Forwarded-Proto.
func isHTTPS(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

func (h *Handler) GetDownloads(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.downloadManager.GetAllDownloads())
}

func (h *Handler) StartDownload(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeDownloadRequest(w, r)
	if !ok {
		return
	}
	if !h.requireFfmpeg(w, req) {
		return
	}

	download, err := h.downloadManager.AddDownload(h.toCoreRequest(req))
	if err != nil {
		switch {
		case errors.Is(err, manager.ErrAlreadyProcessing):
			WriteErrorResponse(w, http.StatusConflict, "Already Processing",
				"This URL is currently being downloaded. Please wait for it to complete.", "ALREADY_PROCESSING")
		case errors.Is(err, manager.ErrAlreadyDownloaded):
			WriteErrorResponse(w, http.StatusConflict, "Already Downloaded",
				"This URL has already been downloaded with the same settings. Check your completed downloads.",
				"ALREADY_DOWNLOADED")
		case errors.Is(err, manager.ErrPlaylistURL):
			WriteValidationError(w, "This appears to be a playlist URL. Please use the playlist download option instead.")
		default:
			writeClientError(w, "StartDownload", err, "Unable to start the download.")
		}
		return
	}

	writeJSON(w, http.StatusOK, download)
}

func (h *Handler) StartPlaylistDownload(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeDownloadRequest(w, r)
	if !ok {
		return
	}
	if !h.requireFfmpeg(w, req) {
		return
	}

	download, err := h.downloadManager.AddPlaylistDownload(h.toCoreRequest(req))
	if err != nil {
		writeClientError(w, "StartPlaylistDownload", err, "Unable to start the playlist download.")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"message":        "Playlist download started",
		"first_download": download,
	})
}

func (h *Handler) StartFirstVideoDownload(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeDownloadRequest(w, r)
	if !ok {
		return
	}
	if !h.requireFfmpeg(w, req) {
		return
	}

	cfg := h.currentConfig()
	downloader := core.NewDownloader(
		cfg.YtDlpPath, cfg.FfmpegPath, cfg.CookiesFilePath,
		cfg.EnableHardwareAccel, cfg.OptimizeForLowPower)
	applyTimeouts(downloader, cfg)
	playlistItems, err := downloader.GetPlaylistItems(req.URL)
	if err != nil {
		writeClientError(w, "StartFirstVideoDownload", err, "Failed to get playlist items.")
		return
	}
	if len(playlistItems) == 0 {
		WriteValidationError(w, "No items found in playlist")
		return
	}

	firstReq := req
	firstReq.URL = playlistItems[0].WatchURL()

	download, err := h.downloadManager.AddDownload(h.toCoreRequest(firstReq))
	if err != nil {
		writeClientError(w, "StartFirstVideoDownload", err, "Unable to start the download.")
		return
	}

	writeJSON(w, http.StatusOK, download)
}

func (h *Handler) ValidateURL(w http.ResponseWriter, r *http.Request) {
	req, ok := decodeDownloadRequest(w, r)
	if !ok {
		return
	}

	cfg := h.currentConfig()
	downloader := core.NewDownloader(
		cfg.YtDlpPath, cfg.FfmpegPath, cfg.CookiesFilePath,
		cfg.EnableHardwareAccel, cfg.OptimizeForLowPower)
	applyTimeouts(downloader, cfg)

	if downloader.IsPlaylistURL(req.URL) {
		h.validatePlaylist(w, req, downloader)
		return
	}

	info, err := downloader.GetVideoInfo(req.URL)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"valid":       false,
			"error":       err.Error(),
			"is_playlist": false,
		})
		return
	}

	response := map[string]any{
		"valid":       true,
		"is_playlist": false,
		"title":       info.Title,
		"filename":    info.Filename,
	}
	existingFile := h.downloadManager.CheckFileExistence(h.toCoreRequest(req))
	response["file_exists"] = existingFile != ""
	if existingFile != "" {
		response["existing_file"] = existingFile
		response["existing_filename"] = filepath.Base(existingFile)
	}

	writeJSON(w, http.StatusOK, response)
}

func (h *Handler) validatePlaylist(w http.ResponseWriter, req downloadRequest, downloader *core.Downloader) {
	playlistItems, err := downloader.GetPlaylistItems(req.URL)
	if err != nil {
		// Enumeration can time out on very large playlists; report it as a
		// playlist without a confirmed count rather than failing validation.
		log.Printf("[API] ValidateURL: Playlist enumeration failed, continuing: %v", err)
		writeJSON(w, http.StatusOK, map[string]any{
			"valid":       true,
			"is_playlist": true,
			"warning":     "Playlist size could not be determined",
			"estimated":   true,
		})
		return
	}

	response := map[string]any{
		"valid":          true,
		"is_playlist":    true,
		"playlist_count": len(playlistItems),
	}

	if len(playlistItems) > 0 {
		first := playlistItems[0]
		response["first_video_title"] = first.Title

		firstReq := req
		firstReq.URL = first.WatchURL()
		existingFile := h.downloadManager.CheckFileExistence(h.toCoreRequest(firstReq))
		response["first_video_exists"] = existingFile != ""
		if existingFile != "" {
			response["existing_file"] = existingFile
			response["existing_filename"] = filepath.Base(existingFile)
		}
	}

	writeJSON(w, http.StatusOK, response)
}

// downloadAction wraps the common pattern of the per-download POST endpoints.
func (h *Handler) downloadAction(w http.ResponseWriter, r *http.Request, status string, fn func(id string) error) {
	id := GetPathVar(r, "id")
	if id == "" {
		WriteValidationError(w, "Download ID is required")
		return
	}

	if err := fn(id); err != nil {
		WriteValidationError(w, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": status})
}

func (h *Handler) DeleteDownload(w http.ResponseWriter, r *http.Request) {
	id := GetPathVar(r, "id")
	if id == "" {
		WriteValidationError(w, "Download ID is required")
		return
	}

	if err := h.downloadManager.RemoveDownload(id); err != nil {
		writeClientError(w, "DeleteDownload", err, "Unable to remove the download.")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) CancelDownload(w http.ResponseWriter, r *http.Request) {
	h.downloadAction(w, r, "canceled", h.downloadManager.CancelDownload)
}

func (h *Handler) PauseDownload(w http.ResponseWriter, r *http.Request) {
	h.downloadAction(w, r, "paused", h.downloadManager.PauseDownload)
}

func (h *Handler) ResumeDownload(w http.ResponseWriter, r *http.Request) {
	h.downloadAction(w, r, "resumed", h.downloadManager.ResumeDownload)
}

func (h *Handler) RetryDownload(w http.ResponseWriter, r *http.Request) {
	h.downloadAction(w, r, "retried", h.downloadManager.RetryDownload)
}

func (h *Handler) ConvertDownload(w http.ResponseWriter, r *http.Request) {
	h.downloadAction(w, r, "converting", h.downloadManager.ConvertDownload)
}

func (h *Handler) ClearAllQueued(w http.ResponseWriter, r *http.Request) {
	if err := h.downloadManager.ClearAllQueued(); err != nil {
		WriteValidationError(w, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"status": "cleared", "message": "All queued downloads cleared"})
}

func (h *Handler) DeleteAllCompleted(w http.ResponseWriter, r *http.Request) {
	if err := h.downloadManager.DeleteAllCompleted(); err != nil {
		writeClientError(w, "DeleteAllCompleted", err, "Unable to delete completed downloads.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"status": "deleted", "message": "All completed downloads and files deleted"})
}

func (h *Handler) ClearAllFailed(w http.ResponseWriter, r *http.Request) {
	if err := h.downloadManager.ClearAllFailed(); err != nil {
		WriteValidationError(w, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"status": "cleared", "message": "All failed downloads cleared"})
}

func (h *Handler) GetUpdateInfo(w http.ResponseWriter, r *http.Request) {
	updateInfo, err := h.updater.CheckForUpdates()
	if err != nil {
		log.Printf("[API] GetUpdateInfo: %v", err)
		WriteInternalError(w, "Failed to check for updates.")
		return
	}
	writeJSON(w, http.StatusOK, updateInfo)
}

func (h *Handler) UpdateYtDlp(w http.ResponseWriter, r *http.Request) {
	if err := h.updater.Update(); err != nil {
		log.Printf("[API] UpdateYtDlp: %v", err)
		WriteInternalError(w, "Update failed.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"status": "success", "message": "yt-dlp updated successfully"})
}

func (h *Handler) GetVersions(w http.ResponseWriter, r *http.Request) {
	cfg := h.currentConfig()
	writeJSON(w, http.StatusOK, core.GetVersionInfo(cfg.YtDlpPath, cfg.FfmpegPath))
}

func (h *Handler) CheckFfmpeg(w http.ResponseWriter, r *http.Request) {
	cfg := h.currentConfig()
	configuredPath := cfg.FfmpegPath
	var version string
	var actualPath string
	var available bool

	if core.IsCommandAvailable(configuredPath) {
		available = true
		actualPath = configuredPath
	} else if core.IsCommandAvailable("ffmpeg") {
		available = true
		actualPath = "ffmpeg (system PATH)"
	} else {
		available = false
		actualPath = configuredPath
	}

	if available {
		versions := core.GetVersionInfo(cfg.YtDlpPath, cfg.FfmpegPath)
		version = versions.FfmpegVersion
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"available":       available,
		"version":         version,
		"configured_path": configuredPath,
		"actual_path":     actualPath,
		"path":            actualPath, // For backward compatibility
	})
}

func (h *Handler) DownloadFile(w http.ResponseWriter, r *http.Request) {
	id := GetPathVar(r, "id")
	if id == "" {
		WriteValidationError(w, "Download ID is required")
		return
	}

	download, exists := h.downloadManager.GetDownload(id)
	if !exists {
		WriteErrorResponse(w, http.StatusNotFound, "Not Found", "Download not found", "NOT_FOUND")
		return
	}

	if download.Status != core.StatusCompleted && download.Status != core.StatusAlreadyExists {
		WriteValidationError(w, fmt.Sprintf("Download not completed (status: %s)", download.Status))
		return
	}

	if !pathWithinDir(download.OutputPath, h.currentConfig().DownloadPath) {
		WriteErrorResponse(w, http.StatusNotFound, "Not Found", "File not available", "NOT_FOUND")
		return
	}

	file, err := os.Open(download.OutputPath)
	if err != nil {
		if os.IsNotExist(err) {
			WriteErrorResponse(w, http.StatusNotFound, "Not Found", "File no longer exists on disk", "NOT_FOUND")
		} else {
			WriteInternalError(w, "Failed to open file")
		}
		return
	}
	defer file.Close()

	fileInfo, err := file.Stat()
	if err != nil {
		WriteInternalError(w, "Failed to get file info")
		return
	}

	downloadFilename := filepath.Base(download.OutputPath)
	// RFC 5987 encoding handles special characters; the plain filename falls
	// back to a quoted ASCII-safe form.
	encodedFilename := url.PathEscape(downloadFilename)
	w.Header().Set("Content-Disposition",
		fmt.Sprintf("attachment; filename=%q; filename*=UTF-8''%s",
			downloadFilename, encodedFilename))
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.FormatInt(fileInfo.Size(), 10))

	if _, err := io.Copy(w, file); err != nil {
		log.Printf("[API] DownloadFile: Failed to stream file: %v", err)
	}
}

// StreamFile serves a completed download for in-browser playback. Unlike
// DownloadFile, it uses http.ServeContent to support HTTP Range requests
// (seeking) and sets an inline media Content-Type.
func (h *Handler) StreamFile(w http.ResponseWriter, r *http.Request) {
	id := GetPathVar(r, "id")
	if id == "" {
		WriteValidationError(w, "Download ID is required")
		return
	}

	download, exists := h.downloadManager.GetDownload(id)
	if !exists {
		WriteErrorResponse(w, http.StatusNotFound, "Not Found", "Download not found", "NOT_FOUND")
		return
	}

	if download.Status != core.StatusCompleted && download.Status != core.StatusAlreadyExists {
		WriteValidationError(w, fmt.Sprintf("Download not completed (status: %s)", download.Status))
		return
	}

	// Defense in depth: the OutputPath is server-generated, but reject any
	// record that resolves outside the configured download directory.
	if !pathWithinDir(download.OutputPath, h.currentConfig().DownloadPath) {
		WriteErrorResponse(w, http.StatusNotFound, "Not Found", "File not available", "NOT_FOUND")
		return
	}

	file, err := os.Open(download.OutputPath)
	if err != nil {
		if os.IsNotExist(err) {
			WriteErrorResponse(w, http.StatusNotFound, "Not Found", "File no longer exists on disk", "NOT_FOUND")
		} else {
			WriteInternalError(w, "Failed to open file")
		}
		return
	}
	defer file.Close()

	fileInfo, err := file.Stat()
	if err != nil {
		WriteInternalError(w, "Failed to get file info")
		return
	}

	w.Header().Set("Content-Type", mediaContentType(download.OutputPath))
	w.Header().Set("Content-Disposition", fmt.Sprintf("inline; filename=%q", filepath.Base(download.OutputPath)))
	http.ServeContent(w, r, filepath.Base(download.OutputPath), fileInfo.ModTime(), file)
}

// mediaContentType maps a file extension to a media MIME type for inline
// streaming. http.ServeContent honors a pre-set Content-Type, so this avoids
// depending on the host's MIME registry, which often lacks media entries.
func mediaContentType(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".mp4", ".m4v":
		return "video/mp4"
	case ".webm":
		return "video/webm"
	case ".mkv":
		return "video/x-matroska"
	case ".mov":
		return "video/quicktime"
	case ".ogv":
		return "video/ogg"
	case ".mp3":
		return "audio/mpeg"
	case ".m4a":
		return "audio/mp4"
	case ".opus", ".ogg":
		return "audio/ogg"
	case ".wav":
		return "audio/wav"
	default:
		return "application/octet-stream"
	}
}

// pathWithinDir reports whether file resolves to a location inside dir. It
// guards against a download record whose OutputPath points outside the
// configured download directory.
func pathWithinDir(file, dir string) bool {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return false
	}
	absFile, err := filepath.Abs(file)
	if err != nil {
		return false
	}
	// Resolve symlinks so a link inside dir that points outside cannot bypass
	// the check. Only swap in the resolved paths when BOTH resolve, keeping the
	// comparison in one namespace; otherwise (e.g. the file does not exist yet)
	// fall back to the lexical absolute paths for a consistent comparison.
	if rDir, errD := filepath.EvalSymlinks(absDir); errD == nil {
		if rFile, errF := filepath.EvalSymlinks(absFile); errF == nil {
			absDir, absFile = rDir, rFile
		}
	}
	rel, err := filepath.Rel(absDir, absFile)
	if err != nil {
		return false
	}
	// Reject "." (the dir itself) and any ".." escape; only strictly-contained
	// paths are within.
	return rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}
