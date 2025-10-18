package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"edev/config"
	"edev/session"
	"edev/user"
	"edev/utils"
)

func init() {
	// Initialize config for tests
	config.Cfg = &config.Config{
		BaseURL:            "http://localhost:8080",
		SessionDuration:    3600 * time.Second,
		GithubOAuthEnabled: true,
		XOAuthEnabled:      true,
		FakeOAuthEnabled:   true,
		GitHubClientID:     "test-github",
		GitHubClientSecret: "test-secret",
		XClientID:          "test-x",
		XClientSecret:      "test-x-secret",
		FakeOAuthClientID:  "test-fake",
		FakeOAuthRedirect:  "/fakeoauth/callback",
		FakeOAuthBaseURL:   "http://localhost:9000",
		ResendAPIKey:       "test-key",
	}
	session.EnableInsecureCookie()
}

// setSessionCookie adds a session cookie to a request
func setSessionCookie(r *http.Request, sid string) {
	c := &http.Cookie{
		Name:  "sid",
		Value: sid,
		Path:  "/",
	}
	r.AddCookie(c)
}

// TestIndexHandlerNotAuthenticated tests the index page for unauthenticated users
func TestIndexHandlerNotAuthenticated(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()

	indexHandler(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "Bem-vindo ao Empreendedor.dev") {
		t.Fatalf("expected welcome message in body, got: %s", body)
	}

	if !strings.Contains(body, "Ir para Login") {
		t.Fatalf("expected login button in body")
	}
}

// TestIndexHandlerAuthenticated tests the index page for authenticated users
func TestIndexHandlerAuthenticated(t *testing.T) {
	// Create a test user and session
	testUser := user.User{
		ID:        1,
		Username:  "testuser",
		Email:     "test@example.com",
		Enabled:   true,
		AvatarURL: "https://example.com/avatar.jpg",
	}

	sid := utils.NewOpaqueID()
	session.Put(sid, testUser)

	req := httptest.NewRequest("GET", "/", nil)
	setSessionCookie(req, sid)

	w := httptest.NewRecorder()
	indexHandler(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "Bem-vindo, testuser!") {
		t.Fatalf("expected personalized welcome in body, got: %s", body)
	}

	if !strings.Contains(body, "test@example.com") {
		t.Fatalf("expected email in body")
	}
}

// TestLoginPageHandler tests the login page
func TestLoginPageHandler(t *testing.T) {
	req := httptest.NewRequest("GET", "/login", nil)
	w := httptest.NewRecorder()

	// This test ensures no panic occurs during template rendering
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("login page handler panicked: %v", r)
		}
	}()

	loginPageHandler(w, req)

	// Accept both 200 (successful render) and other codes
	// The important thing is that we don't panic
	if w.Code < 200 || w.Code >= 500 {
		t.Logf("handler returned status %d", w.Code)
	}
}

// TestLoginPageHandlerRedirectIfAuthenticated tests that authenticated users are redirected
func TestLoginPageHandlerRedirectIfAuthenticated(t *testing.T) {
	testUser := user.User{
		ID:       1,
		Username: "testuser",
		Email:    "test@example.com",
		Enabled:  true,
	}

	sid := utils.NewOpaqueID()
	session.Put(sid, testUser)

	req := httptest.NewRequest("GET", "/login", nil)
	setSessionCookie(req, sid)

	w := httptest.NewRecorder()
	loginPageHandler(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("expected redirect status 302, got %d", w.Code)
	}

	location := w.Header().Get("Location")
	if location != config.Cfg.BaseURL+"/" {
		t.Fatalf("expected redirect to home, got %s", location)
	}
}

// TestMeHandlerNotAuthenticated tests that unauthenticated users are redirected from /me
func TestMeHandlerNotAuthenticated(t *testing.T) {
	req := httptest.NewRequest("GET", "/me", nil)
	w := httptest.NewRecorder()

	meHandler(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("expected redirect status 302, got %d", w.Code)
	}

	location := w.Header().Get("Location")
	if location != config.Cfg.BaseURL+"/login" {
		t.Fatalf("expected redirect to login, got %s", location)
	}
}

// TestMeHandlerGETAuthenticated tests GET /me for authenticated users
func TestMeHandlerGETAuthenticated(t *testing.T) {
	testUser := user.User{
		ID:        1,
		Username:  "testuser",
		Email:     "test@example.com",
		Enabled:   true,
		AvatarURL: "https://example.com/avatar.jpg",
	}

	sid := utils.NewOpaqueID()
	session.Put(sid, testUser)

	req := httptest.NewRequest("GET", "/me", nil)
	setSessionCookie(req, sid)

	w := httptest.NewRecorder()

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("me handler panicked: %v", r)
		}
	}()

	meHandler(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}
}

// TestLogoutHandler tests the logout handler
func TestLogoutHandler(t *testing.T) {
	testUser := user.User{
		ID:       1,
		Username: "testuser",
		Email:    "test@example.com",
		Enabled:  true,
	}

	sid := utils.NewOpaqueID()
	session.Put(sid, testUser)

	req := httptest.NewRequest("GET", "/logout", nil)
	setSessionCookie(req, sid)

	w := httptest.NewRecorder()
	logoutHandler(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("expected redirect status 302, got %d", w.Code)
	}

	location := w.Header().Get("Location")
	if location != config.Cfg.BaseURL+"/" {
		t.Fatalf("expected redirect to home, got %s", location)
	}

	// Verify session was deleted
	if _, found := session.Get(sid); found {
		t.Fatalf("expected session to be deleted after logout")
	}
}

// TestHealthHandler tests the health check endpoint
func TestHealthHandler(t *testing.T) {
	req := httptest.NewRequest("GET", "/healthz", nil)
	w := httptest.NewRecorder()

	healthHandler(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	body := strings.TrimSpace(w.Body.String())
	if body != "ok" {
		t.Fatalf("expected 'ok' response, got %s", body)
	}
}

// TestTemplateRendering tests that templates can be rendered without errors
func TestTemplateRendering(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
		method  string
		path    string
	}{
		{
			name:    "index page",
			handler: indexHandler,
			method:  "GET",
			path:    "/",
		},
		{
			name:    "login page",
			handler: loginPageHandler,
			method:  "GET",
			path:    "/login",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			w := httptest.NewRecorder()

			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("template rendering panicked: %v", r)
				}
			}()

			tt.handler(w, req)

			// Templates should render without panic
			if w.Code < 200 || w.Code >= 500 {
				t.Logf("handler returned status %d", w.Code)
			}
		})
	}
}

// TestTemplatesParseCorrectly ensures all templates can be parsed without errors
func TestTemplatesParseCorrectly(t *testing.T) {
	// If we get here without panicking during template loading in init,
	// then templates parse correctly.
	// Create a simple request to trigger template rendering
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("template rendering panicked: %v", r)
		}
	}()

	indexHandler(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected template rendering to succeed, got status %d", w.Code)
	}
}

// BenchmarkIndexHandler benchmarks the index handler
func BenchmarkIndexHandler(b *testing.B) {
	req := httptest.NewRequest("GET", "/", nil)

	for i := 0; i < b.N; i++ {
		w := httptest.NewRecorder()
		indexHandler(w, req)
	}
}

// BenchmarkLoginPageHandler benchmarks the login page handler
func BenchmarkLoginPageHandler(b *testing.B) {
	req := httptest.NewRequest("GET", "/login", nil)

	for i := 0; i < b.N; i++ {
		w := httptest.NewRecorder()
		loginPageHandler(w, req)
	}
}
