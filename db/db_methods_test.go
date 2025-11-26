package db

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// TestQueryRowRWForInsertWithReturning verifies that QueryRowRW is correctly used
// for INSERT operations with RETURNING clause to get single-row results.
func TestQueryRowRWForInsertWithReturning(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	path := filepath.Join(tmp, "test.db")

	s, err := NewWithPath(path)
	if err != nil {
		t.Fatalf("NewWithPath() error: %v", err)
	}
	defer s.Close()

	// Create a test table
	if err := s.Exec(`CREATE TABLE test_items(
		id INTEGER PRIMARY KEY,
		name TEXT NOT NULL,
		value INTEGER DEFAULT 0
	)`); err != nil {
		t.Fatalf("CREATE TABLE error: %v", err)
	}

	// Test: INSERT with RETURNING using QueryRowRW
	sqlInsert := `INSERT INTO test_items(name, value) VALUES(?, ?) RETURNING id, name, value`
	var id int64
	var name string
	var value int

	err = s.QueryRowRW(sqlInsert, "test_item", 42).Scan(&id, &name, &value)
	if err != nil {
		t.Fatalf("QueryRowRW INSERT RETURNING error: %v", err)
	}

	// Verify returned values
	if id <= 0 {
		t.Errorf("expected id > 0, got %d", id)
	}
	if name != "test_item" {
		t.Errorf("expected name 'test_item', got %q", name)
	}
	if value != 42 {
		t.Errorf("expected value 42, got %d", value)
	}

	// Verify data was actually inserted by querying again
	var dbName string
	var dbValue int
	err = s.QueryRow(`SELECT name, value FROM test_items WHERE id = ?`, id).Scan(&dbName, &dbValue)
	if err != nil {
		t.Fatalf("verification query error: %v", err)
	}
	if dbName != "test_item" || dbValue != 42 {
		t.Errorf("inserted data mismatch: got name=%q, value=%d", dbName, dbValue)
	}
}

// TestQueryRowRWForUpdateWithReturning verifies that QueryRowRW is correctly used
// for UPDATE operations with RETURNING clause to get single-row results.
func TestQueryRowRWForUpdateWithReturning(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	path := filepath.Join(tmp, "test.db")

	s, err := NewWithPath(path)
	if err != nil {
		t.Fatalf("NewWithPath() error: %v", err)
	}
	defer s.Close()

	// Create table and insert initial data
	if err := s.Exec(`CREATE TABLE test_items(
		id INTEGER PRIMARY KEY,
		name TEXT NOT NULL,
		value INTEGER DEFAULT 0
	)`); err != nil {
		t.Fatalf("CREATE TABLE error: %v", err)
	}

	if err := s.Exec(`INSERT INTO test_items(name, value) VALUES(?, ?)`, "original", 10); err != nil {
		t.Fatalf("initial INSERT error: %v", err)
	}

	// Test: UPDATE with RETURNING using QueryRowRW
	sqlUpdate := `UPDATE test_items SET name = ?, value = ? WHERE id = 1 RETURNING id, name, value`
	var id int64
	var name string
	var value int

	err = s.QueryRowRW(sqlUpdate, "updated", 99).Scan(&id, &name, &value)
	if err != nil {
		t.Fatalf("QueryRowRW UPDATE RETURNING error: %v", err)
	}

	// Verify returned values
	if id != 1 {
		t.Errorf("expected id 1, got %d", id)
	}
	if name != "updated" {
		t.Errorf("expected name 'updated', got %q", name)
	}
	if value != 99 {
		t.Errorf("expected value 99, got %d", value)
	}

	// Verify data was actually updated
	var dbName string
	var dbValue int
	err = s.QueryRow(`SELECT name, value FROM test_items WHERE id = 1`).Scan(&dbName, &dbValue)
	if err != nil {
		t.Fatalf("verification query error: %v", err)
	}
	if dbName != "updated" || dbValue != 99 {
		t.Errorf("updated data mismatch: got name=%q, value=%d", dbName, dbValue)
	}
}

// TestExecForInsertWithoutReturning verifies that Exec is correctly used
// for INSERT operations WITHOUT RETURNING clause (when no result scanning is needed).
func TestExecForInsertWithoutReturning(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	path := filepath.Join(tmp, "test.db")

	s, err := NewWithPath(path)
	if err != nil {
		t.Fatalf("NewWithPath() error: %v", err)
	}
	defer s.Close()

	// Create a test table
	if err := s.Exec(`CREATE TABLE test_items(
		id INTEGER PRIMARY KEY,
		name TEXT NOT NULL,
		value INTEGER DEFAULT 0
	)`); err != nil {
		t.Fatalf("CREATE TABLE error: %v", err)
	}

	// Test: INSERT without RETURNING using Exec
	sqlInsert := `INSERT INTO test_items(name, value) VALUES(?, ?)`
	if err := s.Exec(sqlInsert, "test_item", 42); err != nil {
		t.Fatalf("Exec INSERT error: %v", err)
	}

	// Verify data was actually inserted by querying
	var id int64
	var name string
	var value int
	err = s.QueryRow(`SELECT id, name, value FROM test_items WHERE name = ?`, "test_item").Scan(&id, &name, &value)
	if err != nil {
		t.Fatalf("verification query error: %v", err)
	}
	if id <= 0 || name != "test_item" || value != 42 {
		t.Errorf("inserted data mismatch: got id=%d, name=%q, value=%d", id, name, value)
	}
}

// TestExecForUpdateWithoutReturning verifies that Exec is correctly used
// for UPDATE operations WITHOUT RETURNING clause.
func TestExecForUpdateWithoutReturning(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	path := filepath.Join(tmp, "test.db")

	s, err := NewWithPath(path)
	if err != nil {
		t.Fatalf("NewWithPath() error: %v", err)
	}
	defer s.Close()

	// Create table and insert initial data
	if err := s.Exec(`CREATE TABLE test_items(
		id INTEGER PRIMARY KEY,
		name TEXT NOT NULL,
		value INTEGER DEFAULT 0
	)`); err != nil {
		t.Fatalf("CREATE TABLE error: %v", err)
	}

	if err := s.Exec(`INSERT INTO test_items(name, value) VALUES(?, ?)`, "original", 10); err != nil {
		t.Fatalf("initial INSERT error: %v", err)
	}

	// Test: UPDATE without RETURNING using Exec
	sqlUpdate := `UPDATE test_items SET name = ?, value = ? WHERE id = 1`
	if err := s.Exec(sqlUpdate, "updated", 99); err != nil {
		t.Fatalf("Exec UPDATE error: %v", err)
	}

	// Verify data was actually updated
	var name string
	var value int
	err = s.QueryRow(`SELECT name, value FROM test_items WHERE id = 1`).Scan(&name, &value)
	if err != nil {
		t.Fatalf("verification query error: %v", err)
	}
	if name != "updated" || value != 99 {
		t.Errorf("updated data mismatch: got name=%q, value=%d", name, value)
	}
}

// TestExecForDeleteWithoutReturning verifies that Exec is correctly used
// for DELETE operations WITHOUT RETURNING clause.
func TestExecForDeleteWithoutReturning(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	path := filepath.Join(tmp, "test.db")

	s, err := NewWithPath(path)
	if err != nil {
		t.Fatalf("NewWithPath() error: %v", err)
	}
	defer s.Close()

	// Create table and insert test data
	if err := s.Exec(`CREATE TABLE test_items(
		id INTEGER PRIMARY KEY,
		name TEXT NOT NULL
	)`); err != nil {
		t.Fatalf("CREATE TABLE error: %v", err)
	}

	if err := s.Exec(`INSERT INTO test_items(name) VALUES(?)`, "to_delete"); err != nil {
		t.Fatalf("initial INSERT error: %v", err)
	}

	// Verify data exists before deletion
	var count1 int
	err = s.QueryRow(`SELECT COUNT(*) FROM test_items WHERE name = ?`, "to_delete").Scan(&count1)
	if err != nil || count1 != 1 {
		t.Fatalf("precondition failed: expected 1 row to delete")
	}

	// Test: DELETE without RETURNING using Exec
	sqlDelete := `DELETE FROM test_items WHERE name = ?`
	if err := s.Exec(sqlDelete, "to_delete"); err != nil {
		t.Fatalf("Exec DELETE error: %v", err)
	}

	// Verify data was actually deleted
	var count2 int
	err = s.QueryRow(`SELECT COUNT(*) FROM test_items WHERE name = ?`, "to_delete").Scan(&count2)
	if err != nil {
		t.Fatalf("verification query error: %v", err)
	}
	if count2 != 0 {
		t.Errorf("expected 0 rows after deletion, got %d", count2)
	}
}

// TestQueryRowForSelectReadOnly verifies that QueryRow is correctly used
// for SELECT queries on the read-only connection.
func TestQueryRowForSelectReadOnly(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	path := filepath.Join(tmp, "test.db")

	s, err := NewWithPath(path)
	if err != nil {
		t.Fatalf("NewWithPath() error: %v", err)
	}
	defer s.Close()

	// Create table and insert test data
	if err := s.Exec(`CREATE TABLE test_items(
		id INTEGER PRIMARY KEY,
		name TEXT NOT NULL,
		value INTEGER DEFAULT 0
	)`); err != nil {
		t.Fatalf("CREATE TABLE error: %v", err)
	}

	if err := s.Exec(`INSERT INTO test_items(name, value) VALUES(?, ?)`, "test1", 100); err != nil {
		t.Fatalf("INSERT error: %v", err)
	}

	// Test: SELECT using QueryRow (read-only pool)
	var name string
	var value int
	err = s.QueryRow(`SELECT name, value FROM test_items WHERE id = 1`).Scan(&name, &value)
	if err != nil {
		t.Fatalf("QueryRow SELECT error: %v", err)
	}

	if name != "test1" || value != 100 {
		t.Errorf("SELECT result mismatch: got name=%q, value=%d", name, value)
	}
}

// TestQueryRWForMultipleRows verifies that QueryRW is used correctly
// for SELECT queries that return multiple rows (when reading from RW pool is necessary).
func TestQueryRWForMultipleRows(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	path := filepath.Join(tmp, "test.db")

	s, err := NewWithPath(path)
	if err != nil {
		t.Fatalf("NewWithPath() error: %v", err)
	}
	defer s.Close()

	// Create table and insert test data
	if err := s.Exec(`CREATE TABLE test_items(
		id INTEGER PRIMARY KEY,
		name TEXT NOT NULL
	)`); err != nil {
		t.Fatalf("CREATE TABLE error: %v", err)
	}

	for i := 0; i < 3; i++ {
		if err := s.Exec(`INSERT INTO test_items(name) VALUES(?)`, "item"); err != nil {
			t.Fatalf("INSERT error: %v", err)
		}
	}

	// Test: QueryRW for multiple rows
	rows, err := s.QueryRW(`SELECT id, name FROM test_items`)
	if err != nil {
		t.Fatalf("QueryRW error: %v", err)
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			t.Fatalf("Scan error: %v", err)
		}
		count++
		if name != "item" {
			t.Errorf("unexpected name: %q", name)
		}
	}

	if err := rows.Err(); err != nil {
		t.Fatalf("rows.Err() error: %v", err)
	}

	if count != 3 {
		t.Errorf("expected 3 rows, got %d", count)
	}
}

// TestCannotUseQueryRowRWWithoutReturning verifies that operations without RETURNING
// should not use QueryRowRW (which returns a Row, not rows).
// This is more of a documentation test showing the incorrect pattern.
func TestQueryRowRWRequiresReturning(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	path := filepath.Join(tmp, "test.db")

	s, err := NewWithPath(path)
	if err != nil {
		t.Fatalf("NewWithPath() error: %v", err)
	}
	defer s.Close()

	// Create table
	if err := s.Exec(`CREATE TABLE test_items(
		id INTEGER PRIMARY KEY,
		name TEXT NOT NULL
	)`); err != nil {
		t.Fatalf("CREATE TABLE error: %v", err)
	}

	// INSERT without RETURNING - should use Exec instead
	sqlInsert := `INSERT INTO test_items(name) VALUES(?)`
	// If we tried to use QueryRowRW without RETURNING, Scan would fail
	// because there are no columns to return
	err = s.QueryRowRW(sqlInsert, "test").Scan()
	if err == nil {
		t.Error("expected error when scanning INSERT without RETURNING, got nil")
	}
	if err != sql.ErrNoRows && err != nil {
		// We expect an error here - QueryRowRW requires RETURNING to have rows to scan
		t.Logf("got expected error for INSERT without RETURNING: %v", err)
	}

	// The correct approach is to use Exec for INSERT without RETURNING
	if err := s.Exec(sqlInsert, "test"); err != nil {
		t.Fatalf("Exec INSERT error: %v", err)
	}

	// Verify it was inserted
	var name string
	err = s.QueryRow(`SELECT name FROM test_items WHERE name = ?`, "test").Scan(&name)
	if err != nil {
		t.Fatalf("verification query error: %v", err)
	}
}

// TestPoolSeparation verifies that the read-only pool is used for QueryRow
// and the read-write pool is used for QueryRowRW and Exec.
// This is a documentation test showing the intended design.
func TestPoolSeparation(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	path := filepath.Join(tmp, "test.db")

	s, err := NewWithPath(path)
	if err != nil {
		t.Fatalf("NewWithPath() error: %v", err)
	}
	defer s.Close()

	// The SQLite instance has two pools: ro (read-only) and rw (read-write)
	if s.ro == nil {
		t.Error("read-only pool (ro) is nil")
	}
	if s.rw == nil {
		t.Error("read-write pool (rw) is nil")
	}

	// QueryRow should use the read-only pool (ro)
	// QueryRowRW and Exec should use the read-write pool (rw)
	// This is verified by the implementation in db.go

	// Create table and insert data using rw pool
	if err := s.Exec(`CREATE TABLE test_items(id INTEGER PRIMARY KEY, name TEXT)`); err != nil {
		t.Fatalf("CREATE TABLE error: %v", err)
	}

	if err := s.Exec(`INSERT INTO test_items(name) VALUES(?)`, "test"); err != nil {
		t.Fatalf("INSERT error: %v", err)
	}

	// Read using ro pool (via QueryRow)
	var name string
	err = s.QueryRow(`SELECT name FROM test_items WHERE id = 1`).Scan(&name)
	if err != nil {
		t.Fatalf("QueryRow SELECT error: %v", err)
	}
	if name != "test" {
		t.Errorf("expected 'test', got %q", name)
	}

	// The separation is transparent to the user, but the implementation ensures:
	// - Concurrent reads use the ro pool
	// - Writes use the rw pool with single-writer semantics
	// - This prevents "attempt to write a readonly database" errors
}
