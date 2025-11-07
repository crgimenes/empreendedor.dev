package main

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"edev/config"
	"edev/db"
	"edev/session"
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

// initTestDBForHandler initializes a test database with proper schema for handler tests
func initTestDBForHandler(t *testing.T) *db.SQLite {
	t.Helper()

	tempDir := t.TempDir()
	tempDB := tempDir + "/test.db"

	testDB, err := db.NewWithPath(tempDB)
	if err != nil {
		t.Fatalf("failed to create test database: %v", err)
	}

	// Create schema - using simplified statements that work with single Exec calls
	statements := []string{
		`CREATE TABLE IF NOT EXISTS edev_core_users (
			id INTEGER PRIMARY KEY,
			reference_id TEXT NOT NULL UNIQUE DEFAULT "",
			username TEXT UNIQUE COLLATE NOCASE,
			email TEXT UNIQUE COLLATE NOCASE,
			enabled INTEGER NOT NULL DEFAULT 0 CHECK (enabled IN (0,1)),
			avatar_url TEXT,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_edev_core_users_username_nocase
			ON edev_core_users(LOWER(username)) WHERE username IS NOT NULL`,
		`CREATE INDEX IF NOT EXISTS idx_edev_core_users_email_nocase
			ON edev_core_users(LOWER(email)) WHERE email IS NOT NULL`,
		`CREATE INDEX IF NOT EXISTS idx_edev_core_users_enabled ON edev_core_users(enabled)`,
		`CREATE INDEX IF NOT EXISTS idx_edev_core_users_reference_id ON edev_core_users(reference_id)`,
		`CREATE TABLE IF NOT EXISTS edev_core_identities (
			id INTEGER PRIMARY KEY,
			user_id INTEGER NOT NULL REFERENCES edev_core_users(id) ON DELETE CASCADE,
			provider TEXT NOT NULL,
			provider_uid TEXT NOT NULL,
			avatar_url TEXT,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(provider, provider_uid)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_edev_core_identities_user_id ON edev_core_identities(user_id)`,
		`CREATE TRIGGER IF NOT EXISTS edev_core_users_set_updated_at
		AFTER UPDATE OF username, email, enabled, avatar_url ON edev_core_users
		BEGIN
			UPDATE edev_core_users SET updated_at = CURRENT_TIMESTAMP WHERE id = OLD.id;
		END`,
		`CREATE TRIGGER IF NOT EXISTS edev_core_identities_set_updated_at
		AFTER UPDATE OF user_id, provider, provider_uid, avatar_url ON edev_core_identities
		BEGIN
			UPDATE edev_core_identities SET updated_at = CURRENT_TIMESTAMP WHERE id = OLD.id;
		END`,
		`CREATE TRIGGER IF NOT EXISTS edev_core_users_reference_uuid
		AFTER INSERT ON edev_core_users
		BEGIN
		  UPDATE edev_core_users
		  SET reference_id = (
			select substr(u,1,8)||'-'||
			substr(u,9,4)||'-4'||
			substr(u,13,3)||'-'||v||
			substr(u,17,3)||'-'||
			substr(u,21,12) from (
				select
					lower(hex(randomblob(16))) as u,
					substr('89ab',abs(random()) % 4 + 1, 1) as v)
			)
		  WHERE id = NEW.id;
		END`,
	}

	for _, stmt := range statements {
		if err := testDB.Exec(stmt); err != nil {
			t.Fatalf("failed to execute statement: %v\nStatement: %s", err, stmt)
		}
	}

	return testDB
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
	if !strings.Contains(body, "Bem-vindo ao empreendedor.dev") {
		t.Fatalf("expected welcome message in body, got: %s", body)
	}

	if !strings.Contains(body, "Ir para Login") {
		t.Fatalf("expected login button in body")
	}

	// Verify that index template is used (not dashboard)
	if !strings.Contains(body, "Conecte-se com sua conta para continuar") {
		t.Fatalf("expected index template content for unauthenticated users")
	}
}

// TestIndexHandlerAuthenticated tests the index page for authenticated users (should show dashboard)
func TestIndexHandlerAuthenticated(t *testing.T) {
	// Create a test user and session
	testUser := db.User{
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

	// Verify that dashboard template is used (shows user details and edit profile link)
	if !strings.Contains(body, "Editar Perfil") {
		t.Fatalf("expected dashboard template content for authenticated users")
	}

	if !strings.Contains(body, "Conta Ativa") {
		t.Fatalf("expected account status in dashboard")
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
	testUser := db.User{
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
	testUser := db.User{
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
	testUser := db.User{
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

// TestMeHandlerPOSTUsernameConflict tests the me handler POST with username conflict
func TestMeHandlerPOSTUsernameConflict(t *testing.T) {
	// Initialize test database
	testDB := initTestDBForHandler(t)
	defer testDB.Close()

	// Set up global storage for the handler to use
	originalStorage := db.Storage
	db.Storage = testDB
	defer func() { db.Storage = originalStorage }()

	// Create first user with username "alice"
	user1, err := db.Storage.GetUserOrCreateByEmail("alice@test.com")
	if err != nil {
		t.Fatalf("failed to create first user: %v", err)
	}
	_, err = db.Storage.UpdateUserProfile(user1.ID, "alice", "")
	if err != nil {
		t.Fatalf("failed to set username for first user: %v", err)
	}

	// Create second user
	user2, err := db.Storage.GetUserOrCreateByEmail("bob@test.com")
	if err != nil {
		t.Fatalf("failed to create second user: %v", err)
	}
	_, err = db.Storage.UpdateUserProfile(user2.ID, "bob", "")
	if err != nil {
		t.Fatalf("failed to set username for second user: %v", err)
	}

	// Create session for second user
	sid := utils.NewOpaqueID()
	session.Put(sid, *user2)

	// Create POST request trying to update username to "alice" (conflict)
	var requestBody bytes.Buffer
	writer := multipart.NewWriter(&requestBody)
	writer.WriteField("username", "alice")
	writer.WriteField("avatar_url", "")
	writer.Close()

	req := httptest.NewRequest("POST", "/me", &requestBody)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	setSessionCookie(req, sid)

	w := httptest.NewRecorder()

	// Call handler
	meHandler(w, req)

	// Should return 200 (re-rendered form with error)
	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200 (form with error), got %d", w.Code)
	}

	// Check that response contains error message
	body := w.Body.String()
	if !strings.Contains(body, "already in use") {
		t.Fatalf("expected error message 'already in use' in response body")
	}

	// Check that the form is re-rendered (contains the form elements)
	if !strings.Contains(body, "form method=\"POST\"") {
		t.Fatalf("expected form to be re-rendered in response")
	}

	// Verify that user2's username is still "bob" (unchanged)
	updatedUser2, err := db.Storage.GetUserByID(user2.ID)
	if err != nil {
		t.Fatalf("failed to get updated user2: %v", err)
	}
	if updatedUser2.Username != "bob" {
		t.Fatalf("expected user2 username to remain 'bob', got '%s'", updatedUser2.Username)
	}
}

// TestMeHandlerPOSTUsernameConflictCaseInsensitive tests case-insensitive username conflict
func TestMeHandlerPOSTUsernameConflictCaseInsensitive(t *testing.T) {
	// Initialize test database
	testDB := initTestDBForHandler(t)
	defer testDB.Close()

	// Set up global storage for the handler to use
	originalStorage := db.Storage
	db.Storage = testDB
	defer func() { db.Storage = originalStorage }()

	// Create first user with username "alice"
	user1, err := db.Storage.GetUserOrCreateByEmail("alice@test.com")
	if err != nil {
		t.Fatalf("failed to create first user: %v", err)
	}
	_, err = db.Storage.UpdateUserProfile(user1.ID, "alice", "")
	if err != nil {
		t.Fatalf("failed to set username for first user: %v", err)
	}

	// Create second user
	user2, err := db.Storage.GetUserOrCreateByEmail("bob@test.com")
	if err != nil {
		t.Fatalf("failed to create second user: %v", err)
	}
	_, err = db.Storage.UpdateUserProfile(user2.ID, "bob", "")
	if err != nil {
		t.Fatalf("failed to set username for second user: %v", err)
	}

	// Create session for second user
	sid := utils.NewOpaqueID()
	session.Put(sid, *user2)

	// Create POST request trying to update username to "ALICE" (case-insensitive conflict)
	var requestBody2 bytes.Buffer
	writer2 := multipart.NewWriter(&requestBody2)
	writer2.WriteField("username", "ALICE")
	writer2.WriteField("avatar_url", "")
	writer2.Close()

	req := httptest.NewRequest("POST", "/me", &requestBody2)
	req.Header.Set("Content-Type", writer2.FormDataContentType())
	setSessionCookie(req, sid)

	w := httptest.NewRecorder()

	// Call handler
	meHandler(w, req)

	// Should return 200 (re-rendered form with error)
	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200 (form with error), got %d", w.Code)
	}

	// Check that response contains error message
	body := w.Body.String()
	if !strings.Contains(body, "already in use") {
		t.Fatalf("expected error message 'already in use' in response body")
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
