package db

import (
	"database/sql"
	"edev/utils"
	"fmt"
	"path/filepath"
	"runtime"
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
