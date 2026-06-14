package api

import (
	"io/fs"
	"net/http"
	"strings"
	"time"
)

// GetPathVar retrieves a path variable from the request. Kept as a wrapper
// over the stdlib router so handlers stay decoupled from the mux.
func GetPathVar(r *http.Request, key string) string {
	return r.PathValue(key)
}

// SetupRoutes wires all API and asset routes onto a stdlib ServeMux using
// Go 1.22+ method and wildcard patterns.
func SetupRoutes(handler *Handler, assetsFS fs.FS) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/health", handler.Health)
	mux.HandleFunc("GET /health", handler.Health)

	mux.HandleFunc("GET /api/config", handler.GetConfig)
	mux.HandleFunc("POST /api/config", handler.UpdateConfig)
	mux.HandleFunc("GET /api/downloads", handler.GetDownloads)
	mux.HandleFunc("POST /api/downloads", handler.StartDownload)
	mux.HandleFunc("POST /api/downloads/playlist", handler.StartPlaylistDownload)
	mux.HandleFunc("POST /api/downloads/first-video", handler.StartFirstVideoDownload)
	mux.HandleFunc("POST /api/downloads/clear-queued", handler.ClearAllQueued)
	mux.HandleFunc("POST /api/downloads/delete-completed", handler.DeleteAllCompleted)
	mux.HandleFunc("POST /api/downloads/clear-failed", handler.ClearAllFailed)
	mux.HandleFunc("DELETE /api/downloads/{id}", handler.DeleteDownload)
	mux.HandleFunc("POST /api/downloads/{id}/cancel", handler.CancelDownload)
	mux.HandleFunc("POST /api/downloads/{id}/pause", handler.PauseDownload)
	mux.HandleFunc("POST /api/downloads/{id}/resume", handler.ResumeDownload)
	mux.HandleFunc("POST /api/downloads/{id}/retry", handler.RetryDownload)
	mux.HandleFunc("POST /api/downloads/{id}/convert", handler.ConvertDownload)
	mux.HandleFunc("GET /api/downloads/{id}/download", handler.DownloadFile)
	mux.HandleFunc("GET /api/downloads/{id}/stream", handler.StreamFile)
	mux.HandleFunc("POST /api/validate", handler.ValidateURL)
	mux.HandleFunc("POST /api/login", handler.Login)
	mux.HandleFunc("POST /api/logout", handler.Logout)
	mux.HandleFunc("GET /api/yt-dlp/version", handler.GetUpdateInfo)
	mux.HandleFunc("POST /api/yt-dlp/update", handler.UpdateYtDlp)
	mux.HandleFunc("GET /api/ffmpeg/check", handler.CheckFfmpeg)
	mux.HandleFunc("GET /api/versions", handler.GetVersions)
	mux.HandleFunc("GET /api/cookies", handler.GetCookies)
	mux.HandleFunc("POST /api/cookies", handler.UploadCookies)
	mux.HandleFunc("DELETE /api/cookies", handler.DeleteCookies)

	// Embedded static assets
	assetsSubFS, err := fs.Sub(assetsFS, "assets")
	if err != nil {
		assetsSubFS = assetsFS
	}
	assetsHandler := http.FileServer(http.FS(assetsSubFS))
	mux.Handle("GET /assets/", http.StripPrefix("/assets/", assetsHandler))

	// Rate limit only /api/ endpoints. Static assets are cheap embedded reads
	// and a single page load fetches many of them (CSS, JS modules, fonts,
	// sw.js, manifest), so limiting assets would trip the limiter on a few
	// rapid refreshes and break page styling. Auth still wraps everything.
	rateLimiter := NewRateLimiter(300, time.Minute)
	limitedMux := rateLimiter.Middleware(mux)

	var apiHandler http.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			limitedMux.ServeHTTP(w, r)
			return
		}
		mux.ServeHTTP(w, r)
	})
	apiHandler = handler.AuthMiddleware(apiHandler)
	apiHandler = PanicRecoveryMiddleware(apiHandler)
	apiHandler = LoggingMiddleware(apiHandler)

	return apiHandler
}
