package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"goyt/internal/auth"
	"goyt/internal/config"
)

// Content type constants
const (
	ContentTypeJSON = "application/json"
)

func TestAuthMiddlewareExemptsAssets(t *testing.T) {
	h, _ := newTestHandler(t)
	cfg := h.currentConfig()
	cfg.WebUIPasswordHash = "x" // any non-empty value enables the login gate

	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	srv := h.AuthMiddleware(next)

	cases := map[string]int{
		"/assets/js/login.js": http.StatusOK,
		"/assets/fonts/jetbrains-mono-latin-400-normal.woff2": http.StatusOK,
		"/":           http.StatusFound, // still gated
		"/api/config": http.StatusUnauthorized,
	}
	for path, want := range cases {
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != want {
			t.Errorf("GET %s: got %d, want %d", path, rec.Code, want)
		}
	}
}

func TestUnsafeAPIRequestsRejectCrossOrigin(t *testing.T) {
	t.Setenv("WEBUI_PASSWORD", "")
	h := newAuthTestHandler(t, "")
	router := SetupRoutes(h, fstest.MapFS{})
	tests := []struct {
		name   string
		method string
		origin string
		want   int
	}{
		{"same origin", http.MethodPost, "http://example.com:3000", http.StatusOK},
		{"different port", http.MethodPost, "http://example.com:4000", http.StatusForbidden},
		{"different host", http.MethodPost, "http://other.example.com:3000", http.StatusForbidden},
		{"null origin", http.MethodPost, "null", http.StatusForbidden},
		{"no origin API client", http.MethodPost, "", http.StatusOK},
		{"cross origin read", http.MethodGet, "http://other.example.com:3000", http.StatusMethodNotAllowed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, "http://example.com:3000/api/logout", nil)
			if tt.origin != "" {
				req.Header.Set("Origin", tt.origin)
			}
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != tt.want {
				t.Errorf("status = %d, want %d", rec.Code, tt.want)
			}
		})
	}
}

func TestLoginIsRateLimitedPerClient(t *testing.T) {
	t.Setenv("WEBUI_PASSWORD", "right")
	h := newAuthTestHandler(t, "")
	router := SetupRoutes(h, fstest.MapFS{})
	login := func(addr string) int {
		req := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(`{"password":"wrong"}`))
		req.RemoteAddr = addr
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec.Code
	}
	for i := range loginAttemptsPerMinute {
		if code := login("10.0.0.1:1234"); code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: status = %d, want 401", i+1, code)
		}
	}
	if code := login("10.0.0.1:1234"); code != http.StatusTooManyRequests {
		t.Fatalf("attempt over limit: status = %d, want 429", code)
	}
	if code := login("10.0.0.2:1234"); code != http.StatusUnauthorized {
		t.Fatalf("other client: status = %d, want 401", code)
	}
}

func TestUnsafeAPIRequestsRejectCrossSiteFetch(t *testing.T) {
	t.Setenv("WEBUI_PASSWORD", "")
	router := SetupRoutes(newAuthTestHandler(t, ""), fstest.MapFS{})
	req := httptest.NewRequest(http.MethodPost, "http://example.com:3000/api/logout", nil)
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "CROSS_ORIGIN") {
		t.Errorf("body = %q, want JSON error with CROSS_ORIGIN code", rec.Body.String())
	}
}

func TestResponsesCarrySecurityHeaders(t *testing.T) {
	t.Setenv("WEBUI_PASSWORD", "")
	router := SetupRoutes(newAuthTestHandler(t, ""), fstest.MapFS{})
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	for header, want := range map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Referrer-Policy":        "same-origin",
	} {
		if got := rec.Header().Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
}

func TestPanicRecoveryMiddleware(t *testing.T) {
	// Create a handler that panics
	panicHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("test panic")
	})

	// Wrap with panic recovery middleware
	wrapped := PanicRecoveryMiddleware(panicHandler)

	// Create test request
	req := httptest.NewRequest("GET", "/test", nil)
	recorder := httptest.NewRecorder()

	// Execute request
	wrapped.ServeHTTP(recorder, req)

	// Check response
	if recorder.Code != http.StatusInternalServerError {
		t.Errorf("Expected status 500, got %d", recorder.Code)
	}

	// Check content type
	if recorder.Header().Get("Content-Type") != ContentTypeJSON {
		t.Error("Expected JSON content type")
	}

	// Parse response
	var errorResp ErrorResponse
	err := json.NewDecoder(recorder.Body).Decode(&errorResp)
	if err != nil {
		t.Fatalf("Failed to decode error response: %v", err)
	}

	if errorResp.Error != "Internal Server Error" {
		t.Errorf("Expected 'Internal Server Error', got '%s'", errorResp.Error)
	}

	if errorResp.Code != "INTERNAL_ERROR" {
		t.Errorf("Expected 'INTERNAL_ERROR', got '%s'", errorResp.Code)
	}
}

func TestLoggingMiddleware(t *testing.T) {
	// Create a simple handler
	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	// Wrap with logging middleware
	wrapped := LoggingMiddleware(testHandler)

	// Create test request
	req := httptest.NewRequest("GET", "/test", nil)
	recorder := httptest.NewRecorder()

	// Execute request
	wrapped.ServeHTTP(recorder, req)

	// Check response
	if recorder.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", recorder.Code)
	}

	if recorder.Body.String() != "OK" {
		t.Errorf("Expected 'OK', got '%s'", recorder.Body.String())
	}
}

func TestRateLimiter(t *testing.T) {
	rateLimiter := NewRateLimiter(2, time.Minute) // 2 requests per minute

	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	wrapped := rateLimiter.Middleware(testHandler)

	// First two requests should succeed
	for i := range 2 {
		req := httptest.NewRequest("GET", "/test", nil)
		req.RemoteAddr = "127.0.0.1:1234"
		recorder := httptest.NewRecorder()

		wrapped.ServeHTTP(recorder, req)

		if recorder.Code != http.StatusOK {
			t.Errorf("Request %d should succeed, got status %d", i+1, recorder.Code)
		}
	}

	// Third request should be rate limited
	req := httptest.NewRequest("GET", "/test", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	recorder := httptest.NewRecorder()

	wrapped.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusTooManyRequests {
		t.Errorf("Expected rate limit status 429, got %d", recorder.Code)
	}

	// Check response is JSON error
	if recorder.Header().Get("Content-Type") != ContentTypeJSON {
		t.Error("Expected JSON content type for rate limit response")
	}
}

func TestWriteErrorResponse(t *testing.T) {
	recorder := httptest.NewRecorder()

	WriteErrorResponse(recorder, http.StatusBadRequest, "Test Error", "Test message", "TEST_CODE")

	if recorder.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400, got %d", recorder.Code)
	}

	if recorder.Header().Get("Content-Type") != ContentTypeJSON {
		t.Error("Expected JSON content type")
	}

	var errorResp ErrorResponse
	err := json.NewDecoder(recorder.Body).Decode(&errorResp)
	if err != nil {
		t.Fatalf("Failed to decode error response: %v", err)
	}

	if errorResp.Error != "Test Error" {
		t.Errorf("Expected 'Test Error', got '%s'", errorResp.Error)
	}

	if errorResp.Message != "Test message" {
		t.Errorf("Expected 'Test message', got '%s'", errorResp.Message)
	}

	if errorResp.Code != "TEST_CODE" {
		t.Errorf("Expected 'TEST_CODE', got '%s'", errorResp.Code)
	}
}

func TestRateLimiterPerIP(t *testing.T) {
	rateLimiter := NewRateLimiter(1, time.Minute)
	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	wrapped := rateLimiter.Middleware(testHandler)

	// Same IP from different source ports shares one bucket.
	for i, addr := range []string{"10.0.0.1:1111", "10.0.0.1:2222"} {
		req := httptest.NewRequest("GET", "/test", nil)
		req.RemoteAddr = addr
		recorder := httptest.NewRecorder()
		wrapped.ServeHTTP(recorder, req)
		if i == 0 && recorder.Code != http.StatusOK {
			t.Errorf("First request should succeed, got %d", recorder.Code)
		}
		if i == 1 && recorder.Code != http.StatusTooManyRequests {
			t.Errorf("Second request from same IP should be limited, got %d", recorder.Code)
		}
	}

	// A different IP gets its own bucket.
	req := httptest.NewRequest("GET", "/test", nil)
	req.RemoteAddr = "10.0.0.2:1111"
	recorder := httptest.NewRecorder()
	wrapped.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Errorf("Different IP should not be limited, got %d", recorder.Code)
	}
}

func TestWriteValidationError(t *testing.T) {
	recorder := httptest.NewRecorder()

	WriteValidationError(recorder, "Validation failed")

	if recorder.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400, got %d", recorder.Code)
	}

	var errorResp ErrorResponse
	err := json.NewDecoder(recorder.Body).Decode(&errorResp)
	if err != nil {
		t.Fatalf("Failed to decode error response: %v", err)
	}

	if errorResp.Error != "Validation Error" {
		t.Errorf("Expected 'Validation Error', got '%s'", errorResp.Error)
	}

	if errorResp.Code != "VALIDATION_ERROR" {
		t.Errorf("Expected 'VALIDATION_ERROR', got '%s'", errorResp.Code)
	}
}

func TestWriteInternalError(t *testing.T) {
	recorder := httptest.NewRecorder()

	WriteInternalError(recorder, "Something went wrong")

	if recorder.Code != http.StatusInternalServerError {
		t.Errorf("Expected status 500, got %d", recorder.Code)
	}

	var errorResp ErrorResponse
	err := json.NewDecoder(recorder.Body).Decode(&errorResp)
	if err != nil {
		t.Fatalf("Failed to decode error response: %v", err)
	}

	if errorResp.Error != "Internal Server Error" {
		t.Errorf("Expected 'Internal Server Error', got '%s'", errorResp.Error)
	}

	if errorResp.Code != "INTERNAL_ERROR" {
		t.Errorf("Expected 'INTERNAL_ERROR', got '%s'", errorResp.Code)
	}
}

func newAuthTestHandler(t *testing.T, hash string) *Handler {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.WebUIPasswordHash = hash
	cfg.SessionSecret = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=" // 32 zero bytes
	return NewHandler(cfg, "config.json", nil, nil)
}

func TestAuthMiddlewareDisabledPassthrough(t *testing.T) {
	t.Setenv("WEBUI_PASSWORD", "")
	h := newAuthTestHandler(t, "")
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true })
	req := httptest.NewRequest(http.MethodGet, "/api/downloads", nil)
	rec := httptest.NewRecorder()
	h.AuthMiddleware(next).ServeHTTP(rec, req)
	if !called {
		t.Error("expected passthrough when auth disabled")
	}
}

func TestAuthMiddlewareAPIUnauthorized(t *testing.T) {
	t.Setenv("WEBUI_PASSWORD", "")
	hash, _ := auth.Hash("pw")
	h := newAuthTestHandler(t, hash)
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("next should not run") })
	req := httptest.NewRequest(http.MethodGet, "/api/downloads", nil)
	rec := httptest.NewRecorder()
	h.AuthMiddleware(next).ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestAuthMiddlewareNonAPIRedirect(t *testing.T) {
	t.Setenv("WEBUI_PASSWORD", "")
	hash, _ := auth.Hash("pw")
	h := newAuthTestHandler(t, hash)
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("next should not run") })
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h.AuthMiddleware(next).ServeHTTP(rec, req)
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/login" {
		t.Errorf("expected 302 -> /login, got %d %q", rec.Code, rec.Header().Get("Location"))
	}
}

func TestAuthMiddlewareValidCookiePasses(t *testing.T) {
	t.Setenv("WEBUI_PASSWORD", "")
	hash, _ := auth.Hash("pw")
	h := newAuthTestHandler(t, hash)
	tok, _ := auth.Issue(h.sessionSecret, time.Hour)
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true })
	req := httptest.NewRequest(http.MethodGet, "/api/downloads", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: tok})
	rec := httptest.NewRecorder()
	h.AuthMiddleware(next).ServeHTTP(rec, req)
	if !called {
		t.Error("expected next to run with valid cookie")
	}
}

func TestAuthMiddlewareExemptPaths(t *testing.T) {
	t.Setenv("WEBUI_PASSWORD", "")
	hash, _ := auth.Hash("pw")
	h := newAuthTestHandler(t, hash)
	for _, p := range []string{"/login", "/api/login", "/api/logout", "/health", "/api/health"} {
		called := false
		next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true })
		req := httptest.NewRequest(http.MethodGet, p, nil)
		rec := httptest.NewRecorder()
		h.AuthMiddleware(next).ServeHTTP(rec, req)
		if !called {
			t.Errorf("exempt path %q should pass through", p)
		}
	}
}

func TestLoginLogout(t *testing.T) {
	t.Setenv("WEBUI_PASSWORD", "")
	hash, _ := auth.Hash("hunter2")
	h := newAuthTestHandler(t, hash)

	req := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(`{"password":"nope"}`))
	rec := httptest.NewRecorder()
	h.Login(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for wrong password, got %d", rec.Code)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(`{"password":"hunter2"}`))
	rec = httptest.NewRecorder()
	h.Login(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for correct password, got %d", rec.Code)
	}
	var session *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookieName {
			session = c
		}
	}
	if session == nil || !auth.Validate(h.sessionSecret, session.Value) {
		t.Fatal("expected a valid session cookie after login")
	}

	req = httptest.NewRequest(http.MethodPost, "/api/logout", nil)
	rec = httptest.NewRecorder()
	h.Logout(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for logout, got %d", rec.Code)
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookieName && c.MaxAge >= 0 {
			t.Error("logout should expire the session cookie")
		}
	}
}

func TestGetConfigOmitsSecrets(t *testing.T) {
	t.Setenv("WEBUI_PASSWORD", "")
	hash, _ := auth.Hash("pw")
	h := newAuthTestHandler(t, hash)
	req := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	rec := httptest.NewRecorder()
	h.GetConfig(rec, req)
	body := rec.Body.String()
	if strings.Contains(body, hash) || strings.Contains(body, "session_secret") || strings.Contains(body, "AAAAAAAA") {
		t.Errorf("GetConfig response leaked a secret: %s", body)
	}
	if !strings.Contains(body, `"auth_enabled":true`) {
		t.Errorf("expected auth_enabled true in body: %s", body)
	}
}

func TestUpdateConfigOmitsSecretsAndPreservesThem(t *testing.T) {
	t.Setenv("WEBUI_PASSWORD", "")
	hash, _ := auth.Hash("pw")
	// Use a temp config path so the save does not touch the repo's config.json.
	cfg := config.DefaultConfig()
	cfg.WebUIPasswordHash = hash
	cfg.SessionSecret = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=" // 32 zero bytes
	h := NewHandler(cfg, filepath.Join(t.TempDir(), "config.json"), nil, nil)

	// A valid settings payload that does NOT include the secret fields.
	body := `{"download_path":"./downloads","max_concurrent_downloads":3,"port":3000,"default_video_format":"mp4","default_audio_format":"mp3","default_video_quality":"1080p","playlist_load_timeout_seconds":180,"download_start_timeout_seconds":60}`
	req := httptest.NewRequest(http.MethodPost, "/api/config", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.UpdateConfig(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	respBody := rec.Body.String()
	if strings.Contains(respBody, hash) || strings.Contains(respBody, "AAAAAAAA") {
		t.Errorf("UpdateConfig response leaked a secret: %s", respBody)
	}
	// Secret must be preserved in the live config (not wiped by the save).
	if h.currentConfig().WebUIPasswordHash != hash {
		t.Error("UpdateConfig wiped the password hash")
	}
}
