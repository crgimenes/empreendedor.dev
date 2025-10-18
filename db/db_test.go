package db

import (
	"database/sql"
	"edev/utils"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// helper: unwrap rows/error
func mustRows(t *testing.T, rows *sql.Rows, err error) *sql.Rows {
	t.Helper()
	if err != nil {
		t.Fatalf("query error: %v", err)
	}
	return rows
}

// helper: scan single string (avoids rows.Err after Scan to handle PRAGMA context-canceled behavior)
func mustQuerySingleString(t *testing.T, rows *sql.Rows) string {
	t.Helper()
	defer utils.Closer(rows)

	if !rows.Next() {
		// If no row is available, check the iterator error now.
		if err := rows.Err(); err != nil {
			t.Fatalf("rows err (no row): %v", err)
		}
		t.Fatalf("no rows returned")
	}
	var s string
	if err := rows.Scan(&s); err != nil {
		t.Fatalf("scan error: %v", err)
	}
	// Avoid rows.Err() after Scan to prevent "context canceled" for single-row PRAGMA results.
	return s
}

// helper: scan single int64 (same pattern as the string helper)
func mustQuerySingleInt64(t *testing.T, rows *sql.Rows) int64 {
	t.Helper()
	defer utils.Closer(rows)

	if !rows.Next() {
		if err := rows.Err(); err != nil {
			t.Fatalf("rows err (no row): %v", err)
		}
		t.Fatalf("no rows returned")
	}
	var n int64
	if err := rows.Scan(&n); err != nil {
		t.Fatalf("scan error: %v", err)
	}
	return n
}

func TestNewAndPragmas(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	path := filepath.Join(tmp, "test.db")

	s, err := NewWithPath(path)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	defer s.Close()

	// PRAGMA journal_mode
	r1, e1 := s.QueryRW(`PRAGMA journal_mode`)
	mode := mustQuerySingleString(t, mustRows(t, r1, e1))
	if mode != "wal" {
		t.Fatalf("expected WAL, got %q", mode)
	}

	// PRAGMA synchronous (NORMAL = 1)
	r2, e2 := s.QueryRW(`PRAGMA synchronous`)
	sync := mustQuerySingleInt64(t, mustRows(t, r2, e2))
	if sync != 1 {
		t.Fatalf("expected synchronous=NORMAL(1), got %d", sync)
	}

	// PRAGMA foreign_keys (1 = ON)
	r3, e3 := s.QueryRW(`PRAGMA foreign_keys`)
	fk := mustQuerySingleInt64(t, mustRows(t, r3, e3))
	if fk != 1 {
		t.Fatalf("expected foreign_keys=ON, got %d", fk)
	}

	// PRAGMA busy_timeout (ms) - defaultBusyTimeout = 15s
	r4, e4 := s.QueryRW(`PRAGMA busy_timeout`)
	bt := mustQuerySingleInt64(t, mustRows(t, r4, e4))
	if bt != 15000 {
		t.Fatalf("expected busy_timeout=15000, got %d", bt)
	}
}

func TestExecAndQuery(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	path := filepath.Join(tmp, "test.db")

	s, err := NewWithPath(path)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	defer s.Close()

	// Create table and insert rows via Exec (RW).
	if err := s.Exec(`CREATE TABLE IF NOT EXISTS items(id INTEGER PRIMARY KEY, name TEXT NOT NULL)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	for i := 0; i < 3; i++ {
		if err := s.Exec(`INSERT INTO items(name) VALUES(?)`, fmt.Sprintf("n%d", i)); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	// Query (RO): count and targeted lookup.
	rows, err := s.Query(`SELECT COUNT(*) FROM items`)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	defer utils.Closer(rows)
	if !rows.Next() {
		t.Fatalf("count: no rows")
	}
	var count int
	if err := rows.Scan(&count); err != nil {
		t.Fatalf("count scan: %v", err)
	}
	if count != 3 {
		t.Fatalf("expected count=3, got %d", count)
	}

	var name string
	if err := s.QueryRow(`SELECT name FROM items WHERE id = 2`).Scan(&name); err != nil {
		t.Fatalf("queryrow: %v", err)
	}
	if name != "n1" {
		t.Fatalf("expected name=n1, got %s", name)
	}
}

func TestTransactionCommitRollback(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	path := filepath.Join(tmp, "test.db")

	s, err := NewWithPath(path)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	defer s.Close()

	if err := s.Exec(`CREATE TABLE IF NOT EXISTS kv(k TEXT PRIMARY KEY, v TEXT NOT NULL)`); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Commit flow
	tx, err := s.BeginTransaction()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := tx.Exec(`INSERT INTO kv(k,v) VALUES(?,?)`, "a", "1"); err != nil {
		t.Fatalf("insert a: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	var v string
	if err := s.QueryRow(`SELECT v FROM kv WHERE k=?`, "a").Scan(&v); err != nil {
		t.Fatalf("select a: %v", err)
	}
	if v != "1" {
		t.Fatalf("expected v=1, got %s", v)
	}

	// Rollback flow
	tx2, err := s.BeginTransaction()
	if err != nil {
		t.Fatalf("begin2: %v", err)
	}
	if err := tx2.Exec(`INSERT INTO kv(k,v) VALUES(?,?)`, "b", "2"); err != nil {
		t.Fatalf("insert b: %v", err)
	}
	if err := tx2.Rollback(); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	var v2 string
	err = s.QueryRow(`SELECT v FROM kv WHERE k=?`, "b").Scan(&v2)
	if err == nil {
		t.Fatalf("expected no row for k=b after rollback, got v=%s", v2)
	}
}

func TestCheckpointAndClose(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	path := filepath.Join(tmp, "test.db")

	s, err := NewWithPath(path)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	// Generate WAL write activity.
	if err := s.Exec(`CREATE TABLE IF NOT EXISTS t(x)`); err != nil {
		t.Fatalf("create: %v", err)
	}
	for i := 0; i < 100; i++ {
		if err := s.Exec(`INSERT INTO t(x) VALUES(?)`, i); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	// Explicit checkpoint.
	if err := s.CheckpointWAL(); err != nil {
		t.Fatalf("checkpoint: %v", err)
	}

	// Close should not panic; performs a best-effort checkpoint.
	s.Close()
}

func TestConcurrentReadersSingleWriter(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	path := filepath.Join(tmp, "test.db")

	s, err := NewWithPath(path)
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	defer s.Close()

	if err := s.Exec(`CREATE TABLE IF NOT EXISTS c(n INTEGER)`); err != nil {
		t.Fatalf("create: %v", err)
	}

	// One writer loop (RW) plus many readers (RO) running in parallel.
	var wg sync.WaitGroup
	stop := make(chan struct{})

	// Single writer
	wg.Go(func() {
		ticker := time.NewTicker(5 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				_ = s.Exec(`INSERT INTO c(n) VALUES(strftime('%s','now'))`)
			}
		}
	})

	// Multiple readers
	readers := max(runtime.GOMAXPROCS(0), 4)
	var readErr atomicError
	for i := 0; i < readers; i++ {
		wg.Go(func() {
			deadline := time.Now().Add(150 * time.Millisecond)
			for time.Now().Before(deadline) && readErr.Load() == nil {
				var cnt int
				if err := s.QueryRow(`SELECT COUNT(*) FROM c`).Scan(&cnt); err != nil {
					readErr.Store(err)
					return
				}
				time.Sleep(2 * time.Millisecond)
			}
		})
	}

	time.Sleep(200 * time.Millisecond)
	close(stop)
	wg.Wait()

	if err := readErr.Load(); err != nil {
		t.Fatalf("reader error: %v", err)
	}
}

// atomicError: minimal helper to avoid extra deps.
type atomicError struct {
	mu sync.Mutex
	e  error
}

func (a *atomicError) Store(err error) {
	a.mu.Lock()
	a.e = err
	a.mu.Unlock()
}
func (a *atomicError) Load() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.e
}

// initTestDB initializes an in-memory SQLite database with the schema.
// It returns the SQLite instance or fails the test.
func initTestDB(t *testing.T) *SQLite {
	t.Helper()

	// Use temp file instead of :memory: to ensure RW and RO pools share the same database.
	// If we use ":memory:" directly, the RW and RO pools will have separate in-memory databases.
	tmp := t.TempDir()
	path := filepath.Join(tmp, "test.db")

	s, err := NewWithPath(path)
	if err != nil {
		t.Fatalf("NewWithPath(%q): %v", path, err)
	}

	// Create schema from 001_base_system.up.sql
	// Execute each CREATE statement separately because s.Exec only handles one statement at a time.
	statements := []string{
		`CREATE TABLE IF NOT EXISTS users (
    id INTEGER PRIMARY KEY,
    username TEXT,
    email TEXT,
    enabled INTEGER NOT NULL DEFAULT 0 CHECK (enabled IN (0,1)),
    avatar_url TEXT,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
)`,
		`CREATE TABLE IF NOT EXISTS identities (
    id INTEGER PRIMARY KEY,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    provider TEXT NOT NULL,
    provider_uid TEXT NOT NULL,
    avatar_url TEXT,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(provider, provider_uid)
)`,
		`CREATE INDEX IF NOT EXISTS idx_identities_user_id ON identities(user_id)`,
		`CREATE TABLE IF NOT EXISTS magic_token (
    id INTEGER PRIMARY KEY,
    email TEXT NOT NULL,
    token TEXT NOT NULL UNIQUE,
    action TEXT NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at DATETIME NOT NULL DEFAULT (DATETIME('now', '+3 hour'))
)`,
	}

	for _, stmt := range statements {
		if err := s.Exec(stmt); err != nil {
			t.Fatalf("schema exec: %v", err)
		}
	}

	return s
}

func TestGetUserByOAuthProviderID(t *testing.T) {
	t.Parallel()

	s := initTestDB(t)
	defer s.Close()

	// Insert a test user and identity.
	if err := s.Exec(`
		INSERT INTO users (username, email, enabled) VALUES (?, ?, ?)
	`, "testuser", "test@example.com", 1); err != nil {
		t.Fatalf("insert user: %v", err)
	}

	if err := s.Exec(`
		INSERT INTO identities (user_id, provider, provider_uid) VALUES (?, ?, ?)
	`, 1, "github", "gh_12345"); err != nil {
		t.Fatalf("insert identity: %v", err)
	}

	tests := []struct {
		name       string
		provider   string
		providerID string
		wantUserID int64
		wantErr    bool
	}{
		{
			name:       "existing oauth identity",
			provider:   "github",
			providerID: "gh_12345",
			wantUserID: 1,
			wantErr:    false,
		},
		{
			name:       "non-existing provider",
			provider:   "twitter",
			providerID: "tw_12345",
			wantUserID: 0,
			wantErr:    false,
		},
		{
			name:       "non-existing provider id",
			provider:   "github",
			providerID: "gh_99999",
			wantUserID: 0,
			wantErr:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			userID, err := s.GetUserByOAuthProviderID(tt.provider, tt.providerID)
			if (err != nil) != tt.wantErr {
				t.Fatalf("GetUserByOAuthProviderID() error = %v, wantErr %v", err, tt.wantErr)
			}
			if userID != tt.wantUserID {
				t.Fatalf("GetUserByOAuthProviderID() = %d, want %d", userID, tt.wantUserID)
			}
		})
	}
}

func TestStoreMagicLinkToken(t *testing.T) {
	t.Parallel()

	s := initTestDB(t)
	defer s.Close()

	tests := []struct {
		name      string
		token     string
		email     string
		expiresAt time.Time
		wantErr   bool
	}{
		{
			name:      "valid token",
			token:     "tok_abc123",
			email:     "user@example.com",
			expiresAt: time.Now().UTC().Add(3 * time.Hour),
			wantErr:   false,
		},
		{
			name:      "another valid token",
			token:     "tok_xyz789",
			email:     "another@example.com",
			expiresAt: time.Now().UTC().Add(1 * time.Hour),
			wantErr:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := s.StoreMagicLinkToken(tt.token, tt.email, tt.expiresAt)
			if (err != nil) != tt.wantErr {
				t.Fatalf("StoreMagicLinkToken() error = %v, wantErr %v", err, tt.wantErr)
			}

			if !tt.wantErr {
				// Verify token was stored
				var storedEmail string
				err := s.QueryRow(`SELECT email FROM magic_token WHERE token = ?`, tt.token).Scan(&storedEmail)
				if err != nil {
					t.Fatalf("verify token: %v", err)
				}
				if storedEmail != tt.email {
					t.Fatalf("stored email = %q, want %q", storedEmail, tt.email)
				}
			}
		})
	}
}

func TestConsumeMagicLinkToken(t *testing.T) {
	t.Parallel()

	s := initTestDB(t)
	defer s.Close()

	token := "tok_consume123"
	email := "consumer@example.com"
	expiresAt := time.Now().UTC().Add(3 * time.Hour)

	// Store a token
	if err := s.StoreMagicLinkToken(token, email, expiresAt); err != nil {
		t.Fatalf("store token: %v", err)
	}

	tests := []struct {
		name      string
		token     string
		wantEmail string
		wantErr   bool
	}{
		{
			name:      "consume existing valid token",
			token:     token,
			wantEmail: email,
			wantErr:   false,
		},
		{
			name:      "consume already consumed token",
			token:     token,
			wantEmail: "",
			wantErr:   false,
		},
		{
			name:      "consume non-existing token",
			token:     "tok_nonexist",
			wantEmail: "",
			wantErr:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotEmail, err := s.ConsumeMagicLinkToken(tt.token)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ConsumeMagicLinkToken() error = %v, wantErr %v", err, tt.wantErr)
			}
			if gotEmail != tt.wantEmail {
				t.Fatalf("ConsumeMagicLinkToken() = %q, want %q", gotEmail, tt.wantEmail)
			}
		})
	}
}

func TestPurgeExpiredMagicLinkTokens(t *testing.T) {
	t.Parallel()

	s := initTestDB(t)
	defer s.Close()

	// Insert an expired token
	if err := s.Exec(`
		INSERT INTO magic_token (email, token, action, expires_at)
		VALUES (?, ?, ?, ?)
	`, "expired@example.com", "tok_expired", "login", time.Now().Add(-1*time.Hour)); err != nil {
		t.Fatalf("insert expired token: %v", err)
	}

	// Insert a valid (future) token
	if err := s.Exec(`
		INSERT INTO magic_token (email, token, action, expires_at)
		VALUES (?, ?, ?, ?)
	`, "valid@example.com", "tok_valid", "login", time.Now().Add(3*time.Hour)); err != nil {
		t.Fatalf("insert valid token: %v", err)
	}

	// Purge expired tokens
	if err := s.PurgeExpiredMagicLinkTokens(); err != nil {
		t.Fatalf("PurgeExpiredMagicLinkTokens() error = %v", err)
	}

	// Verify expired token is gone
	var expiredCount int
	if err := s.QueryRow(`SELECT COUNT(*) FROM magic_token WHERE token = ?`, "tok_expired").Scan(&expiredCount); err != nil {
		t.Fatalf("count expired: %v", err)
	}
	if expiredCount != 0 {
		t.Fatalf("expected 0 expired tokens, got %d", expiredCount)
	}

	// Verify valid token still exists
	var validCount int
	if err := s.QueryRow(`SELECT COUNT(*) FROM magic_token WHERE token = ?`, "tok_valid").Scan(&validCount); err != nil {
		t.Fatalf("count valid: %v", err)
	}
	if validCount != 1 {
		t.Fatalf("expected 1 valid token, got %d", validCount)
	}
}

func TestGetUserByID(t *testing.T) {
	t.Parallel()

	s := initTestDB(t)
	defer s.Close()

	// Insert test user
	if err := s.Exec(`
		INSERT INTO users (username, email, enabled, avatar_url)
		VALUES (?, ?, ?, ?)
	`, "johndoe", "john@example.com", 1, "https://avatar.example.com/john.jpg"); err != nil {
		t.Fatalf("insert user: %v", err)
	}

	tests := []struct {
		name      string
		userID    int64
		wantUser  bool
		wantError bool
	}{
		{
			name:      "existing user",
			userID:    1,
			wantUser:  true,
			wantError: false,
		},
		{
			name:      "non-existing user",
			userID:    999,
			wantUser:  false,
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u, err := s.GetUserByID(tt.userID)
			if (err != nil) != tt.wantError {
				t.Fatalf("GetUserByID() error = %v, wantError %v", err, tt.wantError)
			}
			if tt.wantUser && u == nil {
				t.Fatalf("GetUserByID() returned nil user")
			}
			if tt.wantUser && u.Username != "johndoe" {
				t.Fatalf("GetUserByID() username = %q, want %q", u.Username, "johndoe")
			}
		})
	}
}

func TestGetUserOrCreateByEmail(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		email     string
		isCreate  bool
		wantError bool
	}{
		{
			name:      "create new user by email",
			email:     "newuser@example.com",
			isCreate:  true,
			wantError: false,
		},
		{
			name:      "get existing user by email",
			email:     "newuser@example.com",
			isCreate:  false,
			wantError: false,
		},
		{
			name:      "create second user",
			email:     "seconduser@example.com",
			isCreate:  true,
			wantError: false,
		},
	}

	createdID := int64(0)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := initTestDB(t)
			defer s.Close()

			u, err := s.GetUserOrCreateByEmail(tt.email)
			if (err != nil) != tt.wantError {
				t.Fatalf("GetUserOrCreateByEmail() error = %v, wantError %v", err, tt.wantError)
			}
			if u == nil {
				t.Fatalf("GetUserOrCreateByEmail() returned nil user")
			}

			if tt.isCreate {
				createdID = u.ID
				if u.ID == 0 {
					t.Fatalf("GetUserOrCreateByEmail() created user with ID 0")
				}
			} else {
				if u.ID != createdID {
					t.Fatalf("GetUserOrCreateByEmail() returned different ID: got %d, want %d", u.ID, createdID)
				}
			}
		})
	}
}

func TestGetUserOrCreateByOAuth(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		provider   string
		providerID string
		email      string
		username   string
		avatarURL  string
		isCreate   bool
		wantError  bool
	}{
		{
			name:       "create new user from oauth",
			provider:   "github",
			providerID: "gh_user_123",
			email:      "github@example.com",
			username:   "githubuser",
			avatarURL:  "https://avatar.example.com/github.jpg",
			isCreate:   true,
			wantError:  false,
		},
		{
			name:       "get existing user by oauth",
			provider:   "github",
			providerID: "gh_user_123",
			email:      "github@example.com",
			username:   "githubuser",
			avatarURL:  "https://avatar.example.com/github.jpg",
			isCreate:   false,
			wantError:  false,
		},
		{
			name:       "create user from twitter",
			provider:   "twitter",
			providerID: "tw_user_456",
			email:      "twitter@example.com",
			username:   "twitteruser",
			avatarURL:  "https://avatar.example.com/twitter.jpg",
			isCreate:   true,
			wantError:  false,
		},
	}

	createdID := int64(0)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := initTestDB(t)
			defer s.Close()

			u, err := s.GetUserOrCreateByOAuth(tt.provider, tt.providerID, tt.email, tt.username, tt.avatarURL)
			if (err != nil) != tt.wantError {
				t.Fatalf("GetUserOrCreateByOAuth() error = %v, wantError %v", err, tt.wantError)
			}
			if u == nil {
				t.Fatalf("GetUserOrCreateByOAuth() returned nil user")
			}

			if tt.isCreate {
				createdID = u.ID
				if u.ID == 0 {
					t.Fatalf("GetUserOrCreateByOAuth() created user with ID 0")
				}
				if u.Username != tt.username {
					t.Fatalf("GetUserOrCreateByOAuth() username = %q, want %q", u.Username, tt.username)
				}

				// Verify identity was created
				var identityCount int
				if err := s.QueryRow(`
					SELECT COUNT(*) FROM identities
					WHERE user_id = ? AND provider = ? AND provider_uid = ?
				`, u.ID, tt.provider, tt.providerID).Scan(&identityCount); err != nil {
					t.Fatalf("count identities: %v", err)
				}
				if identityCount != 1 {
					t.Fatalf("expected 1 identity, got %d", identityCount)
				}
			} else {
				if u.ID != createdID {
					t.Fatalf("GetUserOrCreateByOAuth() returned different ID: got %d, want %d", u.ID, createdID)
				}
			}
		})
	}
}

func TestTransactionQueryRow(t *testing.T) {
	t.Parallel()

	s := initTestDB(t)
	defer s.Close()

	// Create test table
	if err := s.Exec(`CREATE TABLE IF NOT EXISTS test_data(id INTEGER PRIMARY KEY, value TEXT)`); err != nil {
		t.Fatalf("create table: %v", err)
	}

	// Begin transaction and insert
	tx, err := s.BeginTransaction()
	if err != nil {
		t.Fatalf("BeginTransaction() error = %v", err)
	}

	if err := tx.Exec(`INSERT INTO test_data(value) VALUES(?)`, "test_value"); err != nil {
		t.Fatalf("tx.Exec() error = %v", err)
		_ = tx.Rollback()
	}

	// Query within transaction
	var value string
	if err := tx.QueryRow(`SELECT value FROM test_data WHERE id = 1`).Scan(&value); err != nil {
		t.Fatalf("tx.QueryRow().Scan() error = %v", err)
		_ = tx.Rollback()
	}

	if value != "test_value" {
		t.Fatalf("tx.QueryRow() value = %q, want %q", value, "test_value")
	}

	if err := tx.Commit(); err != nil {
		t.Fatalf("tx.Commit() error = %v", err)
	}

	// Verify data persisted
	var persistedValue string
	if err := s.QueryRow(`SELECT value FROM test_data WHERE id = 1`).Scan(&persistedValue); err != nil {
		t.Fatalf("verify persisted: %v", err)
	}
	if persistedValue != "test_value" {
		t.Fatalf("persisted value = %q, want %q", persistedValue, "test_value")
	}
}

func TestTransactionQuery(t *testing.T) {
	t.Parallel()

	s := initTestDB(t)
	defer s.Close()

	// Create test table
	if err := s.Exec(`CREATE TABLE IF NOT EXISTS numbers(id INTEGER PRIMARY KEY, num INTEGER)`); err != nil {
		t.Fatalf("create table: %v", err)
	}

	// Begin transaction and insert multiple rows
	tx, err := s.BeginTransaction()
	if err != nil {
		t.Fatalf("BeginTransaction() error = %v", err)
	}

	for i := 1; i <= 3; i++ {
		if err := tx.Exec(`INSERT INTO numbers(num) VALUES(?)`, i*10); err != nil {
			_ = tx.Rollback()
			t.Fatalf("tx.Exec() error = %v", err)
		}
	}

	// Query rows within transaction
	rows, err := tx.Query(`SELECT num FROM numbers ORDER BY num`)
	if err != nil {
		_ = tx.Rollback()
		t.Fatalf("tx.Query() error = %v", err)
	}
	defer utils.Closer(rows)

	var nums []int
	for rows.Next() {
		var num int
		if err := rows.Scan(&num); err != nil {
			_ = tx.Rollback()
			t.Fatalf("rows.Scan() error = %v", err)
		}
		nums = append(nums, num)
	}

	if err := tx.Commit(); err != nil {
		t.Fatalf("tx.Commit() error = %v", err)
	}

	expected := []int{10, 20, 30}
	if len(nums) != len(expected) {
		t.Fatalf("query results count = %d, want %d", len(nums), len(expected))
	}
	for i, num := range nums {
		if num != expected[i] {
			t.Fatalf("query result[%d] = %d, want %d", i, num, expected[i])
		}
	}
}

// TestStoreMagicLinkTokenComprehensive tests StoreMagicLinkToken with various scenarios.
func TestStoreMagicLinkTokenComprehensive(t *testing.T) {
	tests := []struct {
		name      string
		email     string
		token     string
		expiresAt time.Time
		wantErr   bool
	}{
		{
			name:      "valid token",
			email:     "user@example.com",
			token:     "valid-token-123",
			expiresAt: time.Now().UTC().Add(3 * time.Hour),
			wantErr:   false,
		},
		{
			name:      "expired token (past)",
			email:     "user@example.com",
			token:     "expired-token-456",
			expiresAt: time.Now().UTC().Add(-1 * time.Hour),
			wantErr:   false,
		},
		{
			name:      "empty email",
			email:     "",
			token:     "token-empty-789",
			expiresAt: time.Now().UTC().Add(3 * time.Hour),
			wantErr:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := initTestDB(t)
			defer s.Close()

			err := s.StoreMagicLinkToken(tt.token, tt.email, tt.expiresAt)
			if (err != nil) != tt.wantErr {
				t.Fatalf("StoreMagicLinkToken() error = %v, wantErr %v", err, tt.wantErr)
			}

			if !tt.wantErr {
				// Verify token was stored
				var storedEmail string
				const query = `SELECT email FROM magic_token WHERE token = ?`
				err := s.QueryRow(query, tt.token).Scan(&storedEmail)
				if err != nil {
					t.Fatalf("failed to verify stored token: %v", err)
				}
				if storedEmail != tt.email {
					t.Fatalf("stored email = %q, want %q", storedEmail, tt.email)
				}
			}
		})
	}
}

// TestConsumeMagicLinkTokenComprehensive tests ConsumeMagicLinkToken scenarios.
func TestConsumeMagicLinkTokenComprehensive(t *testing.T) {
	tests := []struct {
		name      string
		setupFunc func(*testing.T, *SQLite)
		token     string
		wantEmail string
		wantErr   bool
	}{
		{
			name: "valid token",
			setupFunc: func(t *testing.T, s *SQLite) {
				if err := s.StoreMagicLinkToken(
					"valid-token",
					"user@example.com",
					time.Now().Add(3*time.Hour),
				); err != nil {
					t.Fatalf("setup: %v", err)
				}
			},
			token:     "valid-token",
			wantEmail: "user@example.com",
			wantErr:   false,
		},
		{
			name: "expired token",
			setupFunc: func(t *testing.T, s *SQLite) {
				if err := s.StoreMagicLinkToken(
					"expired-token",
					"user@example.com",
					time.Now().Add(-1*time.Hour),
				); err != nil {
					t.Fatalf("setup: %v", err)
				}
			},
			token:     "expired-token",
			wantEmail: "",
			wantErr:   false,
		},
		{
			name:      "non-existent token",
			setupFunc: func(t *testing.T, s *SQLite) {},
			token:     "non-existent-token",
			wantEmail: "",
			wantErr:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := initTestDB(t)
			defer s.Close()

			tt.setupFunc(t, s)

			email, err := s.ConsumeMagicLinkToken(tt.token)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ConsumeMagicLinkToken() error = %v, wantErr %v", err, tt.wantErr)
			}
			if email != tt.wantEmail {
				t.Fatalf("ConsumeMagicLinkToken() email = %q, want %q", email, tt.wantEmail)
			}

			// Verify token was deleted only if it was successfully consumed
			if email != "" {
				var count int
				const countQuery = `SELECT COUNT(*) FROM magic_token WHERE token = ?`
				if err := s.QueryRow(countQuery, tt.token).Scan(&count); err != nil {
					t.Fatalf("failed to count token: %v", err)
				}
				if count > 0 {
					t.Fatalf("token not deleted: count = %d", count)
				}
			}
		})
	}
}

// TestPurgeExpiredMagicLinkTokensComprehensive tests PurgeExpiredMagicLinkTokens.
func TestPurgeExpiredMagicLinkTokensComprehensive(t *testing.T) {
	s := initTestDB(t)
	defer s.Close()

	// Insert expired token
	if err := s.StoreMagicLinkToken(
		"expired-1",
		"expired@example.com",
		time.Now().Add(-2*time.Hour),
	); err != nil {
		t.Fatalf("setup: %v", err)
	}

	// Insert valid token
	if err := s.StoreMagicLinkToken(
		"valid-1",
		"valid@example.com",
		time.Now().Add(3*time.Hour),
	); err != nil {
		t.Fatalf("setup: %v", err)
	}

	// Insert another expired token
	if err := s.StoreMagicLinkToken(
		"expired-2",
		"expired2@example.com",
		time.Now().Add(-30*time.Minute),
	); err != nil {
		t.Fatalf("setup: %v", err)
	}

	// Purge expired tokens
	if err := s.PurgeExpiredMagicLinkTokens(); err != nil {
		t.Fatalf("PurgeExpiredMagicLinkTokens() error = %v", err)
	}

	// Verify expired tokens are gone
	var count int
	const countExpired = `SELECT COUNT(*) FROM magic_token WHERE expires_at <= CURRENT_TIMESTAMP`
	if err := s.QueryRow(countExpired).Scan(&count); err != nil {
		t.Fatalf("failed to count expired: %v", err)
	}
	if count != 0 {
		t.Fatalf("expired tokens not purged: count = %d", count)
	}

	// Verify valid token still exists
	var validExists int
	const countValid = `SELECT COUNT(*) FROM magic_token WHERE token = ?`
	if err := s.QueryRow(countValid, "valid-1").Scan(&validExists); err != nil {
		t.Fatalf("failed to verify valid token: %v", err)
	}
	if validExists != 1 {
		t.Fatalf("valid token was purged or duplicated: count = %d", validExists)
	}
}

// TestGetUserByOAuthProviderIDComprehensive tests GetUserByOAuthProviderID.
func TestGetUserByOAuthProviderIDComprehensive(t *testing.T) {
	tests := []struct {
		name       string
		setupFunc  func(*testing.T, *SQLite)
		provider   string
		providerID string
		wantUserID int64
		wantErr    bool
	}{
		{
			name: "existing oauth identity",
			setupFunc: func(t *testing.T, s *SQLite) {
				// Create a user
				const userSQL = `INSERT INTO users(email, username, avatar_url) VALUES(?, ?, ?)
				RETURNING id`
				var userID int64
				if err := s.QueryRowRW(userSQL, "oauth@example.com", "oauthuser", "").Scan(&userID); err != nil {
					t.Fatalf("setup user: %v", err)
				}

				// Create identity
				const identitySQL = `INSERT INTO identities(user_id, provider, provider_uid)
				VALUES(?, ?, ?)`
				if err := s.Exec(identitySQL, userID, "github", "gh-123"); err != nil {
					t.Fatalf("setup identity: %v", err)
				}
			},
			provider:   "github",
			providerID: "gh-123",
			wantUserID: 1,
			wantErr:    false,
		},
		{
			name:       "non-existent provider",
			setupFunc:  func(t *testing.T, s *SQLite) {},
			provider:   "twitter",
			providerID: "tw-456",
			wantUserID: 0,
			wantErr:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := initTestDB(t)
			defer s.Close()

			tt.setupFunc(t, s)

			userID, err := s.GetUserByOAuthProviderID(tt.provider, tt.providerID)
			if (err != nil) != tt.wantErr {
				t.Fatalf("GetUserByOAuthProviderID() error = %v, wantErr %v", err, tt.wantErr)
			}
			if userID != tt.wantUserID {
				t.Fatalf("GetUserByOAuthProviderID() userID = %d, want %d", userID, tt.wantUserID)
			}
		})
	}
}

// TestGetUserByIDComprehensive tests GetUserByID.
func TestGetUserByIDComprehensive(t *testing.T) {
	tests := []struct {
		name      string
		setupFunc func(*testing.T, *SQLite) int64
		userID    int64
		wantUser  bool
		wantErr   bool
	}{
		{
			name: "existing user",
			setupFunc: func(t *testing.T, s *SQLite) int64 {
				const sql = `INSERT INTO users(email, username, avatar_url, enabled)
				VALUES(?, ?, ?, ?) RETURNING id`
				var id int64
				if err := s.QueryRowRW(sql,
					"user@example.com",
					"testuser",
					"https://example.com/avatar.jpg",
					1,
				).Scan(&id); err != nil {
					t.Fatalf("setup: %v", err)
				}
				return id
			},
			wantUser: true,
			wantErr:  false,
		},
		{
			name: "non-existent user",
			setupFunc: func(t *testing.T, s *SQLite) int64 {
				return 9999
			},
			wantUser: false,
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := initTestDB(t)
			defer s.Close()

			userID := tt.setupFunc(t, s)

			user, err := s.GetUserByID(userID)
			if (err != nil) != tt.wantErr {
				t.Fatalf("GetUserByID() error = %v, wantErr %v", err, tt.wantErr)
			}

			if tt.wantUser && user != nil {
				if user.ID != userID {
					t.Fatalf("user.ID = %d, want %d", user.ID, userID)
				}
				if user.Email == "" {
					t.Fatalf("user.Email is empty")
				}
			}
		})
	}
}

// TestGetUserOrCreateByEmailComprehensive tests GetUserOrCreateByEmail.
func TestGetUserOrCreateByEmailComprehensive(t *testing.T) {
	tests := []struct {
		name      string
		setupFunc func(*testing.T, *SQLite)
		email     string
		wantNew   bool
		wantErr   bool
	}{
		{
			name:      "create new user",
			setupFunc: func(t *testing.T, s *SQLite) {},
			email:     "newuser@example.com",
			wantNew:   true,
			wantErr:   false,
		},
		{
			name: "get existing user",
			setupFunc: func(t *testing.T, s *SQLite) {
				const sql = `INSERT INTO users(email, username, avatar_url) VALUES(?, ?, ?)`
				if err := s.Exec(sql, "existing@example.com", "existinguser", ""); err != nil {
					t.Fatalf("setup: %v", err)
				}
			},
			email:   "existing@example.com",
			wantNew: false,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := initTestDB(t)
			defer s.Close()

			tt.setupFunc(t, s)

			user, err := s.GetUserOrCreateByEmail(tt.email)
			if (err != nil) != tt.wantErr {
				t.Fatalf("GetUserOrCreateByEmail() error = %v, wantErr %v", err, tt.wantErr)
			}

			if user != nil {
				if user.ID == 0 {
					t.Fatalf("user.ID is zero")
				}
				if user.Email == "" {
					t.Fatalf("user.Email is empty")
				}
			}
		})
	}
}

// TestGetUserOrCreateByOAuthComprehensive tests GetUserOrCreateByOAuth.
func TestGetUserOrCreateByOAuthComprehensive(t *testing.T) {
	tests := []struct {
		name       string
		setupFunc  func(*testing.T, *SQLite)
		provider   string
		providerID string
		email      string
		username   string
		avatarURL  string
		wantErr    bool
	}{
		{
			name:       "create new oauth user",
			setupFunc:  func(t *testing.T, s *SQLite) {},
			provider:   "github",
			providerID: "gh-999",
			email:      "newgithub@example.com",
			username:   "newgithubuser",
			avatarURL:  "https://example.com/avatar.jpg",
			wantErr:    false,
		},
		{
			name: "get existing oauth user",
			setupFunc: func(t *testing.T, s *SQLite) {
				// Create existing user with identity
				const userSQL = `INSERT INTO users(email, username, avatar_url) VALUES(?, ?, ?)
				RETURNING id`
				var userID int64
				if err := s.QueryRowRW(userSQL, "existing@github.com", "existinggithub", "").Scan(&userID); err != nil {
					t.Fatalf("setup user: %v", err)
				}

				const identitySQL = `INSERT INTO identities(user_id, provider, provider_uid)
				VALUES(?, ?, ?)`
				if err := s.Exec(identitySQL, userID, "github", "gh-existing"); err != nil {
					t.Fatalf("setup identity: %v", err)
				}
			},
			provider:   "github",
			providerID: "gh-existing",
			email:      "different@example.com",
			username:   "different",
			avatarURL:  "https://example.com/avatar2.jpg",
			wantErr:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := initTestDB(t)
			defer s.Close()

			tt.setupFunc(t, s)

			user, err := s.GetUserOrCreateByOAuth(
				tt.provider,
				tt.providerID,
				tt.email,
				tt.username,
				tt.avatarURL,
			)
			if (err != nil) != tt.wantErr {
				t.Fatalf("GetUserOrCreateByOAuth() error = %v, wantErr %v", err, tt.wantErr)
			}

			if user != nil {
				if user.ID == 0 {
					t.Fatalf("user.ID is zero")
				}

				// Verify identity was created
				var identityCount int
				const countSQL = `SELECT COUNT(*) FROM identities
				WHERE provider = ? AND provider_uid = ?`
				if err := s.QueryRow(countSQL, tt.provider, tt.providerID).Scan(&identityCount); err != nil {
					t.Fatalf("failed to count identities: %v", err)
				}
				if identityCount != 1 {
					t.Fatalf("expected 1 identity, got %d", identityCount)
				}
			}
		})
	}
}

// TestStoreMagicLinkTokenDuplicateToken tests unique constraint on token.
func TestStoreMagicLinkTokenDuplicateToken(t *testing.T) {
	s := initTestDB(t)
	defer s.Close()

	token := "duplicate-token"
	email1 := "user1@example.com"
	email2 := "user2@example.com"

	// Store first token
	if err := s.StoreMagicLinkToken(token, email1, time.Now().UTC().Add(3*time.Hour)); err != nil {
		t.Fatalf("first insert: %v", err)
	}

	// Try to store same token with different email
	err := s.StoreMagicLinkToken(token, email2, time.Now().UTC().Add(3*time.Hour))
	if err == nil {
		t.Fatalf("expected error for duplicate token, got nil")
	}
}

// TestMagicLinkTokenRoundTrip tests complete flow: store and consume.
func TestMagicLinkTokenRoundTrip(t *testing.T) {
	s := initTestDB(t)
	defer s.Close()

	email := "roundtrip@example.com"
	token := "roundtrip-token-123"
	expiresAt := time.Now().Add(3 * time.Hour)

	// Store token
	if err := s.StoreMagicLinkToken(token, email, expiresAt); err != nil {
		t.Fatalf("StoreMagicLinkToken(): %v", err)
	}

	// Consume token
	retrievedEmail, err := s.ConsumeMagicLinkToken(token)
	if err != nil {
		t.Fatalf("ConsumeMagicLinkToken(): %v", err)
	}

	if retrievedEmail != email {
		t.Fatalf("email mismatch: got %q, want %q", retrievedEmail, email)
	}

	// Try to consume again (should be gone)
	retrievedEmail, err = s.ConsumeMagicLinkToken(token)
	if err != nil {
		t.Fatalf("ConsumeMagicLinkToken() second call: %v", err)
	}

	if retrievedEmail != "" {
		t.Fatalf("token should be consumed, got email %q", retrievedEmail)
	}
}

// TestUpdateUserProfile tests profile update with username and avatar_url.
func TestUpdateUserProfile(t *testing.T) {
	s := initTestDB(t)
	defer s.Close()

	// Create a user with email via magic link flow
	email := "profile@example.com"
	token := "profile-token-123"
	_ = s.StoreMagicLinkToken(token, email, time.Now().UTC().Add(3*time.Hour))
	consumedEmail, _ := s.ConsumeMagicLinkToken(token)

	u, err := s.GetUserOrCreateByEmail(consumedEmail)
	if err != nil {
		t.Fatalf("GetUserOrCreateByEmail(): %v", err)
	}

	if u.Username != "" {
		t.Fatalf("new user should have empty username, got %q", u.Username)
	}

	tests := []struct {
		name       string
		username   string
		avatar     string
		wantErr    bool
		wantEnable bool
	}{
		{
			name:       "valid update",
			username:   "newuser",
			avatar:     "https://example.com/avatar.jpg",
			wantErr:    false,
			wantEnable: true,
		},
		{
			name:       "empty username",
			username:   "  ",
			avatar:     "",
			wantErr:    true,
			wantEnable: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			updatedUser, err := s.UpdateUserProfile(u.ID, tt.username, tt.avatar)
			if (err != nil) != tt.wantErr {
				t.Fatalf("UpdateUserProfile() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}

			if updatedUser.Username != tt.username {
				t.Fatalf("username = %q, want %q", updatedUser.Username, tt.username)
			}
			if updatedUser.AvatarURL != tt.avatar {
				t.Fatalf("avatar_url = %q, want %q", updatedUser.AvatarURL, tt.avatar)
			}
			if updatedUser.Enabled != tt.wantEnable {
				t.Fatalf("enabled = %v, want %v", updatedUser.Enabled, tt.wantEnable)
			}
		})
	}
}

// TestUpdateUserProfileDuplicateUsername tests that updating with duplicate
// username (case-insensitive) fails.
func TestUpdateUserProfileDuplicateUsername(t *testing.T) {
	s := initTestDB(t)
	defer s.Close()

	// Create first user
	u1, _ := s.GetUserOrCreateByEmail("user1@example.com")
	s.UpdateUserProfile(u1.ID, "alice", "")

	// Create second user
	u2, _ := s.GetUserOrCreateByEmail("user2@example.com")

	// Try to update second user with duplicate username (different case)
	_, err := s.UpdateUserProfile(u2.ID, "ALICE", "")
	if err == nil {
		t.Fatalf("UpdateUserProfile() should reject duplicate username, got nil error")
	}
	if !strings.Contains(err.Error(), "already in use") {
		t.Fatalf("expected 'already in use' error, got %v", err)
	}
}

// TestEnableUserByEmailValidation tests enabling user after email validation.
func TestEnableUserByEmailValidation(t *testing.T) {
	s := initTestDB(t)
	defer s.Close()

	// Create user via OAuth without username
	u, _ := s.GetUserOrCreateByOAuth(
		"github", "12345", "oauth@example.com", "", "")

	if u.Enabled {
		t.Fatalf("new OAuth user should not be enabled, got enabled=true")
	}

	// Update profile to add username
	u, _ = s.UpdateUserProfile(u.ID, "oauthuser", "")

	if !u.Enabled {
		t.Fatalf("after UpdateUserProfile with username, user should be enabled")
	}

	// Test direct EnableUserByEmailValidation
	u2, _ := s.GetUserOrCreateByEmail("direct@example.com")
	u2, _ = s.UpdateUserProfile(u2.ID, "directuser", "")

	// Should be enabled now
	if !u2.Enabled {
		t.Fatalf("user should be enabled after profile update")
	}
}

// TestOAuthEmailConflictResolution tests that OAuth respects existing users registered via magic link.
func TestOAuthEmailConflictResolution(t *testing.T) {
	t.Parallel()
	s := initTestDB(t)
	defer s.Close()

	// Step 1: User signs up via magic link
	email := "shared@example.com"
	magicLinkUser, err := s.GetUserOrCreateByEmail(email)
	if err != nil {
		t.Fatalf("GetUserOrCreateByEmail failed: %v", err)
	}
	if magicLinkUser.ID == 0 {
		t.Fatalf("GetUserOrCreateByEmail returned user with ID 0")
	}

	// Step 2: User tries to login via OAuth with same email but different username
	oauthUsername := "oauth_user"
	oauthUser, err := s.GetUserOrCreateByOAuth("github", "gh-12345", email, oauthUsername, "")
	if err != nil {
		t.Fatalf("GetUserOrCreateByOAuth failed: %v", err)
	}

	// Step 3: Verify same user (same ID)
	if oauthUser.ID != magicLinkUser.ID {
		t.Fatalf("Expected same user ID, got magic_link=%d, oauth=%d", magicLinkUser.ID, oauthUser.ID)
	}

	// Step 4: Verify GitHub identity was added to existing user
	var identityCount int
	const countSQL = `SELECT COUNT(*) FROM identities WHERE user_id = ? AND provider = ? AND provider_uid = ?`
	if err := s.QueryRow(countSQL, magicLinkUser.ID, "github", "gh-12345").Scan(&identityCount); err != nil {
		t.Fatalf("count identities: %v", err)
	}
	if identityCount != 1 {
		t.Fatalf("expected 1 identity, got %d", identityCount)
	}

	// Step 5: Verify username was updated (if magic link user had no username)
	if magicLinkUser.Username == "" {
		updated, err := s.GetUserByID(magicLinkUser.ID)
		if err != nil {
			t.Fatalf("GetUserByID failed: %v", err)
		}
		if updated.Username != oauthUsername {
			t.Fatalf("expected username %q, got %q", oauthUsername, updated.Username)
		}
	}
}

// TestOAuthUsernameConflictGeneration tests that OAuth generates unique usernames when conflict exists.
func TestOAuthUsernameConflictGeneration(t *testing.T) {
	t.Parallel()
	s := initTestDB(t)
	defer s.Close()

	// Step 1: Create first user with username "cesar"
	email1 := "cesar@example.com"
	_, err := s.GetUserOrCreateByOAuth("github", "gh-cesar1", email1, "cesar", "")
	if err != nil {
		t.Fatalf("first user creation failed: %v", err)
	}

	// Step 2: Try to create another user with same username but different email/provider
	email2 := "newuser@example.com"
	user2, err := s.GetUserOrCreateByOAuth("github", "gh-cesar2", email2, "cesar", "")
	if err != nil {
		t.Fatalf("second user creation failed: %v", err)
	}

	// Step 3: Verify second user got a numbered username
	if user2.Username == "cesar" {
		t.Fatalf("expected username to be modified for conflict, got 'cesar'")
	}

	// Step 4: Verify both users exist and have different usernames
	var user1ID int64
	const checkSQL = `SELECT id FROM users WHERE LOWER(email) = LOWER(?)`
	if err := s.QueryRow(checkSQL, email1).Scan(&user1ID); err != nil {
		t.Fatalf("query user1: %v", err)
	}

	user1, err := s.GetUserByID(user1ID)
	if err != nil {
		t.Fatalf("GetUserByID failed: %v", err)
	}

	if user1.Username == user2.Username {
		t.Fatalf("expected different usernames, both got %q", user1.Username)
	}
}

// TestCountUsersWithUsernamePrefix tests the prefix counting function.
func TestCountUsersWithUsernamePrefix(t *testing.T) {
	t.Parallel()
	s := initTestDB(t)
	defer s.Close()

	// Create some users with matching prefixes
	s.Exec(`INSERT INTO users(email, username) VALUES(?, ?)`, "user1@example.com", "cesar")
	s.Exec(`INSERT INTO users(email, username) VALUES(?, ?)`, "user2@example.com", "cesar1")
	s.Exec(`INSERT INTO users(email, username) VALUES(?, ?)`, "user3@example.com", "cesar2")
	s.Exec(`INSERT INTO users(email, username) VALUES(?, ?)`, "user4@example.com", "other")

	// Count users with "cesar" prefix
	count, err := s.CountUsersWithUsernamePrefix("cesar")
	if err != nil {
		t.Fatalf("CountUsersWithUsernamePrefix failed: %v", err)
	}

	if count != 3 {
		t.Fatalf("expected 3 users with 'cesar' prefix, got %d", count)
	}

	// Count users with "other" prefix
	count, err = s.CountUsersWithUsernamePrefix("other")
	if err != nil {
		t.Fatalf("CountUsersWithUsernamePrefix failed: %v", err)
	}

	if count != 1 {
		t.Fatalf("expected 1 user with 'other' prefix, got %d", count)
	}
}

// TestGenerateUniqueUsername tests unique username generation.
func TestGenerateUniqueUsername(t *testing.T) {
	t.Parallel()
	s := initTestDB(t)
	defer s.Close()

	// Test 1: Generate username when no conflict
	username, err := s.GenerateUniqueUsername("newname")
	if err != nil {
		t.Fatalf("GenerateUniqueUsername failed: %v", err)
	}
	if username != "newname" {
		t.Fatalf("expected 'newname', got %q", username)
	}

	// Test 2: Create a user with this username
	s.Exec(`INSERT INTO users(email, username) VALUES(?, ?)`, "user@example.com", "newname")

	// Test 3: Generate username again (should return numbered version)
	username, err = s.GenerateUniqueUsername("newname")
	if err != nil {
		t.Fatalf("GenerateUniqueUsername failed: %v", err)
	}
	if username == "newname" {
		t.Fatalf("expected numbered version, got 'newname'")
	}
}

// TestMergeOAuthProfileData tests that OAuth data (avatar, username) is merged into existing users.
func TestMergeOAuthProfileData(t *testing.T) {
	t.Parallel()
	s := initTestDB(t)
	defer s.Close()

	// Step 1: Create user via magic link (no username, no avatar)
	magicUser, err := s.GetUserOrCreateByEmail("magicuser@example.com")
	if err != nil {
		t.Fatalf("GetUserOrCreateByEmail failed: %v", err)
	}
	if magicUser.Username != "" || magicUser.AvatarURL != "" {
		t.Fatalf("expected empty username and avatar, got username=%q, avatar=%q", magicUser.Username, magicUser.AvatarURL)
	}

	// Step 2: Merge OAuth profile data into existing user
	oauthUsername := "oauth_username"
	oauthAvatar := "https://example.com/avatar.jpg"

	updated, err := s.MergeOAuthProfileData(magicUser.ID, oauthUsername, oauthAvatar)
	if err != nil {
		t.Fatalf("MergeOAuthProfileData failed: %v", err)
	}

	// Step 3: Verify both username and avatar were updated
	if updated.Username != oauthUsername {
		t.Fatalf("expected username %q, got %q", oauthUsername, updated.Username)
	}
	if updated.AvatarURL != oauthAvatar {
		t.Fatalf("expected avatar %q, got %q", oauthAvatar, updated.AvatarURL)
	}

	// Step 4: Verify user is now enabled (both username and email present)
	if !updated.Enabled {
		t.Fatalf("expected user to be enabled after merge, got enabled=%v", updated.Enabled)
	}
}

// TestMergeOAuthProfileDataPreservesExisting tests that existing profile data is not overwritten.
func TestMergeOAuthProfileDataPreservesExisting(t *testing.T) {
	t.Parallel()
	s := initTestDB(t)
	defer s.Close()

	// Step 1: Create user via OAuth with username and avatar
	oauthUser, err := s.GetUserOrCreateByOAuth("github", "gh-123", "oauthuser@example.com", "existing_username", "https://example.com/existing.jpg")
	if err != nil {
		t.Fatalf("GetUserOrCreateByOAuth failed: %v", err)
	}

	// Step 2: Try to merge new OAuth data (should NOT overwrite existing fields)
	newUsername := "new_username"
	newAvatar := "https://example.com/new.jpg"

	merged, err := s.MergeOAuthProfileData(oauthUser.ID, newUsername, newAvatar)
	if err != nil {
		t.Fatalf("MergeOAuthProfileData failed: %v", err)
	}

	// Step 3: Verify existing data was preserved
	if merged.Username != "existing_username" {
		t.Fatalf("expected username to be preserved as 'existing_username', got %q", merged.Username)
	}
	if merged.AvatarURL != "https://example.com/existing.jpg" {
		t.Fatalf("expected avatar to be preserved, got %q", merged.AvatarURL)
	}
}

// TestMergeOAuthProfileDataPartialUpdate tests partial updates (only avatar or only username).
func TestMergeOAuthProfileDataPartialUpdate(t *testing.T) {
	t.Parallel()
	s := initTestDB(t)
	defer s.Close()

	// Step 1: Create user via magic link (no username, no avatar)
	magicUser, err := s.GetUserOrCreateByEmail("partialuser@example.com")
	if err != nil {
		t.Fatalf("GetUserOrCreateByEmail failed: %v", err)
	}

	// Step 2: Update only username (avatar is empty)
	merged, err := s.MergeOAuthProfileData(magicUser.ID, "partial_username", "")
	if err != nil {
		t.Fatalf("MergeOAuthProfileData with empty avatar failed: %v", err)
	}

	if merged.Username != "partial_username" {
		t.Fatalf("expected username to be updated, got %q", merged.Username)
	}
	if merged.AvatarURL != "" {
		t.Fatalf("expected avatar to remain empty, got %q", merged.AvatarURL)
	}

	// Step 3: Update only avatar (username is now set, so won't be updated)
	merged, err = s.MergeOAuthProfileData(magicUser.ID, "another_username", "https://example.com/avatar.jpg")
	if err != nil {
		t.Fatalf("MergeOAuthProfileData with avatar failed: %v", err)
	}

	if merged.Username != "partial_username" {
		t.Fatalf("expected username to remain 'partial_username', got %q", merged.Username)
	}
	if merged.AvatarURL != "https://example.com/avatar.jpg" {
		t.Fatalf("expected avatar to be updated, got %q", merged.AvatarURL)
	}
}

// TestOAuthAvatarUpdateOnExistingUser tests the complete flow: magic link signup then OAuth with avatar.
func TestOAuthAvatarUpdateOnExistingUser(t *testing.T) {
	t.Parallel()
	s := initTestDB(t)
	defer s.Close()

	// Step 1: User signs up via magic link
	email := "shared@example.com"
	magicLinkUser, err := s.GetUserOrCreateByEmail(email)
	if err != nil {
		t.Fatalf("GetUserOrCreateByEmail failed: %v", err)
	}

	// Step 2: Same user logs in via GitHub OAuth with avatar and username
	githubUsername := "github_user"
	githubAvatar := "https://github.com/github_user.jpg"

	oauthUser, err := s.GetUserOrCreateByOAuth("github", "gh-github_user", email, githubUsername, githubAvatar)
	if err != nil {
		t.Fatalf("GetUserOrCreateByOAuth failed: %v", err)
	}

	// Step 3: Verify same user (same ID)
	if oauthUser.ID != magicLinkUser.ID {
		t.Fatalf("expected same user, got magic_link_id=%d, oauth_id=%d", magicLinkUser.ID, oauthUser.ID)
	}

	// Step 4: Verify avatar and username were populated from OAuth
	if oauthUser.Username != githubUsername {
		t.Fatalf("expected username %q, got %q", githubUsername, oauthUser.Username)
	}
	if oauthUser.AvatarURL != githubAvatar {
		t.Fatalf("expected avatar %q, got %q", githubAvatar, oauthUser.AvatarURL)
	}

	// Step 5: Verify user is now enabled
	if !oauthUser.Enabled {
		t.Fatalf("expected user to be enabled, got enabled=%v", oauthUser.Enabled)
	}
}

// TestOAuthRepeatedLoginMergesProfileData tests that subsequent OAuth logins merge updated profile data.
func TestOAuthRepeatedLoginMergesProfileData(t *testing.T) {
	t.Parallel()
	s := initTestDB(t)
	defer s.Close()

	// Step 1: User logs in via GitHub (first time, no avatar returned)
	email := "repeated@example.com"
	username1 := "github_user"
	avatarURL1 := "" // First login, no avatar

	user1, err := s.GetUserOrCreateByOAuth("github", "gh-repeated-123", email, username1, avatarURL1)
	if err != nil {
		t.Fatalf("first GetUserOrCreateByOAuth failed: %v", err)
	}

	if user1.Username != username1 {
		t.Fatalf("expected username %q, got %q", username1, user1.Username)
	}
	if user1.AvatarURL != "" {
		t.Fatalf("expected empty avatar after first login, got %q", user1.AvatarURL)
	}

	// Step 2: Same user logs in via GitHub again (this time avatar is available)
	avatarURL2 := "https://github.com/github_user.jpg"
	user2, err := s.GetUserOrCreateByOAuth("github", "gh-repeated-123", email, username1, avatarURL2)
	if err != nil {
		t.Fatalf("second GetUserOrCreateByOAuth failed: %v", err)
	}

	// Step 3: Verify same user ID
	if user2.ID != user1.ID {
		t.Fatalf("expected same user ID, got user1.ID=%d, user2.ID=%d", user1.ID, user2.ID)
	}

	// Step 4: Verify avatar was updated from the second login
	if user2.AvatarURL != avatarURL2 {
		t.Fatalf("expected avatar %q after second login, got %q", avatarURL2, user2.AvatarURL)
	}

	// Step 5: Verify username didn't change
	if user2.Username != username1 {
		t.Fatalf("expected username to remain %q, got %q", username1, user2.Username)
	}
}
