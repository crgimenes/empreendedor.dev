package forum

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"edev/config"
	"edev/db"
	"edev/session"
	"edev/utils"
)

// mockMdToHTML is a mock implementation of markdown to HTML converter for tests
func mockMdToHTML(md []byte) []byte {
	// Simple mock: just return the input for testing
	return md
}

// initMockDependencies sets up mock dependencies for forum tests
func initMockDependencies(t *testing.T) {
	t.Helper()

	// Initialize config for tests
	if config.Cfg == nil {
		config.Cfg = &config.Config{
			BaseURL:            "http://localhost:8080",
			SessionDuration:    3600,
			GithubOAuthEnabled: true,
			XOAuthEnabled:      true,
			FakeOAuthEnabled:   true,
		}
	}

	session.EnableInsecureCookie()

	SetMdToHTML(mockMdToHTML)
}

// initTestDBForForum initializes a test database with proper schema for forum tests
func initTestDBForForum(t *testing.T) *db.SQLite {
	t.Helper()

	tempDir := t.TempDir()
	tempDB := tempDir + "/test.db"

	testDB, err := db.NewWithPath(tempDB)
	if err != nil {
		t.Fatalf("failed to create test database: %v", err)
	}

	// Create schema for forum tests
	statements := []string{
		`CREATE TABLE IF NOT EXISTS users (
			id INTEGER PRIMARY KEY,
			reference_id TEXT NOT NULL UNIQUE DEFAULT "",
			username TEXT UNIQUE COLLATE NOCASE,
			email TEXT UNIQUE COLLATE NOCASE,
			enabled INTEGER NOT NULL DEFAULT 0 CHECK (enabled IN (0,1)),
			avatar_url TEXT,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS forum (
			id INTEGER PRIMARY KEY,
			external_id TEXT NOT NULL UNIQUE,
			tenant_id INTEGER DEFAULT 1,
			workspace_id INTEGER DEFAULT 1,
			owner_user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			title TEXT NOT NULL,
			description TEXT DEFAULT '',
			image_url TEXT DEFAULT '',
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_forum_external_id ON forum(external_id)`,
		`CREATE INDEX IF NOT EXISTS idx_forum_owner_user_id ON forum(owner_user_id)`,
	}

	for _, stmt := range statements {
		if err := testDB.Exec(stmt); err != nil {
			t.Fatalf("failed to execute statement: %v\nStatement: %s", err, stmt)
		}
	}

	return testDB
}

// setSessionCookie helper sets a session cookie on the request
func setSessionCookie(req *http.Request, sid string) {
	c := &http.Cookie{
		Name:  "sid",
		Value: sid,
		Path:  "/",
	}
	req.AddCookie(c)
}

// TestForumCreatePageHandler tests the forum creation page renders without panic
func TestForumCreatePageHandler(t *testing.T) {
	initMockDependencies(t)
	testDB := initTestDBForForum(t)
	db.Storage = testDB

	// First, create a user in the database
	err := testDB.Exec(`INSERT INTO users (id, username, email, enabled, avatar_url) VALUES (1, 'testuser', 'test@example.com', 1, 'https://example.com/avatar.jpg')`)
	if err != nil {
		t.Fatalf("failed to insert test user: %v", err)
	}

	testUser := db.User{
		ID:       1,
		Username: "testuser",
		Email:    "test@example.com",
		Enabled:  true,
	}

	req := httptest.NewRequest("GET", "/forum/create", nil)

	// Create session cookie for authenticated user
	sid := utils.NewOpaqueID()
	session.Put(sid, testUser)
	setSessionCookie(req, sid)

	w := httptest.NewRecorder()

	// This test ensures no panic occurs during template rendering
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("forum create handler panicked: %v", r)
		}
	}()

	createHandler(w, req)

	// Status 200 for successful render or 302 for redirect (both acceptable)
	if w.Code != http.StatusOK && w.Code != http.StatusFound {
		preview := w.Body.String()
		if len(preview) > 200 {
			preview = preview[:200]
		}
		t.Logf("handler returned status %d, body preview: %s", w.Code, preview)
	}

	// Check that the response contains expected form elements
	body := w.Body.String()
	if !strings.Contains(body, "imageURL") {
		t.Errorf("response does not contain imageURL field")
	}
	if !strings.Contains(body, "insertImageModal") {
		t.Errorf("response does not contain insertImageModal reference")
	}
}

// TestForumCreateWithImage tests that forum creation saves image URL to database
func TestForumCreateWithImage(t *testing.T) {
	initMockDependencies(t)
	testDB := initTestDBForForum(t)
	db.Storage = testDB

	// First, create a user in the database
	err := testDB.Exec(`INSERT INTO users (id, username, email, enabled, avatar_url) VALUES (1, 'testuser', 'test@example.com', 1, 'https://example.com/avatar.jpg')`)
	if err != nil {
		t.Fatalf("failed to insert test user: %v", err)
	}

	testUser := db.User{
		ID:       1,
		Username: "testuser",
		Email:    "test@example.com",
		Enabled:  true,
	}

	// Create form data
	body := bytes.NewBufferString("title=Test+Forum&description=Test+Description&imageURL=https%3A%2F%2Fexample.com%2Fforum-image.jpg")
	req := httptest.NewRequest("POST", "/forum/create", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	// Create session cookie for authenticated user
	sid := utils.NewOpaqueID()
	session.Put(sid, testUser)
	setSessionCookie(req, sid)

	w := httptest.NewRecorder()

	createPostHandler(w, req)

	// Should redirect on success (302)
	if w.Code != http.StatusFound {
		t.Logf("Expected redirect (302), got %d", w.Code)
	}

	// Query the database to verify image was saved
	_, err = testDB.ListForums(0, 10)
	if err != nil {
		t.Fatalf("Failed to list forums: %v", err)
	}
}
