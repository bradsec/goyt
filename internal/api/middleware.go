package api

import (
	"encoding/json"
	"log"
	"net"
	"net/http"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"goyt/internal/auth"
)

// ErrorResponse represents a structured error response
type ErrorResponse struct {
	Error     string    `json:"error"`
	Message   string    `json:"message,omitempty"`
	Code      string    `json:"code,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

// authExemptPaths are reachable without a valid session so a locked-out user can
// still log in and monitoring can probe health.
var authExemptPaths = map[string]bool{
	"/login":      true,
	"/api/login":  true,
	"/api/logout": true,
	"/health":     true,
	"/api/health": true,
}

// AuthMiddleware enforces a valid session cookie when auth is enabled. When auth
// is disabled it passes every request through unchanged.
func (h *Handler) AuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cfg := h.currentConfig()
		if !cfg.AuthEnabled() || authExemptPaths[r.URL.Path] {
			next.ServeHTTP(w, r)
			return
		}

		if cookie, err := r.Cookie(sessionCookieName); err == nil &&
			auth.Validate(h.sessionSecret, cookie.Value) {
			next.ServeHTTP(w, r)
			return
		}

		if strings.HasPrefix(r.URL.Path, "/api/") {
			WriteErrorResponse(w, http.StatusUnauthorized,
				"Unauthorized", "Authentication required.", "AUTH_REQUIRED")
			return
		}
		http.Redirect(w, r, "/login", http.StatusFound)
	})
}

// PanicRecoveryMiddleware recovers from panics and returns a proper error response
func PanicRecoveryMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				log.Printf("PANIC recovered in %s %s: %v\n%s", r.Method, r.URL.Path, err, debug.Stack())
				WriteErrorResponse(w, http.StatusInternalServerError,
					"Internal Server Error", "An unexpected error occurred. Please try again.", "INTERNAL_ERROR")
			}
		}()

		next.ServeHTTP(w, r)
	})
}

// LoggingMiddleware logs all HTTP requests
func LoggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		wrappedWriter := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}
		next.ServeHTTP(wrappedWriter, r)

		log.Printf("%s %s %d %v %s",
			r.Method,
			r.URL.Path,
			wrappedWriter.statusCode,
			time.Since(start),
			r.RemoteAddr,
		)
	})
}

// RateLimiter is a simple in-memory sliding-window limiter keyed by client IP.
type RateLimiter struct {
	requests map[string][]time.Time
	maxReqs  int
	window   time.Duration
	mu       sync.Mutex
	lastSwep time.Time
}

func NewRateLimiter(maxReqs int, window time.Duration) *RateLimiter {
	return &RateLimiter{
		requests: make(map[string][]time.Time),
		maxReqs:  maxReqs,
		window:   window,
		lastSwep: time.Now(),
	}
}

func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clientIP, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			clientIP = r.RemoteAddr
		}
		now := time.Now()

		rl.mu.Lock()

		// Periodically drop idle clients so the map cannot grow unbounded.
		if now.Sub(rl.lastSwep) > rl.window {
			for ip, timestamps := range rl.requests {
				if len(timestamps) == 0 || now.Sub(timestamps[len(timestamps)-1]) > rl.window {
					delete(rl.requests, ip)
				}
			}
			rl.lastSwep = now
		}

		timestamps := rl.requests[clientIP]
		valid := timestamps[:0]
		for _, ts := range timestamps {
			if now.Sub(ts) <= rl.window {
				valid = append(valid, ts)
			}
		}

		if len(valid) >= rl.maxReqs {
			rl.requests[clientIP] = valid
			rl.mu.Unlock()
			WriteErrorResponse(w, http.StatusTooManyRequests,
				"Rate Limit Exceeded", "Too many requests. Please slow down and try again.", "RATE_LIMIT_EXCEEDED")
			return
		}

		rl.requests[clientIP] = append(valid, now)
		rl.mu.Unlock()

		next.ServeHTTP(w, r)
	})
}

// responseWriter wraps http.ResponseWriter to capture status code
type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

// WriteErrorResponse writes a structured error response
func WriteErrorResponse(w http.ResponseWriter, statusCode int, errorType, message, code string) {
	errorResp := ErrorResponse{
		Error:     errorType,
		Message:   message,
		Code:      code,
		Timestamp: time.Now(),
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	if encodeErr := json.NewEncoder(w).Encode(errorResp); encodeErr != nil {
		log.Printf("Failed to encode error response: %v", encodeErr)
	}
}

// WriteValidationError writes a validation error response
func WriteValidationError(w http.ResponseWriter, message string) {
	WriteErrorResponse(w, http.StatusBadRequest, "Validation Error", message, "VALIDATION_ERROR")
}

// WriteInternalError writes an internal server error response
func WriteInternalError(w http.ResponseWriter, message string) {
	WriteErrorResponse(w, http.StatusInternalServerError, "Internal Server Error", message, "INTERNAL_ERROR")
}

// writeClientError logs the underlying error server-side and returns only a
// generic message to the client, so internal details such as filesystem paths
// or yt-dlp stderr are not exposed to API callers (finding API-6).
func writeClientError(w http.ResponseWriter, where string, err error, publicMessage string) {
	log.Printf("[API] %s: %v", where, err)
	WriteValidationError(w, publicMessage)
}
