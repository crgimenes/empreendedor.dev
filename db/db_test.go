package db

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"edev/utils"
)

// testMigrationMu serializes schema migrations executed by initTestDB to avoid
// concurrent global Storage mutation leading to intermittent missing-table errors.
var testMigrationMu sync.Mutex

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
	const sqlCreateItems = `CREATE TABLE IF NOT EXISTS items(
            id INTEGER PRIMARY KEY,
            name TEXT NOT NULL
        )`
	if err := s.Exec(sqlCreateItems); err != nil {
		t.Fatalf("create table: %v", err)
	}
	for i := 0; i < 3; i++ {
		const sqlInsertItem = `INSERT INTO items(
                name
            ) VALUES (?)`
		if err := s.Exec(sqlInsertItem, fmt.Sprintf("n%d", i)); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	// Query (RO): count and targeted lookup.
	const sqlCountItems = `SELECT
            COUNT(*)
        FROM items`
	rows, err := s.Query(sqlCountItems)
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
	const sqlSelectItemByID = `SELECT
            name
        FROM items
        WHERE id = ?    -- 1`
	if err := s.QueryRow(sqlSelectItemByID, 2).Scan(&name); err != nil {
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

	const sqlCreateKV = `CREATE TABLE IF NOT EXISTS kv(
            k TEXT PRIMARY KEY,
            v TEXT NOT NULL
        )`
	if err := s.Exec(sqlCreateKV); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Commit flow
	tx, err := s.BeginTransaction()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	const sqlInsertKV = `INSERT INTO kv(
                k,
                v
            ) VALUES (?, ?)`
	if err := tx.Exec(sqlInsertKV, "a", "1"); err != nil {
		t.Fatalf("insert a: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	var v string
	const sqlSelectKV = `SELECT
            v
        FROM kv
        WHERE k = ?    -- 1`
	if err := s.QueryRow(sqlSelectKV, "a").Scan(&v); err != nil {
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
	if err := tx2.Exec(sqlInsertKV, "b", "2"); err != nil {
		t.Fatalf("insert b: %v", err)
	}
	if err := tx2.Rollback(); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	var v2 string
	err = s.QueryRow(sqlSelectKV, "b").Scan(&v2)
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
	const sqlCreateT = `CREATE TABLE IF NOT EXISTS t(
            x
        )`
	if err := s.Exec(sqlCreateT); err != nil {
		t.Fatalf("create: %v", err)
	}
	for i := 0; i < 100; i++ {
		const sqlInsertT = `INSERT INTO t(
                x
            ) VALUES (?)`
		if err := s.Exec(sqlInsertT, i); err != nil {
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

	const sqlCreateC = `CREATE TABLE IF NOT EXISTS c(
            n INTEGER
        )`
	if err := s.Exec(sqlCreateC); err != nil {
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
				const sqlInsertC = `INSERT INTO c(
                    n
                ) VALUES (STRFTIME('%s', 'now'))`
				_ = s.Exec(sqlInsertC)
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
				const sqlCountC = `SELECT
                    COUNT(*)
                FROM c`
				if err := s.QueryRow(sqlCountC).Scan(&cnt); err != nil {
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

// initTestDB initializes a test SQLite database with the schema.
// It returns the SQLite instance or fails the test.
//
// The test database file is stored in a temporary directory created by t.TempDir(),
// which is automatically cleaned up by Go's test framework after the test completes.
// Database connections are closed via defer s.Close() in calling tests.
//
// Schema is created by temporarily setting Storage to run migrations on the test instance.
func initTestDB(t *testing.T) *SQLite {
	t.Helper()

	// Use temp file to ensure RW and RO pools share the same database.
	// If we use ":memory:" directly, the RW and RO pools will have separate in-memory databases.
	// t.TempDir() guarantees automatic cleanup after test completes.
	tmp := t.TempDir()
	path := filepath.Join(tmp, "test.db")

	s, err := NewWithPath(path)
	if err != nil {
		t.Fatalf("NewWithPath(%q): %v", path, err)
	}

	// Temporarily set Storage to run migrations on the test instance (serialized).
	testMigrationMu.Lock()
	oldStorage := Storage
	Storage = s
	err = RunMigration()
	Storage = oldStorage
	testMigrationMu.Unlock()

	if err != nil {
		t.Fatalf("RunMigration: %v", err)
	}

	return s
}

func TestGetUserByOAuthProviderID(t *testing.T) {
	t.Parallel()

	s := initTestDB(t)
	defer s.Close()

	// Insert a test user and identity.
	const sqlInsertUser = `INSERT INTO users (
            username,
            email,
            enabled
        ) VALUES (?, ?, ?)`
	if err := s.Exec(sqlInsertUser, "testuser", "test@example.com", 1); err != nil {
		t.Fatalf("insert user: %v", err)
	}

	const sqlInsertIdentity = `INSERT INTO identities (
            user_id,
            provider,
            provider_uid
        ) VALUES (?, ?, ?)`
	if err := s.Exec(sqlInsertIdentity, 1, "github", "gh_12345"); err != nil {
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
				const sqlSelectTokenEmail = `SELECT
                    email
                FROM magic_token
                WHERE token = ?    -- 1`
				err := s.QueryRow(sqlSelectTokenEmail, tt.token).Scan(&storedEmail)
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

	// Insert an expired token using SQLite's time functions
	const sqlInsertExpiredToken = `INSERT INTO magic_token (
            email,
            token,
            action,
            expires_at
        ) VALUES (?, ?, ?, DATETIME('now', '-1 hour'))`
	if err := s.Exec(sqlInsertExpiredToken, "expired@example.com", "tok_expired", "login"); err != nil {
		t.Fatalf("insert expired token: %v", err)
	}

	// Insert a valid (future) token using SQLite's time functions
	const sqlInsertValidToken = `INSERT INTO magic_token (
            email,
            token,
            action,
            expires_at
        ) VALUES (?, ?, ?, DATETIME('now', '+3 hour'))`
	if err := s.Exec(sqlInsertValidToken, "valid@example.com", "tok_valid", "login"); err != nil {
		t.Fatalf("insert valid token: %v", err)
	}

	// Purge expired tokens
	if err := s.PurgeExpiredMagicLinkTokens(); err != nil {
		t.Fatalf("PurgeExpiredMagicLinkTokens() error = %v", err)
	}

	// Verify expired token is gone
	var expiredCount int
	const sqlCountToken = `SELECT
            COUNT(*)
        FROM magic_token
        WHERE token = ?    -- 1`
	if err := s.QueryRow(sqlCountToken, "tok_expired").Scan(&expiredCount); err != nil {
		t.Fatalf("count expired: %v", err)
	}
	if expiredCount != 0 {
		t.Fatalf("expected 0 expired tokens, got %d", expiredCount)
	}

	// Verify valid token still exists
	var validCount int
	if err := s.QueryRow(sqlCountToken, "tok_valid").Scan(&validCount); err != nil {
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
	const sqlInsertTestUser = `INSERT INTO users (
            username,
            email,
            enabled,
            avatar_url
        ) VALUES (?, ?, ?, ?)`
	if err := s.Exec(sqlInsertTestUser, "johndoe", "john@example.com", 1, "https://avatar.example.com/john.jpg"); err != nil {
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
	const sqlCreateTestData = `CREATE TABLE IF NOT EXISTS test_data(
            id INTEGER PRIMARY KEY,
            value TEXT
        )`
	if err := s.Exec(sqlCreateTestData); err != nil {
		t.Fatalf("create table: %v", err)
	}

	// Begin transaction and insert
	tx, err := s.BeginTransaction()
	if err != nil {
		t.Fatalf("BeginTransaction() error = %v", err)
	}

	const sqlInsertTestData = `INSERT INTO test_data(
            value
        ) VALUES (?)`
	if err := tx.Exec(sqlInsertTestData, "test_value"); err != nil {
		t.Fatalf("tx.Exec() error = %v", err)
		_ = tx.Rollback()
	}

	// Query within transaction
	var value string
	const sqlSelectTestData = `SELECT
            value
        FROM test_data
        WHERE id = ?    -- 1`
	if err := tx.QueryRow(sqlSelectTestData, 1).Scan(&value); err != nil {
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
	if err := s.QueryRow(sqlSelectTestData, 1).Scan(&persistedValue); err != nil {
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
	const sqlCreateNumbers = `CREATE TABLE IF NOT EXISTS numbers(
            id INTEGER PRIMARY KEY,
            num INTEGER
        )`
	if err := s.Exec(sqlCreateNumbers); err != nil {
		t.Fatalf("create table: %v", err)
	}

	// Begin transaction and insert multiple rows
	tx, err := s.BeginTransaction()
	if err != nil {
		t.Fatalf("BeginTransaction() error = %v", err)
	}

	for i := 1; i <= 3; i++ {
		const sqlInsertNumbers = `INSERT INTO numbers(
                num
            ) VALUES (?)`
		if err := tx.Exec(sqlInsertNumbers, i*10); err != nil {
			_ = tx.Rollback()
			t.Fatalf("tx.Exec() error = %v", err)
		}
	}

	// Query rows within transaction
	const sqlSelectNumbers = `SELECT
            num
        FROM numbers
        ORDER BY num`
	rows, err := tx.Query(sqlSelectNumbers)
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
				const query = `SELECT
                    email
                FROM magic_token
                WHERE token = ?    -- 1`
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
				const countQuery = `SELECT
                    COUNT(*)
                FROM magic_token
                WHERE token = ?    -- 1`
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
	const countExpired = `SELECT
            COUNT(*)
        FROM magic_token
        WHERE expires_at <= CURRENT_TIMESTAMP`
	if err := s.QueryRow(countExpired).Scan(&count); err != nil {
		t.Fatalf("failed to count expired: %v", err)
	}
	if count != 0 {
		t.Fatalf("expired tokens not purged: count = %d", count)
	}

	// Verify valid token still exists
	var validExists int
	const countValid = `SELECT
            COUNT(*)
        FROM magic_token
        WHERE token = ?    -- 1`
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
				const countSQL = `SELECT
                COUNT(*)
            FROM identities
            WHERE provider = ?         -- 1
            AND provider_uid = ?       -- 2`
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
	const countSQL = `SELECT
            COUNT(*)
        FROM identities
        WHERE user_id = ?           -- 1
        AND provider = ?            -- 2
        AND provider_uid = ?        -- 3`
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
	const sqlInsertUser2 = `INSERT INTO users(
            email,
            username
        ) VALUES (?, ?)`
	s.Exec(sqlInsertUser2, "user1@example.com", "cesar")
	s.Exec(sqlInsertUser2, "user2@example.com", "cesar1")
	s.Exec(sqlInsertUser2, "user3@example.com", "cesar2")
	s.Exec(sqlInsertUser2, "user4@example.com", "other")

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
	const sqlInsertUser3 = `INSERT INTO users(
            email,
            username
        ) VALUES (?, ?)`
	s.Exec(sqlInsertUser3, "user@example.com", "newname")

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

// TestEnableUserByEmailValidation tests enabling user after email validation.
func TestEnableUserByEmailValidationSuccess(t *testing.T) {
	t.Parallel()
	s := initTestDB(t)
	defer s.Close()

	// Step 1: Create user via magic link with email
	email := "validate@example.com"
	u, err := s.GetUserOrCreateByEmail(email)
	if err != nil {
		t.Fatalf("GetUserOrCreateByEmail failed: %v", err)
	}

	if u.Enabled {
		t.Fatalf("user should not be enabled without username")
	}

	// Step 2: Update profile to add username
	u, err = s.UpdateUserProfile(u.ID, "validuser", "https://example.com/avatar.jpg")
	if err != nil {
		t.Fatalf("UpdateUserProfile failed: %v", err)
	}

	// Step 3: Verify user is now enabled
	if !u.Enabled {
		t.Fatalf("user should be enabled after profile update")
	}

	// Step 4: Verify EnableUserByEmailValidation works
	u, err = s.EnableUserByEmailValidation(u.ID)
	if err != nil {
		t.Fatalf("EnableUserByEmailValidation failed: %v", err)
	}

	if !u.Enabled {
		t.Fatalf("user should be enabled")
	}
}

// TestEnableUserByEmailValidationNoEmail tests error when user has no email.
func TestEnableUserByEmailValidationNoEmail(t *testing.T) {
	t.Parallel()
	s := initTestDB(t)
	defer s.Close()

	// Try to enable user with no email (invalid state)
	_, err := s.EnableUserByEmailValidation(9999)
	if err == nil {
		t.Fatalf("EnableUserByEmailValidation should fail for non-existent user")
	}
}

// TestEnableUserByEmailValidationNoUsername tests error when user has no username.
func TestEnableUserByEmailValidationNoUsername(t *testing.T) {
	t.Parallel()
	s := initTestDB(t)
	defer s.Close()

	// Step 1: Create user via magic link (no username)
	u, err := s.GetUserOrCreateByEmail("nousername@example.com")
	if err != nil {
		t.Fatalf("GetUserOrCreateByEmail failed: %v", err)
	}

	// Step 2: Try to enable user without username
	_, err = s.EnableUserByEmailValidation(u.ID)
	if err == nil {
		t.Fatalf("EnableUserByEmailValidation should fail when username is empty")
	}

	if !strings.Contains(err.Error(), "username") {
		t.Fatalf("expected error about username, got: %v", err)
	}
}

// TestGetUserOrCreateByOAuthNewUser tests creating a new OAuth user.
func TestGetUserOrCreateByOAuthNewUser(t *testing.T) {
	t.Parallel()
	s := initTestDB(t)
	defer s.Close()

	u, err := s.GetUserOrCreateByOAuth("github", "gh-new-123", "newuser@example.com", "newusername", "https://example.com/avatar.jpg")
	if err != nil {
		t.Fatalf("GetUserOrCreateByOAuth failed: %v", err)
	}

	if u.ID == 0 {
		t.Fatalf("user should have been created with non-zero ID")
	}

	if u.Email != "newuser@example.com" {
		t.Fatalf("expected email 'newuser@example.com', got %q", u.Email)
	}

	if u.Username != "newusername" {
		t.Fatalf("expected username 'newusername', got %q", u.Username)
	}

	if u.AvatarURL != "https://example.com/avatar.jpg" {
		t.Fatalf("expected avatar, got %q", u.AvatarURL)
	}

	if !u.Enabled {
		t.Fatalf("user should be enabled (has email and username)")
	}
}

// TestGetUserOrCreateByOAuthExistingIdentity tests returning existing OAuth identity.
func TestGetUserOrCreateByOAuthExistingIdentity(t *testing.T) {
	t.Parallel()
	s := initTestDB(t)
	defer s.Close()

	// Step 1: Create user
	u1, err := s.GetUserOrCreateByOAuth("github", "gh-existing-123", "existing@example.com", "existinguser", "https://example.com/avatar1.jpg")
	if err != nil {
		t.Fatalf("first GetUserOrCreateByOAuth failed: %v", err)
	}

	// Step 2: Call again with same provider/ID
	u2, err := s.GetUserOrCreateByOAuth("github", "gh-existing-123", "different@example.com", "differentuser", "https://example.com/avatar2.jpg")
	if err != nil {
		t.Fatalf("second GetUserOrCreateByOAuth failed: %v", err)
	}

	// Step 3: Verify same user returned (ID unchanged)
	if u2.ID != u1.ID {
		t.Fatalf("expected same user ID, got u1.ID=%d, u2.ID=%d", u1.ID, u2.ID)
	}

	// Step 4: Verify profile wasn't overwritten
	if u2.Email != "existing@example.com" {
		t.Fatalf("expected email to remain 'existing@example.com', got %q", u2.Email)
	}
}

// TestGetUserOrCreateByOAuthMultipleProvidersViaEmail tests linking multiple providers to same user via email.
func TestGetUserOrCreateByOAuthMultipleProvidersViaEmail(t *testing.T) {
	t.Parallel()
	s := initTestDB(t)
	defer s.Close()

	// Step 1: Create user via GitHub
	email := "multiauth@example.com"
	u1, err := s.GetUserOrCreateByOAuth("github", "gh-123", email, "ghuser", "https://github.com/avatar.jpg")
	if err != nil {
		t.Fatalf("GitHub GetUserOrCreateByOAuth failed: %v", err)
	}

	// Step 2: Create via X (Twitter) with same email
	u2, err := s.GetUserOrCreateByOAuth("twitter", "tw-456", email, "twitteruser", "https://twitter.com/avatar.jpg")
	if err != nil {
		t.Fatalf("Twitter GetUserOrCreateByOAuth failed: %v", err)
	}

	// Step 3: Verify same user
	if u2.ID != u1.ID {
		t.Fatalf("expected same user, got u1.ID=%d, u2.ID=%d", u1.ID, u2.ID)
	}

	// Step 4: Verify both identities are linked
	var ghCount, twCount int
	const sqlCountIdentities = `SELECT
            COUNT(*)
        FROM identities
        WHERE user_id = ?       -- 1
        AND provider = ?        -- 2`
	_ = s.QueryRow(sqlCountIdentities, u1.ID, "github").Scan(&ghCount)
	_ = s.QueryRow(sqlCountIdentities, u1.ID, "twitter").Scan(&twCount)

	if ghCount != 1 || twCount != 1 {
		t.Fatalf("expected both providers linked, got github=%d, twitter=%d", ghCount, twCount)
	}
}

// TestGetUserOrCreateByOAuthWithoutEmail tests creating user without email.
func TestGetUserOrCreateByOAuthWithoutEmail(t *testing.T) {
	t.Parallel()
	s := initTestDB(t)
	defer s.Close()

	u, err := s.GetUserOrCreateByOAuth("github", "gh-no-email", "", "noemailu", "https://example.com/avatar.jpg")
	if err != nil {
		t.Fatalf("GetUserOrCreateByOAuth without email failed: %v", err)
	}

	if u.Email != "" {
		t.Fatalf("expected empty email, got %q", u.Email)
	}

	if u.Enabled {
		t.Fatalf("user without email should not be enabled (requires both email and username)")
	}
}

// TestGetUserOrCreateByOAuthWithoutUsername tests creating user without username.
func TestGetUserOrCreateByOAuthWithoutUsername(t *testing.T) {
	t.Parallel()
	s := initTestDB(t)
	defer s.Close()

	u, err := s.GetUserOrCreateByOAuth("github", "gh-no-username", "nousername@example.com", "", "https://example.com/avatar.jpg")
	if err != nil {
		t.Fatalf("GetUserOrCreateByOAuth without username failed: %v", err)
	}

	if u.Username != "" {
		t.Fatalf("expected empty username, got %q", u.Username)
	}

	if u.Enabled {
		t.Fatalf("user without username should not be enabled")
	}
}

// TestGetUserOrCreateByOAuthUsernameConflictOnNewUser tests that new user gets unique username when conflict exists.
func TestGetUserOrCreateByOAuthUsernameConflictOnNewUser(t *testing.T) {
	t.Parallel()
	s := initTestDB(t)
	defer s.Close()

	// Step 1: Create first user with username "alice"
	_, err := s.GetUserOrCreateByOAuth("github", "gh-alice1", "alice1@example.com", "alice", "")
	if err != nil {
		t.Fatalf("first user creation failed: %v", err)
	}

	// Step 2: Create second user trying to use same username "alice"
	u2, err := s.GetUserOrCreateByOAuth("github", "gh-alice2", "alice2@example.com", "alice", "")
	if err != nil {
		t.Fatalf("second user creation failed: %v", err)
	}

	// Step 3: Verify second user got unique username
	if u2.Username == "alice" {
		t.Fatalf("expected unique username, got 'alice'")
	}

	if u2.Username != "alice1" {
		t.Fatalf("expected 'alice1', got %q", u2.Username)
	}
}

// TestUpdateUserProfileSuccess tests successful profile update.
func TestUpdateUserProfileSuccess(t *testing.T) {
	t.Parallel()
	s := initTestDB(t)
	defer s.Close()

	// Create user first
	u, err := s.GetUserOrCreateByEmail("profiletest@example.com")
	if err != nil {
		t.Fatalf("GetUserOrCreateByEmail failed: %v", err)
	}

	// Update profile
	updated, err := s.UpdateUserProfile(u.ID, "profileuser", "https://example.com/profile.jpg")
	if err != nil {
		t.Fatalf("UpdateUserProfile failed: %v", err)
	}

	if updated.Username != "profileuser" {
		t.Fatalf("expected username 'profileuser', got %q", updated.Username)
	}

	if updated.AvatarURL != "https://example.com/profile.jpg" {
		t.Fatalf("expected avatar URL, got %q", updated.AvatarURL)
	}

	if !updated.Enabled {
		t.Fatalf("user should be enabled after profile update")
	}
}

// TestUpdateUserProfileEmptyUsername tests error on empty username.
func TestUpdateUserProfileEmptyUsername(t *testing.T) {
	t.Parallel()
	s := initTestDB(t)
	defer s.Close()

	u, _ := s.GetUserOrCreateByEmail("empty@example.com")

	_, err := s.UpdateUserProfile(u.ID, "   ", "")
	if err == nil {
		t.Fatalf("UpdateUserProfile should fail with empty username")
	}

	if !strings.Contains(err.Error(), "username is required") {
		t.Fatalf("expected 'username is required' error, got %v", err)
	}
}

// TestUpdateUserProfileZeroUserID tests error with zero user ID.
func TestUpdateUserProfileZeroUserID(t *testing.T) {
	t.Parallel()
	s := initTestDB(t)
	defer s.Close()

	_, err := s.UpdateUserProfile(0, "testuser", "")
	if err == nil {
		t.Fatalf("UpdateUserProfile should fail with zero user ID")
	}

	if !strings.Contains(err.Error(), "user_id is required") {
		t.Fatalf("expected 'user_id is required' error, got %v", err)
	}
}

// TestMergeOAuthProfileDataNoChanges tests merge when no changes are needed.
func TestMergeOAuthProfileDataNoChanges(t *testing.T) {
	t.Parallel()
	s := initTestDB(t)
	defer s.Close()

	// Create user with username and avatar already
	u, _ := s.GetUserOrCreateByOAuth("github", "gh-123", "merge@example.com", "mergeuser", "https://example.com/avatar.jpg")

	// Try to merge with new data (should not change)
	merged, err := s.MergeOAuthProfileData(u.ID, "differentuser", "https://example.com/different.jpg")
	if err != nil {
		t.Fatalf("MergeOAuthProfileData failed: %v", err)
	}

	if merged.Username != "mergeuser" {
		t.Fatalf("expected username to remain 'mergeuser', got %q", merged.Username)
	}

	if merged.AvatarURL != "https://example.com/avatar.jpg" {
		t.Fatalf("expected avatar to remain unchanged, got %q", merged.AvatarURL)
	}
}

// TestMergeOAuthProfileDataZeroUserID tests error with zero user ID.
func TestMergeOAuthProfileDataZeroUserID(t *testing.T) {
	t.Parallel()
	s := initTestDB(t)
	defer s.Close()

	_, err := s.MergeOAuthProfileData(0, "user", "https://example.com/avatar.jpg")
	if err == nil {
		t.Fatalf("MergeOAuthProfileData should fail with zero user ID")
	}

	if !strings.Contains(err.Error(), "user_id is required") {
		t.Fatalf("expected 'user_id is required' error, got %v", err)
	}
}

// TestStoreMagicLinkTokenValidation tests token storage with validation.
func TestStoreMagicLinkTokenValidation(t *testing.T) {
	t.Parallel()
	s := initTestDB(t)
	defer s.Close()

	token := "valid-token-123"
	email := "tokentest@example.com"
	expiresAt := time.Now().UTC().Add(1 * time.Hour)

	err := s.StoreMagicLinkToken(token, email, expiresAt)
	if err != nil {
		t.Fatalf("StoreMagicLinkToken failed: %v", err)
	}

	// Verify token was stored
	var storedEmail string
	const sqlSelectToken = `SELECT
            email
        FROM magic_token
        WHERE token = ?    -- 1`
	_ = s.QueryRow(sqlSelectToken, token).Scan(&storedEmail)

	if storedEmail != email {
		t.Fatalf("expected email %q, got %q", email, storedEmail)
	}
}

// TestConsumeMagicLinkTokenExpired tests consuming an expired token.
func TestConsumeMagicLinkTokenExpired(t *testing.T) {
	t.Parallel()
	s := initTestDB(t)
	defer s.Close()

	token := "expired-token"
	email := "expired@example.com"
	expiresAt := time.Now().UTC().Add(-1 * time.Hour) // Already expired

	_ = s.StoreMagicLinkToken(token, email, expiresAt)

	// Try to consume expired token
	retrievedEmail, err := s.ConsumeMagicLinkToken(token)
	if err != nil {
		t.Fatalf("ConsumeMagicLinkToken failed: %v", err)
	}

	if retrievedEmail != "" {
		t.Fatalf("expected empty email for expired token, got %q", retrievedEmail)
	}
}

// TestGetUserByIDNotFound tests error when user doesn't exist.
func TestGetUserByIDNotFound(t *testing.T) {
	t.Parallel()
	s := initTestDB(t)
	defer s.Close()

	_, err := s.GetUserByID(99999)
	if err == nil {
		t.Fatalf("GetUserByID should fail for non-existent user")
	}

	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected sql.ErrNoRows, got %v", err)
	}
}

// TestGetUserOrCreateByEmailCaseSensitivity tests case-insensitive email handling.
func TestGetUserOrCreateByEmailCaseSensitivity(t *testing.T) {
	t.Parallel()
	s := initTestDB(t)
	defer s.Close()

	// Create user with lowercase email
	u1, err := s.GetUserOrCreateByEmail("Test@Example.Com")
	if err != nil {
		t.Fatalf("first GetUserOrCreateByEmail failed: %v", err)
	}

	// Get same user with different case
	u2, err := s.GetUserOrCreateByEmail("test@example.com")
	if err != nil {
		t.Fatalf("second GetUserOrCreateByEmail failed: %v", err)
	}

	// Should be same user
	if u2.ID != u1.ID {
		t.Fatalf("expected same user, got u1.ID=%d, u2.ID=%d", u1.ID, u2.ID)
	}
}

// TestGetUserOrCreateByOAuthMultipleNewUsers tests creating multiple new OAuth users.
func TestGetUserOrCreateByOAuthMultipleNewUsers(t *testing.T) {
	t.Parallel()
	s := initTestDB(t)
	defer s.Close()

	// Create multiple users via OAuth
	u1, _ := s.GetUserOrCreateByOAuth("github", "gh-1", "user1@example.com", "user1", "")
	u2, _ := s.GetUserOrCreateByOAuth("github", "gh-2", "user2@example.com", "user2", "")
	u3, _ := s.GetUserOrCreateByOAuth("twitter", "tw-1", "user3@example.com", "user3", "")

	if u1.ID == u2.ID || u2.ID == u3.ID || u1.ID == u3.ID {
		t.Fatalf("all users should have different IDs")
	}

	if u1.Username != "user1" || u2.Username != "user2" || u3.Username != "user3" {
		t.Fatalf("usernames don't match expected values")
	}
}

// TestGenerateUniqueUsernameMultipleConflicts tests generating unique username with multiple conflicts.
func TestGenerateUniqueUsernameMultipleConflicts(t *testing.T) {
	t.Parallel()
	s := initTestDB(t)
	defer s.Close()

	// Create users with numbered usernames
	const sqlInsertUserAlice = `INSERT INTO users(
            email,
            username
        ) VALUES (?, ?)`
	s.Exec(sqlInsertUserAlice, "u1@example.com", "alice")
	s.Exec(sqlInsertUserAlice, "u2@example.com", "alice1")
	s.Exec(sqlInsertUserAlice, "u3@example.com", "alice2")
	s.Exec(sqlInsertUserAlice, "u4@example.com", "alice3")

	// Generate unique username should give alice4
	username, err := s.GenerateUniqueUsername("alice")
	if err != nil {
		t.Fatalf("GenerateUniqueUsername failed: %v", err)
	}

	if username != "alice4" {
		t.Fatalf("expected 'alice4', got %q", username)
	}
}

// TestCountUsersWithUsernamePrefixEdgeCases tests prefix counting with edge cases.
func TestCountUsersWithUsernamePrefixEdgeCases(t *testing.T) {
	t.Parallel()
	s := initTestDB(t)
	defer s.Close()

	// No users created yet
	count, _ := s.CountUsersWithUsernamePrefix("nonexistent")
	if count != 0 {
		t.Fatalf("expected 0 count, got %d", count)
	}

	// Create users and test case-insensitive matching
	const sqlInsertUserTest = `INSERT INTO users(
            email,
            username
        ) VALUES (?, ?)`
	s.Exec(sqlInsertUserTest, "u1@example.com", "Test")
	s.Exec(sqlInsertUserTest, "u2@example.com", "TEST1")
	s.Exec(sqlInsertUserTest, "u3@example.com", "test2")

	// Count with lowercase should match all case variations
	count, _ = s.CountUsersWithUsernamePrefix("test")
	if count != 3 {
		t.Fatalf("expected 3 (case-insensitive), got %d", count)
	}

	// Count with uppercase should also match
	count, _ = s.CountUsersWithUsernamePrefix("TEST")
	if count != 3 {
		t.Fatalf("expected 3 (case-insensitive), got %d", count)
	}
}

// TestStoreMagicLinkTokenDuplicateEmail tests storing multiple tokens for same email.
func TestStoreMagicLinkTokenDuplicateEmail(t *testing.T) {
	t.Parallel()
	s := initTestDB(t)
	defer s.Close()

	email := "multi@example.com"
	token1 := "token-1"
	token2 := "token-2"

	// Store first token
	_ = s.StoreMagicLinkToken(token1, email, time.Now().UTC().Add(1*time.Hour))

	// Store second token for same email
	_ = s.StoreMagicLinkToken(token2, email, time.Now().UTC().Add(1*time.Hour))

	// Both should exist
	var count int
	const sqlCountTokenEmail = `SELECT
            COUNT(*)
        FROM magic_token
        WHERE email = ?    -- 1`
	_ = s.QueryRow(sqlCountTokenEmail, email).Scan(&count)

	if count != 2 {
		t.Fatalf("expected 2 tokens for same email, got %d", count)
	}
}

// TestPurgeExpiredMagicLinkTokensMultipleExpired tests purging multiple expired tokens.
func TestPurgeExpiredMagicLinkTokensMultipleExpired(t *testing.T) {
	t.Parallel()
	s := initTestDB(t)
	defer s.Close()

	// Create mix of expired and valid tokens
	for i := 0; i < 5; i++ {
		token := fmt.Sprintf("expired-%d", i)
		_ = s.StoreMagicLinkToken(token, "expired@example.com", time.Now().UTC().Add(-1*time.Hour))
	}

	for i := 0; i < 3; i++ {
		token := fmt.Sprintf("valid-%d", i)
		_ = s.StoreMagicLinkToken(token, "valid@example.com", time.Now().UTC().Add(1*time.Hour))
	}

	// Verify initial state
	var beforeCount int
	const sqlCountAllTokens = `SELECT
            COUNT(*)
        FROM magic_token`
	_ = s.QueryRow(sqlCountAllTokens).Scan(&beforeCount)
	if beforeCount != 8 {
		t.Fatalf("expected 8 tokens before purge, got %d", beforeCount)
	}

	// Purge
	_ = s.PurgeExpiredMagicLinkTokens()

	// Verify only valid tokens remain
	var afterCount int
	_ = s.QueryRow(sqlCountAllTokens).Scan(&afterCount)
	if afterCount != 3 {
		t.Fatalf("expected 3 tokens after purge, got %d", afterCount)
	}
}

// TestGetUserByOAuthProviderIDMultipleIdentities tests retrieving user with multiple provider identities.
func TestGetUserByOAuthProviderIDMultipleIdentities(t *testing.T) {
	t.Parallel()
	s := initTestDB(t)
	defer s.Close()

	// Create user with multiple OAuth providers
	u, _ := s.GetUserOrCreateByOAuth("github", "gh-123", "multi@example.com", "multiuser", "")
	_, _ = s.GetUserOrCreateByOAuth("twitter", "tw-456", "multi@example.com", "", "")

	// Find user by GitHub identity
	userID, _ := s.GetUserByOAuthProviderID("github", "gh-123")
	if userID != u.ID {
		t.Fatalf("expected user ID %d, got %d", u.ID, userID)
	}

	// Find user by Twitter identity
	userID, _ = s.GetUserByOAuthProviderID("twitter", "tw-456")
	if userID != u.ID {
		t.Fatalf("expected user ID %d, got %d", u.ID, userID)
	}
}

// TestUpdateUserProfileAvatarOnly tests updating only avatar URL.
func TestUpdateUserProfileAvatarOnly(t *testing.T) {
	t.Parallel()
	s := initTestDB(t)
	defer s.Close()

	u, _ := s.GetUserOrCreateByEmail("avatar@example.com")

	// Update profile with avatar
	updated, err := s.UpdateUserProfile(u.ID, "avataruser", "https://example.com/avatar.jpg")
	if err != nil {
		t.Fatalf("UpdateUserProfile failed: %v", err)
	}

	// Verify avatar is set
	if updated.AvatarURL != "https://example.com/avatar.jpg" {
		t.Fatalf("expected avatar URL, got %q", updated.AvatarURL)
	}
}

// TestConsumeMagicLinkTokenTwice tests consuming same token twice.
func TestConsumeMagicLinkTokenTwice(t *testing.T) {
	t.Parallel()
	s := initTestDB(t)
	defer s.Close()

	token := "twice-token"
	email := "twice@example.com"

	_ = s.StoreMagicLinkToken(token, email, time.Now().UTC().Add(1*time.Hour))

	// First consume
	email1, _ := s.ConsumeMagicLinkToken(token)
	if email1 != email {
		t.Fatalf("first consume failed")
	}

	// Second consume should return empty
	email2, _ := s.ConsumeMagicLinkToken(token)
	if email2 != "" {
		t.Fatalf("expected empty email on second consume, got %q", email2)
	}
}

// TestMergeOAuthProfileDataUsernameConflict tests merge when username would conflict.
func TestMergeOAuthProfileDataUsernameConflict(t *testing.T) {
	t.Parallel()
	s := initTestDB(t)
	defer s.Close()

	// Create first user
	_, _ = s.GetUserOrCreateByOAuth("github", "gh-1", "user1@example.com", "conflicted", "")

	// Create second user without username
	u2, _ := s.GetUserOrCreateByOAuth("twitter", "tw-1", "user2@example.com", "", "")

	// Try to merge with conflicting username
	merged, err := s.MergeOAuthProfileData(u2.ID, "conflicted", "https://example.com/avatar.jpg")
	if err != nil {
		t.Fatalf("MergeOAuthProfileData failed: %v", err)
	}

	// Should get unique username
	if merged.Username == "conflicted" {
		t.Fatalf("expected unique username, got 'conflicted'")
	}

	if merged.Username != "conflicted1" {
		t.Fatalf("expected 'conflicted1', got %q", merged.Username)
	}
}

// TestGetUserOrCreateByOAuthAllEmpty tests creating OAuth user with no data.
func TestGetUserOrCreateByOAuthAllEmpty(t *testing.T) {
	t.Parallel()
	s := initTestDB(t)
	defer s.Close()

	u, err := s.GetUserOrCreateByOAuth("github", "gh-empty", "", "", "")
	if err != nil {
		t.Fatalf("GetUserOrCreateByOAuth with empty data failed: %v", err)
	}

	if u.Email != "" || u.Username != "" || u.AvatarURL != "" {
		t.Fatalf("expected all fields empty")
	}

	if u.Enabled {
		t.Fatalf("user with no email/username should not be enabled")
	}
}

// TestMagicLinkTokenRoundTripWithLeadingTrailingSpaces tests email canonicalization.
func TestMagicLinkTokenRoundTripWithLeadingTrailingSpaces(t *testing.T) {
	t.Parallel()
	s := initTestDB(t)
	defer s.Close()

	// Store token with space-padded email (should be canonicalized)
	email := "  spaces@example.com  "
	token := "spaces-token"

	err := s.StoreMagicLinkToken(token, email, time.Now().UTC().Add(1*time.Hour))
	if err != nil {
		t.Fatalf("StoreMagicLinkToken failed: %v", err)
	}

	// Consume should work
	retrieved, _ := s.ConsumeMagicLinkToken(token)
	if retrieved == "" {
		t.Fatalf("expected to consume token")
	}
}

// TestGetUserOrCreateByEmailInvalidEmail tests handling of invalid emails.
func TestGetUserOrCreateByEmailInvalidEmail(t *testing.T) {
	t.Parallel()
	s := initTestDB(t)
	defer s.Close()

	// Try to create user with invalid email
	_, err := s.GetUserOrCreateByEmail("not-an-email")
	if err == nil {
		t.Fatalf("GetUserOrCreateByEmail should fail with invalid email")
	}
}

// TestUpdateUserProfileNoUserFound tests updating non-existent user.
func TestUpdateUserProfileNoUserFound(t *testing.T) {
	t.Parallel()
	s := initTestDB(t)
	defer s.Close()

	// Try to update non-existent user
	_, err := s.UpdateUserProfile(99999, "testuser", "")
	if err == nil {
		t.Fatalf("UpdateUserProfile should fail for non-existent user")
	}
}

// TestEnableUserByEmailValidationZeroUserID tests error with zero user ID.
func TestEnableUserByEmailValidationZeroUserID(t *testing.T) {
	t.Parallel()
	s := initTestDB(t)
	defer s.Close()

	_, err := s.EnableUserByEmailValidation(0)
	if err == nil {
		t.Fatalf("EnableUserByEmailValidation should fail with zero user ID")
	}

	if !strings.Contains(err.Error(), "user_id is required") {
		t.Fatalf("expected 'user_id is required' error, got %v", err)
	}
}

// TestGenerateUniqueUsernameEmpty tests generating unique username from empty base.
func TestGenerateUniqueUsernameEmpty(t *testing.T) {
	t.Parallel()
	s := initTestDB(t)
	defer s.Close()

	username, err := s.GenerateUniqueUsername("")
	if err != nil {
		t.Fatalf("GenerateUniqueUsername with empty base failed: %v", err)
	}

	if username != "" {
		t.Fatalf("expected empty string for empty base, got %q", username)
	}
}

// TestMergeOAuthProfileDataNonExistentUser tests merge with non-existent user.
func TestMergeOAuthProfileDataNonExistentUser(t *testing.T) {
	t.Parallel()
	s := initTestDB(t)
	defer s.Close()

	_, err := s.MergeOAuthProfileData(99999, "user", "https://example.com/avatar.jpg")
	if err == nil {
		t.Fatalf("MergeOAuthProfileData should fail for non-existent user")
	}
}

// TestGetUserByOAuthProviderIDNotFound tests retrieving non-existent provider ID.
func TestGetUserByOAuthProviderIDNotFound(t *testing.T) {
	t.Parallel()
	s := initTestDB(t)
	defer s.Close()

	userID, err := s.GetUserByOAuthProviderID("github", "gh-nonexistent")
	if err != nil {
		t.Fatalf("GetUserByOAuthProviderID should not error for missing ID: %v", err)
	}

	if userID != 0 {
		t.Fatalf("expected userID 0 for non-existent provider, got %d", userID)
	}
}

// TestUpdateUserProfileUsernameValidationComprehensive tests comprehensive username validation scenarios.
func TestUpdateUserProfileUsernameValidationComprehensive(t *testing.T) {
	t.Parallel()
	s := initTestDB(t)
	defer s.Close()

	tests := []struct {
		name        string
		setupUsers  []struct{ email, username string }
		updateUser  int // index of user to update (0-based)
		newUsername string
		expectError bool
		errorMsg    string
	}{
		{
			name: "update_to_existing_username_exact_match",
			setupUsers: []struct{ email, username string }{
				{"user1@test.com", "alice"},
				{"user2@test.com", "bob"},
			},
			updateUser:  1,
			newUsername: "alice",
			expectError: true,
			errorMsg:    "already in use",
		},
		{
			name: "update_to_existing_username_case_insensitive",
			setupUsers: []struct{ email, username string }{
				{"user1@test.com", "alice"},
				{"user2@test.com", "bob"},
			},
			updateUser:  1,
			newUsername: "ALICE",
			expectError: true,
			errorMsg:    "already in use",
		},
		{
			name: "update_to_existing_username_mixed_case",
			setupUsers: []struct{ email, username string }{
				{"user1@test.com", "AlIcE"},
				{"user2@test.com", "bob"},
			},
			updateUser:  1,
			newUsername: "alice",
			expectError: true,
			errorMsg:    "already in use",
		},
		{
			name: "update_to_same_username_should_succeed",
			setupUsers: []struct{ email, username string }{
				{"user1@test.com", "alice"},
			},
			updateUser:  0,
			newUsername: "alice",
			expectError: false,
		},
		{
			name: "update_to_same_username_different_case_should_succeed",
			setupUsers: []struct{ email, username string }{
				{"user1@test.com", "alice"},
			},
			updateUser:  0,
			newUsername: "ALICE",
			expectError: false,
		},
		{
			name: "update_to_unique_username_should_succeed",
			setupUsers: []struct{ email, username string }{
				{"user1@test.com", "alice"},
				{"user2@test.com", "bob"},
			},
			updateUser:  1,
			newUsername: "charlie",
			expectError: false,
		},
		{
			name: "three_users_conflict_scenario",
			setupUsers: []struct{ email, username string }{
				{"user1@test.com", "alice"},
				{"user2@test.com", "bob"},
				{"user3@test.com", "charlie"},
			},
			updateUser:  2,
			newUsername: "BOB",
			expectError: true,
			errorMsg:    "already in use",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clear database for this subtest
			s.Exec("DELETE FROM users")

			// Setup users
			var users []*User
			for _, setup := range tt.setupUsers {
				user, err := s.GetUserOrCreateByEmail(setup.email)
				if err != nil {
					t.Fatalf("Failed to create user %s: %v", setup.email, err)
				}

				// Set username if provided
				if setup.username != "" {
					user, err = s.UpdateUserProfile(user.ID, setup.username, "")
					if err != nil {
						t.Fatalf("Failed to set username %s for user %s: %v", setup.username, setup.email, err)
					}
				}
				users = append(users, user)
			}

			// Perform the update test
			targetUser := users[tt.updateUser]
			_, err := s.UpdateUserProfile(targetUser.ID, tt.newUsername, "")

			if tt.expectError {
				if err == nil {
					t.Fatalf("Expected error when updating username to '%s', got nil", tt.newUsername)
				}
				if !strings.Contains(err.Error(), tt.errorMsg) {
					t.Fatalf("Expected error containing '%s', got: %v", tt.errorMsg, err)
				}
			} else {
				if err != nil {
					t.Fatalf("Expected no error when updating username to '%s', got: %v", tt.newUsername, err)
				}
			}
		})
	}
}

// TestGetUserOrCreateByOAuthInvalidUsername tests that invalid usernames from OAuth are treated as empty.
func TestGetUserOrCreateByOAuthInvalidUsername(t *testing.T) {
	t.Parallel()
	s := initTestDB(t)
	defer s.Close()

	tests := []struct {
		name            string
		username        string
		email           string
		expectEmptyUser bool
		expectEnabled   bool
	}{
		{
			name:            "valid_username_should_be_used",
			username:        "validuser",
			email:           "valid@example.com",
			expectEmptyUser: false,
			expectEnabled:   true,
		},
		{
			name:            "username_too_short_should_be_ignored",
			username:        "ab",
			email:           "short@example.com",
			expectEmptyUser: true,
			expectEnabled:   false,
		},
		{
			name:            "username_too_long_should_be_ignored",
			username:        "this_is_a_very_long_username_that_exceeds_30_characters",
			email:           "long@example.com",
			expectEmptyUser: true,
			expectEnabled:   false,
		},
		{
			name:            "username_with_spaces_should_be_ignored",
			username:        "user name",
			email:           "spaces@example.com",
			expectEmptyUser: true,
			expectEnabled:   false,
		},
		{
			name:            "username_with_special_chars_should_be_ignored",
			username:        "user@name",
			email:           "special@example.com",
			expectEmptyUser: true,
			expectEnabled:   false,
		},
		{
			name:            "username_with_unicode_should_be_ignored",
			username:        "usuário",
			email:           "unicode@example.com",
			expectEmptyUser: true,
			expectEnabled:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clear database for this subtest
			s.Exec("DELETE FROM identities")
			s.Exec("DELETE FROM users")

			// Create OAuth user
			user, err := s.GetUserOrCreateByOAuth("github", "gh123", tt.email, tt.username, "")
			if err != nil {
				t.Fatalf("GetUserOrCreateByOAuth failed: %v", err)
			}

			// Check username behavior
			if tt.expectEmptyUser {
				if user.Username != "" {
					t.Errorf("Expected empty username for invalid username '%s', got '%s'", tt.username, user.Username)
				}
			} else {
				if user.Username == "" {
					t.Errorf("Expected non-empty username for valid username '%s'", tt.username)
				}
			}

			// Check enabled status
			if user.Enabled != tt.expectEnabled {
				t.Errorf("Expected enabled=%v, got enabled=%v", tt.expectEnabled, user.Enabled)
			}

			// Check email is preserved
			if user.Email != tt.email {
				t.Errorf("Expected email '%s', got '%s'", tt.email, user.Email)
			}
		})
	}
}

// TestMergeOAuthProfileDataInvalidUsername tests that invalid usernames are ignored during profile merge.
func TestMergeOAuthProfileDataInvalidUsername(t *testing.T) {
	t.Parallel()
	s := initTestDB(t)
	defer s.Close()

	// Create initial user with email but no username
	user, err := s.GetUserOrCreateByEmail("test@example.com")
	if err != nil {
		t.Fatalf("GetUserOrCreateByEmail failed: %v", err)
	}

	tests := []struct {
		name           string
		oauthUsername  string
		expectUsername string
		expectEnabled  bool
	}{
		{
			name:           "valid_username_should_be_merged",
			oauthUsername:  "validuser",
			expectUsername: "validuser",
			expectEnabled:  true,
		},
		{
			name:           "invalid_username_should_be_ignored",
			oauthUsername:  "a",
			expectUsername: "",
			expectEnabled:  false,
		},
		{
			name:           "username_with_special_chars_should_be_ignored",
			oauthUsername:  "user@name",
			expectUsername: "",
			expectEnabled:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Reset user to initial state (email only, no username)
			s.Exec("UPDATE users SET username = '', enabled = 0 WHERE id = ?", user.ID)

			// Merge OAuth profile data
			updatedUser, err := s.MergeOAuthProfileData(user.ID, tt.oauthUsername, "")
			if err != nil {
				t.Fatalf("MergeOAuthProfileData failed: %v", err)
			}

			// Check username behavior
			if updatedUser.Username != tt.expectUsername {
				t.Errorf("Expected username '%s', got '%s'", tt.expectUsername, updatedUser.Username)
			}

			// Check enabled status
			if updatedUser.Enabled != tt.expectEnabled {
				t.Errorf("Expected enabled=%v, got enabled=%v", tt.expectEnabled, updatedUser.Enabled)
			}
		})
	}
}

// TestCreateMinimalUserForOAuthFallback verifies that minimal fallback users
// can be created when OAuth signup fails.
func TestCreateMinimalUserForOAuthFallback(t *testing.T) {
	t.Parallel()

	s := initTestDB(t)
	defer s.Close()

	tests := []struct {
		name        string
		email       string
		avatarURL   string
		expectError bool
		expectID    bool
	}{
		{
			name:        "valid_email_should_create_user",
			email:       "user@example.com",
			avatarURL:   "https://example.com/avatar.jpg",
			expectError: false,
			expectID:    true,
		},
		{
			name:        "valid_email_without_avatar",
			email:       "user2@example.com",
			avatarURL:   "",
			expectError: false,
			expectID:    true,
		},
		{
			name:        "empty_email_should_error",
			email:       "",
			avatarURL:   "https://example.com/avatar.jpg",
			expectError: true,
			expectID:    false,
		},
		{
			name:        "whitespace_email_should_error",
			email:       "   ",
			avatarURL:   "",
			expectError: true,
			expectID:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			user, err := s.CreateMinimalUserForOAuthFallback(tt.email, tt.avatarURL)

			if tt.expectError {
				if err == nil {
					t.Errorf("Expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("Expected no error, got: %v", err)
				return
			}

			if tt.expectID && user.ID == 0 {
				t.Errorf("Expected user ID > 0, got 0")
			}

			// Verify user is created with empty username
			if user.Username != "" {
				t.Errorf("Expected empty username, got %q", user.Username)
			}

			// Verify user is not enabled
			if user.Enabled {
				t.Errorf("Expected enabled=false, got enabled=true")
			}

			// Verify email is set
			if user.Email != tt.email {
				t.Errorf("Expected email %q, got %q", tt.email, user.Email)
			}

			// Verify avatar URL is set
			if user.AvatarURL != tt.avatarURL {
				t.Errorf("Expected avatar_url %q, got %q", tt.avatarURL, user.AvatarURL)
			}

			// Verify user can be retrieved from database
			retrievedUser, err := s.GetUserByID(user.ID)
			if err != nil {
				t.Errorf("Failed to retrieve created user: %v", err)
			}
			if retrievedUser.ID != user.ID {
				t.Errorf("Retrieved user has different ID: expected %d, got %d", user.ID, retrievedUser.ID)
			}
		})
	}
}

// TestBuildFTSQuery covers the unexported buildFTSQuery helper ensuring tokenization
// and quote stripping behavior matches expectations.
func TestBuildFTSQuery(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   string
		out  string
	}{
		{"empty", "", ""},
		{"spaces_only", "   \t  ", ""},
		{"single_token", "alpha", "alpha*"},
		{"multi_tokens", "alpha beta", "alpha* beta*"},
		{"quoted_tokens", "'gamma' \"delta\"", "gamma* delta*"},
		{"mixed_quotes", "'a\"b' c", "ab* c*"},
	}

	for _, tc := range cases {
		got := buildFTSQuery(tc.in)
		if got != tc.out {
			// Keep failure concise; show both values
			// Using Errorf (not Fatalf) to see all subcase failures
			t.Errorf("%s: expected %q got %q", tc.name, tc.out, got)
		}
	}
}

// TestFileMetadataCRUDAndSearch exercises SaveFileMetadata, GetFileByUserIDAndFilename,
// GetFileByUserReferenceIDAndFilename, ListFilesByUserID, ListFilesByUserIDSorted,
// SearchFilesByUserIDFTS (both empty query fallback and real search),
// UpdateFileMetadataByUserAndFilename and SoftDeleteFileByUserAndFilename.
func TestFileMetadataCRUDAndSearch(t *testing.T) {
	// Avoid t.Parallel here due to cumulative DB operations; keeps memory usage lower.
	s := initTestDB(t)
	defer s.Close()

	// Insert a user; required for foreign key user_id.
	const sqlInsertUser = `INSERT INTO users (
	    email,             -- 1
	    username,          -- 2
	    avatar_url,        -- 3
	    enabled,           -- 4
	    created_at,
	    updated_at
	) VALUES (
	    ?,                 -- 1
	    ?,                 -- 2
	    ?,                 -- 3
	    ?,                 -- 4
	    CURRENT_TIMESTAMP,
	    CURRENT_TIMESTAMP
	)
	RETURNING
	    id,                -- 1
	    reference_id,      -- 2
	    COALESCE(username, ''), -- 3
	    email,             -- 4
	    COALESCE(avatar_url, ''), -- 5
	    enabled            -- 6`

	var userID int64
	var userRefID string
	err := s.QueryRowRW(sqlInsertUser, "filetester@example.com", "filetester", "", true).Scan(&userID, &userRefID, new(string), new(string), new(string), new(bool))
	if err != nil {
		t.Fatalf("insert user error: %v", err)
	}
	// Reload to obtain trigger-populated reference_id (migrations set this after insert).
	uReload, err := s.GetUserByID(userID)
	if err != nil {
		t.Fatalf("GetUserByID reload error: %v", err)
	}
	userRefID = uReload.ReferenceID

	// SaveFileMetadata nil input should error.
	if _, err := s.SaveFileMetadata(nil); err == nil {
		t.Errorf("expected error on nil file metadata input")
	}

	// Create several files.
	files := []*File{
		{UserID: userID, OriginalFilename: "Beta.txt", Filename: "beta.txt", Filesize: 10, Filetype: "text/plain", Filehash: "h1", Filetag: "project", Filedescription: "alpha beta", Processed: false},
		{UserID: userID, OriginalFilename: "alpha.txt", Filename: "alpha.txt", Filesize: 11, Filetype: "text/plain", Filehash: "h2", Filetag: "notes", Filedescription: "gamma delta", Processed: true},
		{UserID: userID, OriginalFilename: "zeta.txt", Filename: "zeta.txt", Filesize: 12, Filetype: "text/plain", Filehash: "h3", Filetag: "misc", Filedescription: "epsilon", Processed: false},
	}
	for i, f := range files {
		saved, err := s.SaveFileMetadata(f)
		if err != nil {
			t.Fatalf("SaveFileMetadata %d error: %v", i, err)
		}
		files[i] = saved
	}

	// GetFileByUserIDAndFilename existing
	f1, err := s.GetFileByUserIDAndFilename(userID, "alpha.txt")
	if err != nil || f1 == nil {
		t.Fatalf("expected file alpha.txt, got err=%v file=%v", err, f1)
	}
	// GetFileByUserIDAndFilename missing
	fn, err := s.GetFileByUserIDAndFilename(userID, "missing.txt")
	if err != nil || fn != nil {
		t.Fatalf("expected (nil,nil) for missing file, got (%v,%v)", fn, err)
	}

	// GetFileByUserReferenceIDAndFilename existing via reference_id
	f2, err := s.GetFileByUserReferenceIDAndFilename(userRefID, "beta.txt")
	if err != nil || f2 == nil {
		t.Fatalf("expected file beta.txt by reference id, got err=%v file=%v", err, f2)
	}

	// ListFilesByUserID basic listing
	listed, err := s.ListFilesByUserID(userID, 0, 10)
	if err != nil || len(listed) != 3 {
		t.Fatalf("expected 3 files listed, got %d err=%v", len(listed), err)
	}

	// ListFilesByUserIDSorted by name ascending should have alpha first.
	listedSorted, err := s.ListFilesByUserIDSorted(userID, "name_asc", 0, 10)
	if err != nil {
		t.Fatalf("ListFilesByUserIDSorted error: %v", err)
	}
	if len(listedSorted) == 0 || listedSorted[0].OriginalFilename != "alpha.txt" {
		t.Errorf("expected first sorted file alpha.txt, got %q", func() string {
			if len(listedSorted) == 0 {
				return "(none)"
			}
			return listedSorted[0].OriginalFilename
		}())
	}

	// SearchFilesByUserIDFTS empty query fallback: returns same as ListFilesByUserID (limit may differ)
	searchEmpty, err := s.SearchFilesByUserIDFTS(userID, "   ", "date_desc", 0, 10)
	if err != nil || len(searchEmpty) != 3 {
		t.Fatalf("expected fallback search to list 3 files, got %d err=%v", len(searchEmpty), err)
	}

	// SearchFilesByUserIDFTS real query matching "alpha" (in description of first file) and tag "project" (tokenization AND behavior requires both if both supplied).
	searchOne, err := s.SearchFilesByUserIDFTS(userID, "alpha", "date_desc", 0, 10)
	if err != nil {
		t.Fatalf("search alpha error: %v", err)
	}
	if len(searchOne) == 0 {
		t.Fatalf("expected at least one search result for 'alpha'")
	}

	// Update metadata for beta.txt
	if err := s.UpdateFileMetadataByUserAndFilename(userID, "beta.txt", "updated desc", "updatedtag"); err != nil {
		t.Fatalf("UpdateFileMetadata error: %v", err)
	}
	fUpdated, err := s.GetFileByUserIDAndFilename(userID, "beta.txt")
	if err != nil || fUpdated == nil {
		t.Fatalf("expected updated beta.txt, err=%v file=%v", err, fUpdated)
	}
	if fUpdated.Filedescription != "updated desc" || fUpdated.Filetag != "updatedtag" {
		t.Errorf("metadata not updated: got desc=%q tag=%q", fUpdated.Filedescription, fUpdated.Filetag)
	}

	// Soft delete zeta.txt then ensure retrieval returns nil
	if err := s.SoftDeleteFileByUserAndFilename(userID, "zeta.txt"); err != nil {
		t.Fatalf("SoftDelete error: %v", err)
	}
	deletedFile, err := s.GetFileByUserIDAndFilename(userID, "zeta.txt")
	if err != nil || deletedFile != nil {
		t.Fatalf("expected deleted file to be nil, got %v err=%v", deletedFile, err)
	}
}

// TestForumLifecycleCRUD exercises forum, thread, and post CRUD paths:
// CreateForum, GetForumByExternalID, UpdateForum, ListForums,
// CreateThread, GetThreadByExternalID, ListThreadsByForumID, UpdateThread,
// CreatePost, UpdatePost, GetPostsByThreadID, DeletePost.
func TestForumLifecycleCRUD(t *testing.T) {
	s := initTestDB(t)
	defer s.Close()

	// Insert a user to own forum/thread/posts.
	const sqlInsertUser = `INSERT INTO users (
	    email,             -- 1
	    username,          -- 2
	    avatar_url,        -- 3
	    enabled,           -- 4
	    created_at,
	    updated_at
	) VALUES (
	    ?,                 -- 1
	    ?,                 -- 2
	    ?,                 -- 3
	    ?,                 -- 4
	    CURRENT_TIMESTAMP,
	    CURRENT_TIMESTAMP
	)
	RETURNING
	    id,                -- 1
	    reference_id,      -- 2
	    COALESCE(username, ''), -- 3
	    email,             -- 4
	    COALESCE(avatar_url, ''), -- 5
	    enabled            -- 6`

	var ownerID int64
	err := s.QueryRowRW(sqlInsertUser, "forumowner@example.com", "forumowner", "", true).Scan(&ownerID, new(string), new(string), new(string), new(string), new(bool))
	if err != nil {
		t.Fatalf("insert owner user error: %v", err)
	}

	// Create forum
	forumExternal := "forum-ext-1"
	f, err := s.CreateForum(forumExternal, 0, 0, ownerID, "Titulo", "Descricao", "img.png")
	if err != nil {
		t.Fatalf("CreateForum error: %v", err)
	}
	if f.ExternalID != forumExternal || f.Title != "Titulo" {
		t.Errorf("forum fields mismatch: %+v", f)
	}
	if f.OwnerUserName != "forumowner" {
		t.Errorf("forum OwnerUserName not filled: expected 'forumowner', got '%s'", f.OwnerUserName)
	}

	// Get forum by external id
	fGet, err := s.GetForumByExternalID(forumExternal)
	if err != nil || fGet == nil || fGet.ID != f.ID {
		t.Fatalf("GetForumByExternalID mismatch: %v err=%v", fGet, err)
	}

	// Update forum
	fUpdated, err := s.UpdateForum(forumExternal, "NovoTitulo", "NovaDesc", "new.png")
	if err != nil {
		t.Fatalf("UpdateForum error: %v", err)
	}
	if fUpdated.Title != "NovoTitulo" || fUpdated.Description != "NovaDesc" || fUpdated.ImageURL != "new.png" {
		t.Errorf("forum not updated: %+v", fUpdated)
	}

	// List forums (pagination)
	forums, err := s.ListForums(0, 10)
	if err != nil || len(forums) == 0 {
		t.Fatalf("ListForums expected at least 1, got %d err=%v", len(forums), err)
	}

	// Create threads
	threadExternal1 := "thread-ext-1"
	threadExternal2 := "thread-ext-2"
	t1, err := s.CreateThread(f.ID, ownerID, "Thread 1", threadExternal1, "t1.png")
	if err != nil {
		t.Fatalf("CreateThread 1 error: %v", err)
	}
	if t1.OwnerUserName != "forumowner" {
		t.Errorf("thread OwnerUserName not filled: expected 'forumowner', got '%s'", t1.OwnerUserName)
	}
	_, err = s.CreateThread(f.ID, ownerID, "Thread 2", threadExternal2, "t2.png")
	if err != nil {
		t.Fatalf("CreateThread 2 error: %v", err)
	}

	// Get thread by external id
	tGet, err := s.GetThreadByExternalID(threadExternal1)
	if err != nil || tGet == nil || tGet.ID != t1.ID {
		t.Fatalf("GetThreadByExternalID mismatch: %v err=%v", tGet, err)
	}

	// List threads for forum
	threads, err := s.ListThreadsByForumID(f.ID, 0, 10)
	if err != nil || len(threads) < 2 {
		t.Fatalf("ListThreadsByForumID expected >=2, got %d err=%v", len(threads), err)
	}

	// Update thread
	tUpdated, err := s.UpdateThread(threadExternal2, "Thread 2 Updated", "t2-new.png")
	if err != nil {
		t.Fatalf("UpdateThread error: %v", err)
	}
	if tUpdated.Title != "Thread 2 Updated" || tUpdated.ImageURL != "t2-new.png" {
		t.Errorf("thread not updated: %+v", tUpdated)
	}

	// Create posts in thread 1
	postExternal1 := "post-ext-1"
	p1, err := s.CreatePost(t1.ID, ownerID, "Conteudo 1", "<p>Conteudo 1</p>", postExternal1, nil)
	if err != nil {
		t.Fatalf("CreatePost 1 error: %v", err)
	}
	postExternal2 := "post-ext-2"
	p2, err := s.CreatePost(t1.ID, ownerID, "Conteudo 2", "<p>Conteudo 2</p>", postExternal2, nil)
	if err != nil {
		t.Fatalf("CreatePost 2 error: %v", err)
	}

	// Update post 2
	p2Updated, err := s.UpdatePost(p2.ID, "Conteudo 2 Alterado", "<p>Conteudo 2 Alterado</p>")
	if err != nil {
		t.Fatalf("UpdatePost error: %v", err)
	}
	if !strings.Contains(p2Updated.Content, "Alterado") {
		t.Errorf("post content not updated: %+v", p2Updated)
	}

	// Get posts by thread (should include both)
	posts, err := s.GetPostsByThreadID(t1.ID)
	if err != nil || len(posts) < 2 {
		t.Fatalf("GetPostsByThreadID expected >=2, got %d err=%v", len(posts), err)
	}

	// Delete first post
	if err := s.DeletePost(p1.ID); err != nil {
		t.Fatalf("DeletePost error: %v", err)
	}

	// Ensure posts list shrinks
	postsAfter, err := s.GetPostsByThreadID(t1.ID)
	if err != nil {
		t.Fatalf("GetPostsByThreadID after delete error: %v", err)
	}
	if len(postsAfter) != len(posts)-1 {
		t.Errorf("expected posts count %d after delete, got %d", len(posts)-1, len(postsAfter))
	}
}
