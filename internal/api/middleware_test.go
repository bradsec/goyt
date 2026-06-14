package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"goyt/internal/auth"
	"goyt/internal/config"
)

// Content type constants
const (
	ContentTypeJSON = "application/json"
)

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
	for i := 0; i < 2; i++ {
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
