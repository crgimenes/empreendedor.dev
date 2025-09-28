package db_test

import (
	"fmt"
	"os"
	"path/filepath"

	"edev/db"
)

// ExampleNewWithPath shows how to open a temporary database, create a table,
// and read data using the helper methods that apply timeouts automatically.
func ExampleNewWithPath() {
	tmpDir, err := os.MkdirTemp("", "db-example-")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(tmpDir)

	store, err := db.NewWithPath(filepath.Join(tmpDir, "example.db"))
	if err != nil {
		panic(err)
	}
	defer store.Close()

	if err := store.Exec(`CREATE TABLE items(name TEXT NOT NULL)`); err != nil {
		panic(err)
	}
	if err := store.Exec(`INSERT INTO items(name) VALUES(?)`, "widget"); err != nil {
		panic(err)
	}

	var name string
	if err := store.QueryRow(`SELECT name FROM items`).Scan(&name); err != nil {
		panic(err)
	}

	fmt.Println("name:", name)
	// Output:
	// name: widget
}

// ExampleTransaction demonstrates how to use explicit transactions for grouped writes.
func ExampleTransaction() {
	tmpDir, err := os.MkdirTemp("", "db-example-tx-")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(tmpDir)

	store, err := db.NewWithPath(filepath.Join(tmpDir, "example-tx.db"))
	if err != nil {
		panic(err)
	}
	defer store.Close()

	if err := store.Exec(`CREATE TABLE kv(k TEXT PRIMARY KEY, v TEXT NOT NULL)`); err != nil {
		panic(err)
	}

	tx, err := store.BeginTransaction()
	if err != nil {
		panic(err)
	}
	if err := tx.Exec(`INSERT INTO kv(k, v) VALUES(?, ?)`, "a", "committed"); err != nil {
		_ = tx.Rollback()
		panic(err)
	}
	if err := tx.Commit(); err != nil {
		panic(err)
	}

	tx2, err := store.BeginTransaction()
	if err != nil {
		panic(err)
	}
	if err := tx2.Exec(`INSERT INTO kv(k, v) VALUES(?, ?)`, "b", "rolled-back"); err != nil {
		_ = tx2.Rollback()
		panic(err)
	}
	if err := tx2.Rollback(); err != nil {
		panic(err)
	}

	var value string
	if err := store.QueryRow(`SELECT v FROM kv WHERE k = ?`, "a").Scan(&value); err != nil {
		panic(err)
	}

	fmt.Println("stored value:", value)
	// Output:
	// stored value: committed
}
