// Package db provides SQLite access using modernc.org/sqlite (no CGO).
// Goals: performance, concurrency and predictability with minimal dependencies.
// - Separate pools: one writer (RW) and many readers (RO).
// - WAL + synchronous=NORMAL + busy_timeout.
// - Short transactions with timeouts per operation (not on Begin).
// - WAL checkpoint on Close() for hygiene.
package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"time"

	_ "modernc.org/sqlite"

	"edev/log"
	"edev/utils"
)

// Storage keeps a global handle for convenience (preserves your original pattern).
var Storage *SQLite

// SQLite holds separate read/write pools.
type SQLite struct {
	rw *sql.DB // single-writer pool
	ro *sql.DB // read-only pool
}

// Transaction wraps a write transaction.
type Transaction struct {
	tx *sql.Tx
}

// Row wraps sql.Row so that the timeout context is canceled only after Scan or Err is invoked.
type Row struct {
	row    *sql.Row
	cancel context.CancelFunc
	once   sync.Once
	err    error
}

func newRow(row *sql.Row, cancel context.CancelFunc) *Row {
	return &Row{row: row, cancel: cancel}
}

func errorRow(err error) *Row {
	return &Row{err: err}
}

func (r *Row) release() {
	if r == nil {
		return
	}
	r.once.Do(func() {
		if r.cancel != nil {
			r.cancel()
		}
	})
}

// Scan delegates to the underlying sql.Row while ensuring the timeout context is released.
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
	defer r.release()
	return r.row.Scan(dest...)
}

// Err mirrors (*sql.Row).Err and releases the timeout context.
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
	defer r.release()
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
// Uses config.Cfg.DatabaseURL as the SQLite path/URI; defaults to "app.db".
func New() (*SQLite, error) {
	path := "edev.db"
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
	if err := pingWithTimeout(rw, defaultWriteOpTimeout); err != nil {
		utils.Closer(rw)
		return nil, fmt.Errorf("ping RW: %w", err)
	}
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
	if err := pingWithTimeout(ro, defaultReadOpTimeout); err != nil {
		utils.Closer(ro)
		utils.Closer(s.rw)
		return nil, fmt.Errorf("ping RO: %w", err)
	}
	s.ro = ro

	return s, nil
}

func pingWithTimeout(db *sql.DB, d time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	return db.PingContext(ctx)
}

// BeginTransaction starts a write transaction.
//
// IMPORTANT: we intentionally DO NOT set a timeout context on BeginTx itself.
// Timeouts are enforced on the subsequent Exec/Query* calls, not on Begin.
// This avoids "transaction already committed/rolled back" when an early timeout fires.
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
	ctx, cancel := context.WithTimeout(context.Background(), defaultWriteOpTimeout)
	defer cancel()
	_, err := t.tx.ExecContext(ctx, query, args...)
	return err
}

// Query runs a SELECT inside the transaction (consistent view).
func (t *Transaction) Query(query string, args ...any) (*sql.Rows, error) {
	if t == nil || t.tx == nil {
		return nil, errors.New("nil tx")
	}
	ctx, cancel := context.WithTimeout(context.Background(), defaultReadOpTimeout)
	defer cancel()
	return t.tx.QueryContext(ctx, query, args...)
}

// QueryRow returns a single row inside the transaction.
func (t *Transaction) QueryRow(query string, args ...any) *Row {
	if t == nil || t.tx == nil {
		return errorRow(errors.New("nil tx"))
	}
	ctx, cancel := context.WithTimeout(context.Background(), defaultReadOpTimeout)
	return newRow(t.tx.QueryRowContext(ctx, query, args...), cancel)
}

// Exec executes a write statement on the RW pool (outside explicit transactions).
func (s *SQLite) Exec(query string, args ...any) error {
	if s == nil || s.rw == nil {
		return errors.New("db not initialized")
	}
	ctx, cancel := context.WithTimeout(context.Background(), defaultWriteOpTimeout)
	defer cancel()
	_, err := s.rw.ExecContext(ctx, query, args...)
	return err
}

// Query executes a SELECT on the RO pool (parallel reads).
func (s *SQLite) Query(query string, args ...any) (*sql.Rows, error) {
	if s == nil || s.ro == nil {
		return nil, errors.New("db not initialized")
	}
	ctx, cancel := context.WithTimeout(context.Background(), defaultReadOpTimeout)
	defer cancel()
	return s.ro.QueryContext(ctx, query, args...)
}

// QueryRow executes a single-row SELECT on the RO pool.
func (s *SQLite) QueryRow(query string, args ...any) *Row {
	if s == nil || s.ro == nil {
		return errorRow(errors.New("db not initialized"))
	}
	ctx, cancel := context.WithTimeout(context.Background(), defaultReadOpTimeout)
	return newRow(s.ro.QueryRowContext(ctx, query, args...), cancel)
}

// QueryRW allows SELECT using the RW pool (rarely needed).
func (s *SQLite) QueryRW(query string, args ...any) (*sql.Rows, error) {
	if s == nil || s.rw == nil {
		return nil, errors.New("db not initialized")
	}
	ctx, cancel := context.WithTimeout(context.Background(), defaultReadOpTimeout)
	defer cancel()
	return s.rw.QueryContext(ctx, query, args...)
}

// CheckpointWAL triggers a WAL checkpoint with TRUNCATE.
func (s *SQLite) CheckpointWAL() error {
	if s == nil || s.rw == nil {
		return errors.New("db not initialized")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := s.rw.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`)
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
