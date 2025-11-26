// Package db provides SQLite access using modernc.org/sqlite (no CGO).
// Goals: performance, concurrency and predictability with minimal dependencies.
// - Separate pools: one writer (RW) and many readers (RO).
// - WAL + synchronous=NORMAL + busy_timeout.
// - Short transactions; no per-operation context timeouts in this layer.
// - WAL checkpoint on Close() for hygiene.
package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"

	"edev/config"
	"edev/log"
	"edev/mail"
	"edev/utils"
)

// SQLite holds separate read/write pools.
type SQLite struct {
	rw *sql.DB // single-writer pool
	ro *sql.DB // read-only pool
}

// Transaction wraps a write transaction.
type Transaction struct {
	tx *sql.Tx
}

// Row wraps sql.Row to keep a uniform return type and allow future extension.
// No context cancellation is used at this layer.
type Row struct {
	row  *sql.Row
	once sync.Once
	err  error
}

var (
	// Storage keeps a global handle for convenience (preserves your original pattern).
	Storage *SQLite

	ErrNoRows = sql.ErrNoRows
)

func newRow(row *sql.Row) *Row {
	return &Row{row: row}
}

func errorRow(err error) *Row {
	return &Row{err: err}
}

// Scan delegates to the underlying sql.Row.
func (r *Row) Scan(dest ...any) error {
	if r == nil {
		return errors.New("nil row")
	}
	if r.err != nil {
		return r.err
	}
	if r.row == nil {
		return errors.New("nil row")
	}
	return r.row.Scan(dest...)
}

// Err mirrors (*sql.Row).Err.
func (r *Row) Err() error {
	if r == nil {
		return errors.New("nil row")
	}
	if r.err != nil {
		return r.err
	}
	if r.row == nil {
		return errors.New("nil row")
	}
	return r.row.Err()
}

// Tunables (adjust as needed for your service profile).
const (
	// Slightly longer to avoid flakiness with parallel tests/CI.
	defaultBusyTimeout     = 15 * time.Second
	defaultWriteOpTimeout  = 8 * time.Second
	defaultReadOpTimeout   = 5 * time.Second
	defaultConnMaxLifeRW   = 2 * time.Minute
	defaultConnMaxLifeRO   = 5 * time.Minute
	defaultReadPoolMinimum = 4 // will be raised to GOMAXPROCS if larger
)

// New initializes RW/RO pools.
// Uses config.Cfg.DBFile as the SQLite path/URI;
func New() (*SQLite, error) {
	path := config.Cfg.DBFile
	return NewWithPath(path)
}

// NewWithPath creates SQLite pools for a specific file/URI path.
func NewWithPath(path string) (*SQLite, error) {
	if path == "" {
		return nil, errors.New("database path required")
	}

	// DSN for write pool: WAL, NORMAL, busy_timeout, foreign_keys ON, automatic_index ON,
	// temp_store in memory, modest cache, and tx lock set to IMMEDIATE.
	rwDSN := fmt.Sprintf(
		"file:%s?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(%d)&_pragma=foreign_keys(ON)&_pragma=automatic_index(ON)&_pragma=temp_store(MEMORY)&_pragma=cache_size(-20000)&_txlock=immediate",
		path, int(defaultBusyTimeout.Milliseconds()),
	)
	// DSN for read-only pool: mode=ro with busy_timeout and foreign_keys ON.
	roDSN := fmt.Sprintf(
		"file:%s?mode=ro&_pragma=busy_timeout(%d)&_pragma=foreign_keys(ON)",
		path, int(defaultBusyTimeout.Milliseconds()),
	)

	s := &SQLite{}

	// Open writer (single connection for predictable write latency under contention).
	rw, err := sql.Open("sqlite", rwDSN)
	if err != nil {
		return nil, fmt.Errorf("open RW: %w", err)
	}
	rw.SetMaxOpenConns(1)
	rw.SetMaxIdleConns(1)
	rw.SetConnMaxLifetime(defaultConnMaxLifeRW)
	s.rw = rw

	// Open readers (parallel reads).
	ro, err := sql.Open("sqlite", roDSN)
	if err != nil {
		utils.Closer(s.rw)
		return nil, fmt.Errorf("open RO: %w", err)
	}
	max := defaultReadPoolMinimum
	if n := runtime.GOMAXPROCS(0); n > max {
		max = n
	}
	ro.SetMaxOpenConns(max)
	ro.SetMaxIdleConns(max)
	ro.SetConnMaxLifetime(defaultConnMaxLifeRO)
	s.ro = ro

	return s, nil
}

// BeginTransaction starts a write transaction.
//
// IMPORTANT: We intentionally do not propagate context timeouts in this package.
// Callers should avoid long-lived transactions; SQLite busy_timeout handles
// transient contention, and application code should keep critical sections short.
func (s *SQLite) BeginTransaction() (*Transaction, error) {
	if s == nil || s.rw == nil {
		return nil, errors.New("db not initialized")
	}
	tx, err := s.rw.BeginTx(context.Background(), nil)
	if err != nil {
		return nil, err
	}
	return &Transaction{tx: tx}, nil
}

// Commit finalizes a transaction; on error, attempts a rollback.
func (t *Transaction) Commit() error {
	if t == nil || t.tx == nil {
		return errors.New("nil tx")
	}
	err := t.tx.Commit()
	if err != nil {
		_ = t.tx.Rollback()
		t.tx = nil
		return err
	}
	t.tx = nil
	return nil
}

// Rollback aborts the transaction.
func (t *Transaction) Rollback() error {
	if t == nil || t.tx == nil {
		return nil
	}
	err := t.tx.Rollback()
	t.tx = nil
	return err
}

// Exec executes a write statement inside the transaction.
func (t *Transaction) Exec(query string, args ...any) error {
	if t == nil || t.tx == nil {
		return errors.New("nil tx")
	}
	_, err := t.tx.Exec(query, args...)
	return err
}

// Query runs a SELECT inside the transaction (consistent view).
func (t *Transaction) Query(query string, args ...any) (*sql.Rows, error) {
	if t == nil || t.tx == nil {
		return nil, errors.New("nil tx")
	}
	// Note: Do not use a request-scoped context here; the lifetime of sql.Rows
	// extends beyond this function, and premature cancellation would break iteration.
	return t.tx.Query(query, args...)
}

// QueryRow returns a single row inside the transaction.
func (t *Transaction) QueryRow(query string, args ...any) *Row {
	if t == nil || t.tx == nil {
		return errorRow(errors.New("nil tx"))
	}
	return newRow(t.tx.QueryRow(query, args...))
}

// Exec executes a write statement on the RW pool (outside explicit transactions).
func (s *SQLite) Exec(query string, args ...any) error {
	if s == nil || s.rw == nil {
		return errors.New("db not initialized")
	}
	_, err := s.rw.Exec(query, args...)
	return err
}

// Query executes a SELECT on the RO pool (parallel reads).
func (s *SQLite) Query(query string, args ...any) (*sql.Rows, error) {
	if s == nil || s.ro == nil {
		return nil, errors.New("db not initialized")
	}
	// Note: Avoid wrapping with request-scoped contexts here. The returned
	// sql.Rows must remain valid for iteration by the caller.
	return s.ro.Query(query, args...)
}

// QueryRow executes a single-row SELECT on the RO pool.
func (s *SQLite) QueryRow(query string, args ...any) *Row {
	if s == nil || s.ro == nil {
		return errorRow(errors.New("db not initialized"))
	}
	return newRow(s.ro.QueryRow(query, args...))
}

func (s *SQLite) QueryRowRW(query string, args ...any) *Row {
	if s == nil || s.rw == nil {
		return errorRow(errors.New("db not initialized"))
	}
	return newRow(s.rw.QueryRow(query, args...))
}

// QueryRW allows SELECT using the RW pool (rarely needed).
func (s *SQLite) QueryRW(query string, args ...any) (*sql.Rows, error) {
	if s == nil || s.rw == nil {
		return nil, errors.New("db not initialized")
	}
	// See note above: keep rows consumable without premature cancellation.
	return s.rw.Query(query, args...)
}

// CheckpointWAL triggers a WAL checkpoint with TRUNCATE.
func (s *SQLite) CheckpointWAL() error {
	if s == nil || s.rw == nil {
		return errors.New("db not initialized")
	}
	_, err := s.rw.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
	return err
}

// Close closes pools; performs a best-effort WAL checkpoint first.
func (s *SQLite) Close() {
	if s == nil {
		return
	}
	if err := s.CheckpointWAL(); err != nil {
		log.Println("wal checkpoint:", err)
	}
	utils.Closer(s.ro)
	utils.Closer(s.rw)
}

// Get user by oauth provider id.
func (s *SQLite) GetUserByOAuthProviderID(provider string, providerID string) (int64, error) {
	const sqlStatement = `SELECT
            u.id
        FROM users u
        JOIN identities i ON u.id = i.user_id
        WHERE i.provider = ?    -- 1
        AND i.provider_uid = ?  -- 2
        LIMIT 1;`

	row := s.QueryRow(
		sqlStatement,
		provider,   // 1
		providerID, // 2
	)
	var userID int64
	err := row.Scan(&userID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, nil // No user found
		}
		return 0, err
	}
	return userID, nil
}

// CountUsersWithUsernamePrefix counts how many users have a username starting with the given prefix (case-insensitive).
// Used for generating unique usernames when conflicts occur.
func (s *SQLite) CountUsersWithUsernamePrefix(prefix string) (int, error) {
	const sqlStatement = `SELECT COUNT(*)
        FROM users
        WHERE LOWER(username) LIKE LOWER(?) || '%'`

	var count int
	err := s.QueryRow(sqlStatement, prefix).Scan(&count)
	if err != nil {
		return 0, err
	}
	return count, nil
}

// GenerateUniqueUsername generates a unique username by appending a number if the base username is taken.
// If baseUsername is available, returns it unchanged.
// Otherwise, appends 1, 2, 3, etc. until a unique username is found.
func (s *SQLite) GenerateUniqueUsername(baseUsername string) (string, error) {
	const sqlCheck = `SELECT COUNT(*)
        FROM users
        WHERE LOWER(username) = LOWER(?)`

	// Check if base username is available
	var count int
	err := s.QueryRow(sqlCheck, baseUsername).Scan(&count)
	if err != nil {
		return "", err
	}

	if count == 0 {
		return baseUsername, nil // Base username is available
	}

	// Base username is taken, find how many variants exist with this prefix
	existingCount, err := s.CountUsersWithUsernamePrefix(baseUsername)
	if err != nil {
		return "", err
	}

	// Try appending numbers until we find an available username
	for i := 1; i <= existingCount+10; i++ {
		candidate := baseUsername + fmt.Sprintf("%d", i)
		err := s.QueryRow(sqlCheck, candidate).Scan(&count)
		if err != nil {
			return "", err
		}
		if count == 0 {
			return candidate, nil
		}
	}

	return "", fmt.Errorf("could not generate unique username for %s", baseUsername)
}

func (s *SQLite) StoreMagicLinkToken(
	token string,
	email string,
	expiresAt time.Time) error {
	const sqlStatement = `INSERT INTO magic_token (
            email,         -- 1
            token,         -- 2
            expires_at,    -- 3
            action
        ) VALUES (
            ?,        -- 1
            ?,        -- 2
            ?,        -- 3
            'login'   -- action
        );`

	return s.Exec(
		sqlStatement,
		email,     // 1
		token,     // 2
		expiresAt, // 3
	)
}

func (s *SQLite) ConsumeMagicLinkToken(token string) (string, error) {
	const sqlSelect = `SELECT
            email
        FROM magic_token
        WHERE token = ?          -- 1
        AND expires_at > CURRENT_TIMESTAMP
        LIMIT 1;`

	var email string
	err := s.QueryRow(
		sqlSelect,
		token, // 1
	).Scan(&email)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil // No token found
		}
		return "", err
	}

	const sqlDelete = `DELETE FROM magic_token
        WHERE token = ?;` // 1

	err = s.Exec(
		sqlDelete,
		token, // 1
	)
	if err != nil {
		return "", err
	}

	return email, nil
}

func (s *SQLite) PurgeExpiredMagicLinkTokens() error {
	const sqlStatement = `DELETE FROM magic_token
        WHERE expires_at <= CURRENT_TIMESTAMP;`

	return s.Exec(sqlStatement)
}

func (s *SQLite) GetUserOrCreateByEmail(email string) (*User, error) {
	const sqlSelect = `SELECT
            id,                         -- 1
            reference_id,               -- 2
            COALESCE(username, ''),     -- 3
            email,                      -- 4
            COALESCE(avatar_url, ''),   -- 5
            enabled                     -- 6
        FROM users
        WHERE email = ?  -- 1
        LIMIT 1;`

	var u User

	email, err := mail.CanonicalizeEmail(email)
	if err != nil {
		return nil, err
	}

	err = s.QueryRow(
		sqlSelect,
		email, // 1
	).Scan(
		&u.ID,          // 1
		&u.ReferenceID, // 2
		&u.Username,    // 3
		&u.Email,       // 4
		&u.AvatarURL,   // 5
		&u.Enabled,     // 6
	)
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
	}

	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}

	if err == nil {
		// User already exists
		return &u, nil
	}

	const sqlInsert = `INSERT INTO users (
            email,             -- 1
            created_at,
            updated_at
        ) VALUES (
            ?,                 -- 1
            CURRENT_TIMESTAMP, -- created_at
            CURRENT_TIMESTAMP  -- updated_at
        )
        RETURNING
            id,                        -- 1
            reference_id,              -- 2
            COALESCE(username, ''),    -- 3
            email,                     -- 4
            COALESCE(avatar_url, ''),  -- 5
            enabled;` // 6

	err = s.QueryRowRW(
		sqlInsert,
		email, // 1
	).Scan(
		&u.ID,          // 1
		&u.ReferenceID, // 2
		&u.Username,    // 3
		&u.Email,       // 4
		&u.AvatarURL,   // 5
		&u.Enabled,     // 6
	)
	if err != nil {
		return nil, err
	}

	// IMPORTANT: ReferenceID is filled by an AFTER INSERT trigger.
	// When using RETURNING, SQLite returns values prior to AFTER triggers.
	// Reload user to ensure ReferenceID (and other trigger-updated fields) are populated.
	fresh, err := s.GetUserByID(u.ID)
	if err != nil {
		return nil, err
	}
	return fresh, nil
}

func (s *SQLite) GetUserByID(userID int64) (*User, error) {
	const sqlSelect = `SELECT
            id,                         -- 1
            reference_id,               -- 2
            COALESCE(username, ''),     -- 3
            email,                      -- 4
            COALESCE(avatar_url, ''),   -- 5
            enabled                     -- 6
        FROM users
        WHERE id = ?  -- 1
        LIMIT 1;`

	var u User

	err := s.QueryRow(
		sqlSelect,
		userID, // 1
	).Scan(
		&u.ID,          // 1
		&u.ReferenceID, // 2
		&u.Username,    // 3
		&u.Email,       // 4
		&u.AvatarURL,   // 5
		&u.Enabled,     // 6
	)
	if err != nil {
		return nil, err
	}

	return &u, nil
}

// MergeOAuthProfileData merges OAuth provider data into existing user,
// updating only blank fields.
// It updates username (with conflict resolution) if Username is
// empty and oauthUsername is provided.
// It updates avatar_url if user.AvatarURL is empty and oauthAvatarURL is provided.
// Returns updated user with enabled=true if both username and email
// are now present, or error.
func (s *SQLite) MergeOAuthProfileData(
	userID int64,
	oauthUsername string,
	oauthAvatarURL string,
) (*User, error) {

	if userID == 0 {
		return nil, errors.New("user_id is required")
	}

	// Validate username from OAuth provider - if invalid, treat as empty
	if oauthUsername != "" {
		if err := IsValidUsername(oauthUsername); err != nil {
			oauthUsername = "" // Treat invalid username as empty
		}
	}

	// Get current user
	currentUser, err := s.GetUserByID(userID)
	if err != nil {
		return nil, err
	}

	if currentUser.Email == "" {
		return nil, errors.New("user has no email; cannot update")
	}

	// Determine which fields need updating
	newUsername := currentUser.Username
	newAvatarURL := currentUser.AvatarURL

	// Update username only if current is empty and OAuth provides one
	if oauthUsername != "" && currentUser.Username == "" {
		uniqueUsername, err := s.GenerateUniqueUsername(oauthUsername)
		if err != nil {
			return nil, err
		}
		newUsername = uniqueUsername
	}

	// Update avatar_url only if current is empty and OAuth provides one
	if oauthAvatarURL != "" && currentUser.AvatarURL == "" {
		newAvatarURL = oauthAvatarURL
	}

	// If nothing changed, return current user as-is
	if newUsername == currentUser.Username && newAvatarURL == currentUser.AvatarURL {
		return currentUser, nil
	}

	// Determine if user should be enabled (both username and email must be present)
	shouldBeEnabled := newUsername != "" && currentUser.Email != ""

	// Update user with new values
	const sqlUpdate = `UPDATE users
        SET
            username = ?,    -- 1
            avatar_url = ?,  -- 2
            enabled = ?      -- 3
        WHERE id = ?         -- 4
        RETURNING
            id,                       -- 1
            reference_id,             -- 2
            COALESCE(username, ''),   -- 3
            email,                    -- 4
            COALESCE(avatar_url, ''), -- 5
            enabled;` // 6

	var u User
	enabled := 0
	if shouldBeEnabled {
		enabled = 1
	}

	err = s.QueryRowRW(
		sqlUpdate,
		newUsername,  // 1
		newAvatarURL, // 2
		enabled,      // 3
		userID,       // 4
	).Scan(
		&u.ID,          // 1
		&u.ReferenceID, // 2
		&u.Username,    // 3
		&u.Email,       // 4
		&u.AvatarURL,   // 5
		&u.Enabled,     // 6
	)
	if err != nil {
		return nil, err
	}

	return &u, nil
}

// Validate username to prefent abuse.
func IsValidUsername(username string) error {
	if len(username) < 3 || len(username) > 30 {
		return errors.New("username must be between 3 and 30 characters")
	}

	for _, r := range username {
		if !(r >= 'a' && r <= 'z') &&
			!(r >= 'A' && r <= 'Z') &&
			!(r >= '0' && r <= '9') &&
			r != '_' && r != '-' {
			return errors.New("username contains invalid characters")
		}
	}

	return nil
}

// UpdateUserProfile updates username, avatar_url and enables user if email is
// already validated (email must be non-empty). Validates that username and
// email are not duplicated (case-insensitive). Returns the updated user or
// error if validation fails or SQL error occurs.
func (s *SQLite) UpdateUserProfile(
	userID int64,
	username string,
	avatarURL string,
) (*User, error) {

	if userID == 0 {
		return nil, errors.New("user_id is required")
	}

	username = strings.TrimSpace(username)
	if username == "" {
		return nil, errors.New("username is required")
	}

	err := IsValidUsername(username)
	if err != nil {
		return nil, err
	}

	avatarURL = strings.TrimSpace(avatarURL)

	// Get current user to verify email is present
	currentUser, err := s.GetUserByID(userID)
	if err != nil {
		return nil, err
	}

	if currentUser.Email == "" {
		return nil, errors.New("user has no email; cannot enable account")
	}

	// Check username uniqueness (case-insensitive)
	const sqlCheckUsername = `SELECT COUNT(*)
        FROM users
        WHERE LOWER(username) = LOWER(?)
        AND id != ?
        LIMIT 1;`

	var count int
	err = s.QueryRow(sqlCheckUsername, username, userID).Scan(&count)
	if err != nil {
		return nil, err
	}
	if count > 0 {
		return nil, errors.New("username is already in use")
	}

	// Update user: set username, avatar_url, and enable
	const sqlUpdate = `UPDATE users
        SET
            username = ?,    -- 1
            avatar_url = ?,  -- 2
            enabled = 1
        WHERE id = ?         -- 3
        RETURNING
            id,                        -- 1
            reference_id,              -- 2
            COALESCE(username, ''),    -- 3
            email,                     -- 4
            COALESCE(avatar_url, ''),  -- 5
            enabled;` // 6

	var u User
	err = s.QueryRowRW(
		sqlUpdate,
		username,  // 1
		avatarURL, // 2
		userID,    // 3
	).Scan(
		&u.ID,          // 1
		&u.ReferenceID, // 2
		&u.Username,    // 3
		&u.Email,       // 4
		&u.AvatarURL,   // 5
		&u.Enabled,     // 6
	)
	if err != nil {
		return nil, err
	}

	return &u, nil
}

// EnableUserByEmailValidation enables a user if they have both username and
// email. This is called after email is validated via magic link or OAuth.
// Returns the updated user or error if not found or already enabled.
func (s *SQLite) EnableUserByEmailValidation(userID int64) (*User, error) {

	if userID == 0 {
		return nil, errors.New("user_id is required")
	}

	// Get current user
	currentUser, err := s.GetUserByID(userID)
	if err != nil {
		return nil, err
	}

	if currentUser.Email == "" {
		return nil, errors.New("user has no email; cannot enable")
	}

	if currentUser.Username == "" {
		return nil, errors.New("user has no username; cannot enable")
	}

	// Enable user
	const sqlEnable = `UPDATE users
        SET enabled = 1
        WHERE id = ?
        RETURNING
            id,                       -- 1
            reference_id,             -- 2
            COALESCE(username, ''),   -- 3
            email,                    -- 4
            COALESCE(avatar_url, ''), -- 5
            enabled;` // 6

	var u User
	err = s.QueryRowRW(sqlEnable, userID).Scan(
		&u.ID,          // 1
		&u.ReferenceID, // 2
		&u.Username,    // 3
		&u.Email,       // 4
		&u.AvatarURL,   // 5
		&u.Enabled,     // 6
	)
	if err != nil {
		return nil, err
	}

	return &u, nil
}

// GetUserOrCreateByOAuth creates or retrieves user via OAuth provider.
func (s *SQLite) GetUserOrCreateByOAuth(
	provider string,
	providerID string,
	email string,
	username string,
	avatarURL string,
) (*User, error) {

	// Validate username from OAuth provider - if invalid, treat as empty
	if username != "" {
		if err := IsValidUsername(username); err != nil {
			username = "" // Treat invalid username as empty, user will be redirected to /me
		}
	}

	// First, check if we have an existing OAuth identity for this provider/providerID
	userID, err := s.GetUserByOAuthProviderID(provider, providerID)
	if err != nil {
		return nil, err
	}
	if userID != 0 {
		// User already has this OAuth identity
		// Still merge profile data in case OAuth provider now provides data it didn't before
		existingUser, err := s.MergeOAuthProfileData(userID, username, avatarURL)
		if err != nil {
			return nil, err
		}
		return existingUser, nil
	}

	// Check if a user with this email already exists (from magic link or another OAuth provider)
	if email != "" {
		emailUser, err := s.GetUserOrCreateByEmail(email)
		if err != nil {
			return nil, err
		}
		if emailUser != nil && emailUser.ID != 0 {
			// Email already exists, link this OAuth identity to the existing user
			const sqlInsertIdentity = `INSERT INTO identities (
                user_id,          -- 1
                provider,         -- 2
                provider_uid,     -- 3
                created_at,
                updated_at
            ) VALUES (
                ?,                 -- 1
                ?,                 -- 2
                ?,                 -- 3
                CURRENT_TIMESTAMP, -- created_at
                CURRENT_TIMESTAMP  -- updated_at
            );`

			err = s.Exec(
				sqlInsertIdentity,
				emailUser.ID, // 1
				provider,     // 2
				providerID,   // 3
			)
			if err != nil {
				return nil, err
			}

			// Update missing fields from OAuth data if they are empty in database
			emailUser, err = s.MergeOAuthProfileData(emailUser.ID, username, avatarURL)
			if err != nil {
				return nil, err
			}

			return emailUser, nil
		}
	}

	// No existing email or OAuth identity, create new user with conflict resolution for username
	actualUsername := username
	if username != "" {
		var err error
		actualUsername, err = s.GenerateUniqueUsername(username)
		if err != nil {
			return nil, err
		}
	}

	u := User{
		Username:  actualUsername,
		Email:     email,
		AvatarURL: avatarURL,
		Enabled:   false,
	}

	const sqlInsert = `INSERT INTO users (
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
            CURRENT_TIMESTAMP, -- created_at
            CURRENT_TIMESTAMP  -- updated_at
        )
        RETURNING
            id,                         -- 1
            reference_id,               -- 2
            COALESCE(username, ''),     -- 3
            email,                      -- 4
            COALESCE(avatar_url, ''),   -- 5
            enabled;` // 6

	// enabled is true only if BOTH username AND email are present
	// (we assume email is validated by OAuth provider if present)
	enabled := email != "" && actualUsername != ""

	err = s.QueryRowRW(
		sqlInsert,
		email,          // 1
		actualUsername, // 2
		avatarURL,      // 3
		enabled,        // 4
	).Scan(
		&u.ID,          // 1
		&u.ReferenceID, // 2
		&u.Username,    // 3
		&u.Email,       // 4
		&u.AvatarURL,   // 5
		&u.Enabled,     // 6
	)
	if err != nil {
		return nil, err
	}

	// Reload to get trigger-populated fields (reference_id)
	uFresh, err := s.GetUserByID(u.ID)
	if err != nil {
		return nil, err
	}

	//-----------------------------------------
	const sqlInsertIdentity = `INSERT INTO identities (
            user_id,          -- 1
            provider,         -- 2
            provider_uid,     -- 3
            created_at,
            updated_at
        ) VALUES (
            ?,                 -- 1
            ?,                 -- 2
            ?,                 -- 3
            CURRENT_TIMESTAMP, -- created_at
            CURRENT_TIMESTAMP  -- updated_at
        );`

	// Use the fresh user's ID for identity linking
	err = s.Exec(
		sqlInsertIdentity,
		uFresh.ID,  // 1
		provider,   // 2
		providerID, // 3
	)
	if err != nil {
		return nil, err
	}

	return uFresh, nil
}

func (s *SQLite) SaveFileMetadata(f *File) (*File, error) {
	if f == nil {
		return nil, errors.New("file metadata is required")
	}

	const sqlInsert = `INSERT INTO filemanager_files (
            user_id,               -- 1
            original_filename,     -- 2
            filename,              -- 3
            filesize,              -- 4
            filetype,              -- 5
            filehash,              -- 6
            filetag,               -- 7
            filedescription,       -- 8
            processed              -- 9
        ) VALUES (
            ?,                     -- 1
            ?,                     -- 2
            ?,                     -- 3
            ?,                     -- 4
            ?,                     -- 5
            ?,                     -- 6
            ?,                     -- 7
            ?,                     -- 8
            ?                      -- 9
        )
        RETURNING
            id,                    -- 1
            user_id,               -- 2
            original_filename,     -- 3
            filename,              -- 4
            filesize,              -- 5
            filetype,              -- 6
            filehash,              -- 7
            filetag,               -- 8
            filedescription,       -- 9
            processed,             -- 10
            created_at,            -- 11
            updated_at;` // 12

	var savedFile File
	err := s.QueryRowRW(
		sqlInsert,
		f.UserID,           // 1
		f.OriginalFilename, // 2
		f.Filename,         // 3
		f.Filesize,         // 4
		f.Filetype,         // 5
		f.Filehash,         // 6
		f.Filetag,          // 7
		f.Filedescription,  // 8
		f.Processed,        // 9
	).Scan(
		&savedFile.ID,               // 1
		&savedFile.UserID,           // 2
		&savedFile.OriginalFilename, // 3
		&savedFile.Filename,         // 4
		&savedFile.Filesize,         // 5
		&savedFile.Filetype,         // 6
		&savedFile.Filehash,         // 7
		&savedFile.Filetag,          // 8
		&savedFile.Filedescription,  // 9
		&savedFile.Processed,        // 10
		&savedFile.CreatedAt,        // 11
		&savedFile.UpdatedAt,        // 12
	)
	if err != nil {
		return nil, err
	}

	return &savedFile, nil
}

func (s *SQLite) GetFileByUserIDAndFilename(
	userID int64,
	filename string,
) (*File, error) {
	const sqlSelect = `SELECT
            id,                    -- 1
            user_id,               -- 2
            original_filename,     -- 3
            filename,              -- 4
            filesize,              -- 5
            filetype,              -- 6
            filehash,              -- 7
            filetag,               -- 8
            filedescription,       -- 9
            processed,             -- 10
            created_at,            -- 11
            updated_at             -- 12
    FROM filemanager_files
    WHERE user_id = ?     -- 1
    AND filename = ?      -- 2
    AND deleted = 0
        LIMIT 1;`

	var f File
	err := s.QueryRow(
		sqlSelect,
		userID,   // 1
		filename, // 2
	).Scan(
		&f.ID,               // 1
		&f.UserID,           // 2
		&f.OriginalFilename, // 3
		&f.Filename,         // 4
		&f.Filesize,         // 5
		&f.Filetype,         // 6
		&f.Filehash,         // 7
		&f.Filetag,          // 8
		&f.Filedescription,  // 9
		&f.Processed,        // 10
		&f.CreatedAt,        // 11
		&f.UpdatedAt,        // 12
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil // File not found
		}
		return nil, err
	}

	return &f, nil
}

// CreateMinimalUserForOAuthFallback creates a minimal user account with email only
// when OAuth signup fails. This allows the user to complete their profile at /me.
// Returns error if email is empty or if database operation fails.
func (s *SQLite) CreateMinimalUserForOAuthFallback(
	email string,
	avatarURL string,
) (*User, error) {
	email = strings.TrimSpace(email)
	if email == "" {
		return nil, errors.New("email is required for fallback user creation")
	}

	avatarURL = strings.TrimSpace(avatarURL)

	u := User{
		Username:  "", // User must complete this at /me
		Email:     email,
		AvatarURL: avatarURL,
		Enabled:   false, // Not enabled until username is set
	}

	const sqlInsert = `INSERT INTO users (
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
            CURRENT_TIMESTAMP, -- created_at
            CURRENT_TIMESTAMP  -- updated_at
        )
        RETURNING
            id,                         -- 1
            reference_id,               -- 2
            COALESCE(username, ''),     -- 3
            email,                      -- 4
            COALESCE(avatar_url, ''),  -- 5
            enabled;` // 6

	err := s.QueryRowRW(
		sqlInsert,
		email,     // 1
		nil,       // 2 - NULL username (will be set at /me)
		avatarURL, // 3
		false,     // 4 - not enabled
	).Scan(
		&u.ID,          // 1
		&u.ReferenceID, // 2
		&u.Username,    // 3
		&u.Email,       // 4
		&u.AvatarURL,   // 5
		&u.Enabled,     // 6
	)
	if err != nil {
		return nil, err
	}

	// Reload to get trigger-populated reference_id
	uf, err := s.GetUserByID(u.ID)
	if err != nil {
		return nil, err
	}

	return uf, nil
}

func (s *SQLite) GetFileByUserReferenceIDAndFilename(
	userRefID string,
	filename string,
) (*File, error) {
	const sqlSelect = `SELECT
            f.id,                    -- 1
            f.user_id,               -- 2
            f.original_filename,     -- 3
            f.filename,              -- 4
            f.filesize,              -- 5
            f.filetype,              -- 6
            f.filehash,              -- 7
            f.filetag,               -- 8
            f.filedescription,       -- 9
            f.processed,             -- 10
            f.created_at,            -- 11
            f.updated_at             -- 12
        FROM filemanager_files f
        JOIN users u ON f.user_id = u.id
    WHERE u.reference_id = ?  -- 1
    AND f.filename = ?        -- 2
    AND f.deleted = 0
        LIMIT 1;`

	var f File
	err := s.QueryRow(
		sqlSelect,
		userRefID, // 1
		filename,  // 2
	).Scan(
		&f.ID,               // 1
		&f.UserID,           // 2
		&f.OriginalFilename, // 3
		&f.Filename,         // 4
		&f.Filesize,         // 5
		&f.Filetype,         // 6
		&f.Filehash,         // 7
		&f.Filetag,          // 8
		&f.Filedescription,  // 9
		&f.Processed,        // 10
		&f.CreatedAt,        // 11
		&f.UpdatedAt,        // 12
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil // File not found
		}
		return nil, err
	}

	return &f, nil
}

// files, err := filemanager.ListFilesByUserID(u.ID, offset, limit)
func (s *SQLite) ListFilesByUserID(
	userID int64,
	offset int,
	limit int,
) ([]*File, error) {
	const sqlSelect = `SELECT
            id,                    -- 1
            user_id,               -- 2
            original_filename,     -- 3
            filename,              -- 4
            filesize,              -- 5
            filetype,              -- 6
            filehash,              -- 7
            filetag,               -- 8
            filedescription,       -- 9
            processed,             -- 10
            created_at,            -- 11
            updated_at             -- 12
    FROM filemanager_files
    WHERE user_id = ?        -- 1
    AND deleted = 0
        ORDER BY created_at DESC
        LIMIT ?                  -- 2
        OFFSET ?;                -- 3`
	rows, err := s.Query(
		sqlSelect,
		userID, // 1
		limit,  // 2
		offset, // 3
	)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			// Client aborted/canceled; do not log as error
			return nil, err
		}
		log.Println("ListFilesByUserID query error:", err)
		return nil, err
	}
	defer rows.Close()

	var files []*File
	for rows.Next() {
		var f File
		err := rows.Scan(
			&f.ID,               // 1
			&f.UserID,           // 2
			&f.OriginalFilename, // 3
			&f.Filename,         // 4
			&f.Filesize,         // 5
			&f.Filetype,         // 6
			&f.Filehash,         // 7
			&f.Filetag,          // 8
			&f.Filedescription,  // 9
			&f.Processed,        // 10
			&f.CreatedAt,        // 11
			&f.UpdatedAt,        // 12
		)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				// Client aborted/canceled; do not log as error
				return nil, err
			}
			log.Println("ListFilesByUserID scan error:", err)
			return nil, err
		}
		files = append(files, &f)
	}

	return files, nil
}

// ListFilesByUserIDSorted returns files for a user with explicit sort option.
// sort supports: "date_desc" (default) and "name_asc".
func (s *SQLite) ListFilesByUserIDSorted(
	userID int64,
	sort string,
	offset int,
	limit int,
) ([]*File, error) {
	orderBy := "created_at DESC"
	switch strings.ToLower(strings.TrimSpace(sort)) {
	case "name_asc":
		orderBy = "LOWER(original_filename) ASC, created_at DESC"
	default:
		orderBy = "created_at DESC"
	}

	query := fmt.Sprintf(`SELECT
            id,                    -- 1
            user_id,               -- 2
            original_filename,     -- 3
            filename,              -- 4
            filesize,              -- 5
            filetype,              -- 6
            filehash,              -- 7
            filetag,               -- 8
            filedescription,       -- 9
            processed,             -- 10
            created_at,            -- 11
            updated_at             -- 12
        FROM filemanager_files
        WHERE user_id = ? AND deleted = 0
        ORDER BY %s
        LIMIT ? OFFSET ?;`, orderBy)

	rows, err := s.Query(query, userID, limit, offset)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			// Client aborted/canceled; do not log as error
			return nil, err
		}
		log.Println("ListFilesByUserIDSorted query error:", err)
		return nil, err
	}
	defer rows.Close()

	var files []*File
	for rows.Next() {
		var f File
		if err := rows.Scan(
			&f.ID,
			&f.UserID,
			&f.OriginalFilename,
			&f.Filename,
			&f.Filesize,
			&f.Filetype,
			&f.Filehash,
			&f.Filetag,
			&f.Filedescription,
			&f.Processed,
			&f.CreatedAt,
			&f.UpdatedAt,
		); err != nil {
			if errors.Is(err, context.Canceled) {
				// Client aborted/canceled; do not log as error
				return nil, err
			}
			log.Println("ListFilesByUserIDSorted scan error:", err)
			return nil, err
		}
		files = append(files, &f)
	}
	return files, nil
}

// buildFTSQuery converts a raw user query into a safe FTS5 query by
// splitting on whitespace and joining tokens with AND and a trailing * for prefix match.
func buildFTSQuery(q string) string {
	q = strings.TrimSpace(q)
	if q == "" {
		return ""
	}
	parts := strings.Fields(q)
	tokens := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		// strip simple quotes to avoid breaking the MATCH syntax
		p = strings.ReplaceAll(p, "\"", "")
		p = strings.ReplaceAll(p, "'", "")
		tokens = append(tokens, p+"*")
	}
	if len(tokens) == 0 {
		return ""
	}
	// AND all tokens to narrow results
	return strings.Join(tokens, " ")
}

// SearchFilesByUserIDFTS performs a full-text search across original_filename, filename,
// filetag, and filedescription using the FTS virtual table.
// Returns paginated results for a given user.
// Sort supports: "date_desc" (default) and "name_asc".
func (s *SQLite) SearchFilesByUserIDFTS(
	userID int64,
	query string,
	sort string,
	offset int,
	limit int,
) ([]*File, error) {
	q := buildFTSQuery(query)
	if q == "" {
		// fallback to default listing if no query
		return s.ListFilesByUserID(userID, offset, limit)
	}

	orderBy := "f.created_at DESC"
	switch strings.ToLower(strings.TrimSpace(sort)) {
	case "name_asc":
		orderBy = "LOWER(f.original_filename) ASC, f.created_at DESC"
	default:
		orderBy = "f.created_at DESC"
	}

	const baseSelect = `SELECT
            f.id,                 -- 1
            f.user_id,            -- 2
            f.original_filename,  -- 3
            f.filename,           -- 4
            f.filesize,           -- 5
            f.filetype,           -- 6
            f.filehash,           -- 7
            f.filetag,            -- 8
            f.filedescription,    -- 9
            f.processed,          -- 10
            f.created_at,         -- 11
            f.updated_at          -- 12
        FROM filemanager_files f
        JOIN filemanager_files_fts ON filemanager_files_fts.rowid = f.id
        WHERE f.user_id = ? AND f.deleted = 0 AND filemanager_files_fts MATCH ?
        ORDER BY %s
        LIMIT ? OFFSET ?`

	querySQL := fmt.Sprintf(baseSelect, orderBy)

	rows, err := s.Query(querySQL, userID, q, limit, offset)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			// Client aborted/canceled; do not log as error
			return nil, err
		}
		log.Println("SearchFilesByUserIDFTS query error:", err)
		return nil, err
	}
	defer rows.Close()

	var files []*File
	for rows.Next() {
		var f File
		if err := rows.Scan(
			&f.ID, &f.UserID, &f.OriginalFilename, &f.Filename,
			&f.Filesize, &f.Filetype, &f.Filehash, &f.Filetag,
			&f.Filedescription, &f.Processed, &f.CreatedAt, &f.UpdatedAt,
		); err != nil {
			if errors.Is(err, context.Canceled) {
				// Client aborted/canceled; do not log as error
				return nil, err
			}
			log.Println("SearchFilesByUserIDFTS scan error:", err)
			return nil, err
		}
		files = append(files, &f)
	}

	return files, nil
}

// SoftDeleteFileByUserAndFilename marks a file as deleted (soft delete) for a specific user and filename.
// It does not remove file contents from disk; a background worker may perform physical deletion later.
func (s *SQLite) SoftDeleteFileByUserAndFilename(
	userID int64,
	filename string,
) error {
	const sqlUpdate = `UPDATE filemanager_files
            SET deleted = 1,
                updated_at = CURRENT_TIMESTAMP
            WHERE filename = ? AND user_id = ? AND deleted = 0`
	return s.Exec(sqlUpdate, filename, userID)
}

// UpdateFileMetadataByUserAndFilename updates the file description and tag for a file
// owned by the given user, only if it is not deleted.
func (s *SQLite) UpdateFileMetadataByUserAndFilename(
	userID int64,
	filename string,
	description string,
	tag string,
) error {
	const sqlUpdate = `UPDATE filemanager_files
            SET filedescription = ?,
                filetag = ?,
                updated_at = CURRENT_TIMESTAMP
            WHERE filename = ? AND user_id = ? AND deleted = 0`
	return s.Exec(sqlUpdate, description, tag, filename, userID)
}

// GetForumByExternalID retrieves a forum by its external ID.
func (s *SQLite) GetForumByExternalID(externalID string) (*Forum, error) {
	const sqlSelect = `SELECT
        f.id,                       -- 1
        f.external_id,              -- 2
        f.tenant_id,                -- 3
        f.workspace_id,             -- 4
        f.owner_user_id,            -- 5
        COALESCE(u.username, ''),   -- 6
        COALESCE(f.title, ''),      -- 7
        COALESCE(f.description, ''),-- 8
        COALESCE(f.image_url, ''),  -- 9
        f.created_at,               -- 10
        f.updated_at                -- 11
    FROM forum f
    LEFT JOIN users u ON f.owner_user_id = u.id
    WHERE f.external_id = ?  -- 1
    LIMIT 1`

	var f Forum

	err := s.QueryRow(
		sqlSelect,
		externalID, // 1
	).Scan(
		&f.ID,            // 1
		&f.ExternalID,    // 2
		&f.TenantID,      // 3
		&f.WorkspaceID,   // 4
		&f.OwnerUserID,   // 5
		&f.OwnerUserName, // 6
		&f.Title,         // 7
		&f.Description,   // 8
		&f.ImageURL,      // 9
		&f.CreatedAt,     // 10
		&f.UpdatedAt,     // 11
	)
	if err != nil {
		log.Printf("DEBUG: GetForumByExternalID query error for external_id=%s: %v", externalID, err)
		return nil, err
	}

	log.Printf("DEBUG: GetForumByExternalID found forum - id=%d, external_id=%s, title=%s", f.ID, f.ExternalID, f.Title)
	return &f, nil
}

// CreateThread creates a new thread in a forum.
func (s *SQLite) CreateThread(
	forumID int64,
	ownerUserID int64,
	title string,
	externalID string,
	imageURL string,
) (*Thread, error) {
	const sqlInsert = `INSERT INTO forum_threads (
        external_id,       -- 1
        forum_id,          -- 2
        owner_user_id,     -- 3
        title,             -- 4
        image_url,         -- 5
        created_at,
        updated_at
    ) VALUES (
        ?,                 -- 1
        ?,                 -- 2
        ?,                 -- 3
        ?,                 -- 4
        ?,                 -- 5
        CURRENT_TIMESTAMP, -- created_at
        CURRENT_TIMESTAMP  -- updated_at
    )
    RETURNING
        id,                        -- 1
        external_id,               -- 2
        forum_id,                  -- 3
        owner_user_id,             -- 4
        COALESCE(title, ''),       -- 5
        COALESCE(image_url, ''),   -- 6
        created_at,                -- 7
        updated_at` // 8

	var t Thread

	err := s.QueryRowRW(
		sqlInsert,
		externalID,  // 1
		forumID,     // 2
		ownerUserID, // 3
		title,       // 4
		imageURL,    // 5
	).Scan(
		&t.ID,          // 1
		&t.ExternalID,  // 2
		&t.ForumID,     // 3
		&t.OwnerUserID, // 4
		&t.Title,       // 5
		&t.ImageURL,    // 6
		&t.CreatedAt,   // 7
		&t.UpdatedAt,   // 8
	)
	if err != nil {
		log.Printf("ERROR: CreateThread INSERT failed for forumID=%d, ownerID=%d: %v", forumID, ownerUserID, err)
		return nil, err
	}

	log.Printf("DEBUG: CreateThread INSERT succeeded, got thread id=%d", t.ID)

	// Fetch the username from the users table
	const sqlSelectUsername = `SELECT COALESCE(username, '') FROM users WHERE id = ? LIMIT 1`
	err = s.QueryRow(sqlSelectUsername, ownerUserID).Scan(&t.OwnerUserName)
	if err != nil {
		// If we can't fetch the username, just leave it empty (user might not exist yet)
		log.Printf("DEBUG: CreateThread username lookup failed for ownerID=%d: %v (will use empty string)", ownerUserID, err)
		t.OwnerUserName = ""
	}

	log.Printf("DEBUG: CreateThread complete - id=%d, external_id=%s, forum_id=%d", t.ID, t.ExternalID, t.ForumID)
	return &t, nil
}

// CreatePost creates a new post in a thread.
func (s *SQLite) CreatePost(
	threadID int64,
	ownerUserID int64,
	content string,
	contentHTML string,
	externalID string,
	parentPostID *int64,
) (*Post, error) {
	const sqlInsert = `INSERT INTO forum_posts (
        external_id,       -- 1
        thread_id,         -- 2
        owner_user_id,     -- 3
        content,           -- 4
        content_html,      -- 5
        parent_post_id,    -- 6
        created_at,
        updated_at
    ) VALUES (
        ?,                 -- 1
        ?,                 -- 2
        ?,                 -- 3
        ?,                 -- 4
        ?,                 -- 5
        ?,                 -- 6
        CURRENT_TIMESTAMP, -- created_at
        CURRENT_TIMESTAMP  -- updated_at
    )
    RETURNING
        id,                         -- 1
        external_id,                -- 2
        thread_id,                  -- 3
        owner_user_id,              -- 4
        COALESCE(content, ''),      -- 5
        COALESCE(content_html, ''), -- 6
        parent_post_id,             -- 7
        created_at,                 -- 8
        updated_at` // 9

	var p Post

	err := s.QueryRowRW(
		sqlInsert,
		externalID,   // 1
		threadID,     // 2
		ownerUserID,  // 3
		content,      // 4
		contentHTML,  // 5
		parentPostID, // 6
	).Scan(
		&p.ID,           // 1
		&p.ExternalID,   // 2
		&p.ThreadID,     // 3
		&p.OwnerUserID,  // 4
		&p.Content,      // 5
		&p.ContentHTML,  // 6
		&p.ParentPostID, // 7
		&p.CreatedAt,    // 8
		&p.UpdatedAt,    // 9
	)
	if err != nil {
		log.Printf("ERROR: CreatePost INSERT failed for threadID=%d, ownerID=%d: %v", threadID, ownerUserID, err)
		return nil, err
	}

	log.Printf("DEBUG: CreatePost INSERT succeeded - id=%d, external_id=%s, thread_id=%d", p.ID, p.ExternalID, p.ThreadID)
	return &p, nil
}

// UpdatePost updates an existing post's content.
func (s *SQLite) UpdatePost(
	postID int64,
	content string,
	contentHTML string,
) (*Post, error) {
	const sqlUpdate = `UPDATE forum_posts
    SET
        content = ?,               -- 1
        content_html = ?,          -- 2
        updated_at = CURRENT_TIMESTAMP
    WHERE id = ?                   -- 3
    RETURNING
        id,                         -- 1
        external_id,                -- 2
        thread_id,                  -- 3
        owner_user_id,              -- 4
        COALESCE(content, ''),      -- 5
        COALESCE(content_html, ''), -- 6
        parent_post_id,             -- 7
        created_at,                 -- 8
        updated_at` // 9

	var p Post

	err := s.QueryRowRW(
		sqlUpdate,
		content,     // 1
		contentHTML, // 2
		postID,      // 3
	).Scan(
		&p.ID,           // 1
		&p.ExternalID,   // 2
		&p.ThreadID,     // 3
		&p.OwnerUserID,  // 4
		&p.Content,      // 5
		&p.ContentHTML,  // 6
		&p.ParentPostID, // 7
		&p.CreatedAt,    // 8
		&p.UpdatedAt,    // 9
	)
	if err != nil {
		return nil, err
	}

	return &p, nil
}

// DeletePost deletes a post by ID.
func (s *SQLite) DeletePost(postID int64) error {
	const sqlDelete = `DELETE FROM forum_posts
        WHERE id = ?` // 1

	err := s.Exec(
		sqlDelete,
		postID, // 1
	)
	return err
}

// GetThreadByExternalID retrieves a thread by its external ID.
func (s *SQLite) GetThreadByExternalID(externalID string) (*Thread, error) {
	const sqlSelect = `SELECT
        t.id,                      -- 1
        t.external_id,             -- 2
        t.forum_id,                -- 3
        t.owner_user_id,           -- 4
        COALESCE(t.title, ''),     -- 5
        COALESCE(t.image_url, ''), -- 6
        COALESCE(u.username, ''),  -- 7
        t.created_at,              -- 8
        t.updated_at               -- 9
    FROM forum_threads t
    LEFT JOIN users u ON t.owner_user_id = u.id
    WHERE t.external_id = ?  -- 1
    LIMIT 1`

	var t Thread

	err := s.QueryRow(
		sqlSelect,
		externalID, // 1
	).Scan(
		&t.ID,            // 1
		&t.ExternalID,    // 2
		&t.ForumID,       // 3
		&t.OwnerUserID,   // 4
		&t.Title,         // 5
		&t.ImageURL,      // 6
		&t.OwnerUserName, // 7
		&t.CreatedAt,     // 8
		&t.UpdatedAt,     // 9
	)
	if err != nil {
		return nil, err
	}

	return &t, nil
}

// ListThreadsByForumID retrieves threads in a forum with pagination.
func (s *SQLite) ListThreadsByForumID(
	forumID int64,
	offset int,
	limit int,
) ([]*Thread, error) {
	const sqlSelect = `SELECT
        t.id,                      -- 1
        t.external_id,             -- 2
        t.forum_id,                -- 3
        t.owner_user_id,           -- 4
        COALESCE(t.title, ''),     -- 5
        COALESCE(t.image_url, ''), -- 6
        COALESCE(u.username, ''),  -- 7
        t.created_at,              -- 8
        t.updated_at               -- 9
    FROM forum_threads t
    LEFT JOIN users u ON t.owner_user_id = u.id
    WHERE t.forum_id = ?       -- 1
    ORDER BY t.created_at DESC
    LIMIT ?                    -- 2
    OFFSET ?` // 3

	rows, err := s.Query(
		sqlSelect,
		forumID, // 1
		limit,   // 2
		offset,  // 3
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var threads []*Thread
	for rows.Next() {
		var t Thread
		if err := rows.Scan(
			&t.ID,            // 1
			&t.ExternalID,    // 2
			&t.ForumID,       // 3
			&t.OwnerUserID,   // 4
			&t.Title,         // 5
			&t.ImageURL,      // 6
			&t.OwnerUserName, // 7
			&t.CreatedAt,     // 8
			&t.UpdatedAt,     // 9
		); err != nil {
			return nil, err
		}
		threads = append(threads, &t)
	}

	if err = rows.Err(); err != nil {
		return nil, err
	}

	return threads, nil
}

// UpdateThread updates an existing thread's title, image_url, and updated_at timestamp.
func (s *SQLite) UpdateThread(
	externalID string,
	title string,
	imageURL string,
) (*Thread, error) {
	const sqlUpdate = `UPDATE forum_threads
    SET
        title = ?,                 -- 1
        image_url = ?,             -- 2
        updated_at = CURRENT_TIMESTAMP
    WHERE external_id = ?          -- 3
    RETURNING
        id,                        -- 1
        external_id,               -- 2
        forum_id,                  -- 3
        owner_user_id,             -- 4
        COALESCE(title, ''),       -- 5
        COALESCE(image_url, ''),   -- 6
        created_at,                -- 7
        updated_at` // 8

	var t Thread

	err := s.QueryRowRW(
		sqlUpdate,
		title,      // 1
		imageURL,   // 2
		externalID, // 3
	).Scan(
		&t.ID,          // 1
		&t.ExternalID,  // 2
		&t.ForumID,     // 3
		&t.OwnerUserID, // 4
		&t.Title,       // 5
		&t.ImageURL,    // 6
		&t.CreatedAt,   // 7
		&t.UpdatedAt,   // 8
	)
	if err != nil {
		return nil, err
	}

	return &t, nil
}

// GetPostsByThreadID retrieves posts in a thread.
func (s *SQLite) GetPostsByThreadID(threadID int64) ([]*Post, error) {
	const sqlSelect = `SELECT
        id,                         -- 1
        external_id,                -- 2
        thread_id,                  -- 3
        owner_user_id,              -- 4
        COALESCE(content, ''),      -- 5
        COALESCE(content_html, ''), -- 6
        parent_post_id,             -- 7
        created_at,                 -- 8
        updated_at                  -- 9
    FROM forum_posts
    WHERE thread_id = ?             -- 1
    AND parent_post_id IS NULL
    ORDER BY created_at ASC`

	rows, err := s.Query(
		sqlSelect,
		threadID, // 1
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var posts []*Post
	for rows.Next() {
		var p Post
		if err := rows.Scan(
			&p.ID,           // 1
			&p.ExternalID,   // 2
			&p.ThreadID,     // 3
			&p.OwnerUserID,  // 4
			&p.Content,      // 5
			&p.ContentHTML,  // 6
			&p.ParentPostID, // 7
			&p.CreatedAt,    // 8
			&p.UpdatedAt,    // 9
		); err != nil {
			return nil, err
		}
		posts = append(posts, &p)
	}

	if err = rows.Err(); err != nil {
		return nil, err
	}

	return posts, nil
}

// ListForums retrieves all forums with pagination.
func (s *SQLite) ListForums(offset int, limit int) ([]*Forum, error) {
	const sqlSelect = `SELECT
        f.id,                        -- 1
        f.external_id,               -- 2
        f.tenant_id,                 -- 3
        f.workspace_id,              -- 4
        f.owner_user_id,             -- 5
        COALESCE(u.username, ''),    -- 6
        COALESCE(f.title, ''),       -- 7
        COALESCE(f.description, ''), -- 8
        COALESCE(f.image_url, ''),   -- 9
        f.created_at,                -- 10
        f.updated_at                 -- 11
    FROM forum f
    LEFT JOIN users u ON f.owner_user_id = u.id
    ORDER BY f.created_at DESC
    LIMIT ?                      -- 1
    OFFSET ?` // 2

	rows, err := s.Query(
		sqlSelect,
		limit,  // 1
		offset, // 2
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var forums []*Forum
	for rows.Next() {
		var f Forum
		if err := rows.Scan(
			&f.ID,            // 1
			&f.ExternalID,    // 2
			&f.TenantID,      // 3
			&f.WorkspaceID,   // 4
			&f.OwnerUserID,   // 5
			&f.OwnerUserName, // 6
			&f.Title,         // 7
			&f.Description,   // 8
			&f.ImageURL,      // 9
			&f.CreatedAt,     // 10
			&f.UpdatedAt,     // 11
		); err != nil {
			return nil, err
		}
		forums = append(forums, &f)
	}

	if err = rows.Err(); err != nil {
		return nil, err
	}

	return forums, nil
}

// UpdateForum updates an existing forum.
func (s *SQLite) UpdateForum(
	externalID string,
	title string,
	description string,
	imageURL string,
) (*Forum, error) {
	const sqlUpdate = `UPDATE forum
    SET
        title = ?,                 -- 1
        description = ?,           -- 2
        image_url = ?,             -- 3
        updated_at = CURRENT_TIMESTAMP
    WHERE external_id = ?          -- 4
    RETURNING
        id,                         -- 1
        external_id,                -- 2
        tenant_id,                  -- 3
        workspace_id,               -- 4
        owner_user_id,              -- 5
        COALESCE(title, ''),        -- 6
        COALESCE(description, ''),  -- 7
        COALESCE(image_url, ''),    -- 8
        created_at,                 -- 9
        updated_at` // 10

	var f Forum

	err := s.QueryRowRW(
		sqlUpdate,
		title,       // 1
		description, // 2
		imageURL,    // 3
		externalID,  // 4
	).Scan(
		&f.ID,          // 1
		&f.ExternalID,  // 2
		&f.TenantID,    // 3
		&f.WorkspaceID, // 4
		&f.OwnerUserID, // 5
		&f.Title,       // 6
		&f.Description, // 7
		&f.ImageURL,    // 8
		&f.CreatedAt,   // 9
		&f.UpdatedAt,   // 10
	)
	if err != nil {
		return nil, err
	}

	return &f, nil
}

// CreateForum creates a new forum.
func (s *SQLite) CreateForum(
	externalID string,
	tenantID int64,
	workspaceID int64,
	ownerUserID int64,
	title string,
	description string,
	imageURL string,
) (*Forum, error) {
	const sqlInsert = `INSERT INTO forum (
        external_id,       -- 1
        tenant_id,         -- 2
        workspace_id,      -- 3
        owner_user_id,     -- 4
        title,             -- 5
        description,       -- 6
        image_url,         -- 7
        created_at,
        updated_at
    ) VALUES (
        ?,                 -- 1
        ?,                 -- 2
        ?,                 -- 3
        ?,                 -- 4
        ?,                 -- 5
        ?,                 -- 6
        ?,                 -- 7
        CURRENT_TIMESTAMP, -- created_at
        CURRENT_TIMESTAMP  -- updated_at
    )
    RETURNING
        id,                         -- 1
        external_id,                -- 2
        tenant_id,                  -- 3
        workspace_id,               -- 4
        owner_user_id,              -- 5
        COALESCE(title, ''),        -- 6
        COALESCE(description, ''),  -- 7
        COALESCE(image_url, ''),    -- 8
        created_at,                 -- 9
        updated_at` // 10

	var f Forum

	err := s.QueryRowRW(
		sqlInsert,
		externalID,  // 1
		tenantID,    // 2
		workspaceID, // 3
		ownerUserID, // 4
		title,       // 5
		description, // 6
		imageURL,    // 7
	).Scan(
		&f.ID,          // 1
		&f.ExternalID,  // 2
		&f.TenantID,    // 3
		&f.WorkspaceID, // 4
		&f.OwnerUserID, // 5
		&f.Title,       // 6
		&f.Description, // 7
		&f.ImageURL,    // 8
		&f.CreatedAt,   // 9
		&f.UpdatedAt,   // 10
	)
	if err != nil {
		return nil, err
	}

	// Fetch the username from the users table
	const sqlSelectUsername = `SELECT COALESCE(username, '') FROM users WHERE id = ? LIMIT 1`
	err = s.QueryRow(sqlSelectUsername, ownerUserID).Scan(&f.OwnerUserName)
	if err != nil {
		// If we can't fetch the username, just leave it empty (user might not exist yet)
		f.OwnerUserName = ""
	}

	return &f, nil
}
