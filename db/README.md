# Database Package

The `db` package provides an opinionated SQLite layer built on top of the `modernc.org/sqlite` driver (CGO free). It gives the application safe defaults for concurrency, durability, and observability without forcing extra dependencies.

## Highlights

- Separate connection pools for writers and readers to reduce contention.
- Write-ahead logging (WAL) with `synchronous=NORMAL` for balanced durability.
- Busy timeout, short per-operation deadlines, and aggressive WAL checkpointing.
- Simple API for executing statements and managing explicit transactions.

## Installation

The package is internal to this repository. Import it from other Go packages with:

```go
import "edev/db"
```

All examples assume Go 1.22 or later.

## Opening the database

Use `db.New()` when you want to respect the configured database path (from `config.Cfg.DatabaseURL`). If no path is configured it falls back to `edev.db` in the working directory.

```go
store, err := db.New()
if err != nil {
    log.Fatalf("open database: %v", err)
}
defer store.Close()
```

In tests or tools you can bypass the global configuration and create a new instance pointing to a temporary file:

```go
tmpDir := t.TempDir()
store, err := db.NewWithPath(filepath.Join(tmpDir, "test.db"))
if err != nil {
    t.Fatalf("open database: %v", err)
}
defer store.Close()
```

Both helpers open two pools:

- A single-writer pool configured with WAL, busy timeout, `foreign_keys` enabled, and an IMMEDIATE transaction lock for predictable latency under contention.
- A read-only pool sized to `GOMAXPROCS` (minimum 4 connections) for parallel SELECTs.

## Executing statements

Use the `Exec`, `Query`, and `QueryRow` helpers for ad-hoc operations. All methods run with short timeouts to avoid runaway queries.

```go
if err := store.Exec(`CREATE TABLE IF NOT EXISTS items(id INTEGER PRIMARY KEY, name TEXT NOT NULL)`); err != nil {
    return fmt.Errorf("create table: %w", err)
}
if err := store.Exec(`INSERT INTO items(name) VALUES(?)`, "example"); err != nil {
    return fmt.Errorf("insert: %w", err)
}
var count int
if err := store.QueryRow(`SELECT COUNT(*) FROM items`).Scan(&count); err != nil {
    return fmt.Errorf("count: %w", err)
}
```

> Always call `Scan` (or `Err`) on the returned row to release the underlying timeout context.

## Transactions

Call `BeginTransaction` for multi-statement writes. The returned transaction provides matching `Exec`, `Query`, and `QueryRow` methods. Commit rolls back automatically on failure.

```go
tx, err := store.BeginTransaction()
if err != nil {
    return fmt.Errorf("begin: %w", err)
}
if err := tx.Exec(`INSERT INTO kv(k, v) VALUES(?, ?)`, "key", "value"); err != nil {
    _ = tx.Rollback()
    return fmt.Errorf("insert: %w", err)
}
if err := tx.Commit(); err != nil {
    return fmt.Errorf("commit: %w", err)
}
```

Transactions run on the single-writer pool to guarantee serialized writes. They do not accept a context because per-operation timeouts are applied inside each helper.

## Graceful shutdown

Always call `Close` during application shutdown. The method performs a best-effort WAL checkpoint (`wal_checkpoint(TRUNCATE)`) before closing both pools. The main application defers closing the database until after the HTTP server and background work finish so that all in-flight requests can drain.

```go
func shutdown(ctx context.Context, store *db.SQLite) {
    // stop HTTP server first, wait for workers, then:
    store.Close()
}
```

## Troubleshooting

- **Database is locked**: Busy timeouts handle short spikes, but long-running readers can still block writers. Keep transactions small and avoid starting them far in advance of the write.
- **Slow queries**: The read pool enforces per-operation timeouts. Tune SQL or add indexes if queries exceed the deadline.
- **Unexpected temporary files**: WAL mode keeps a `*-wal` file while the process runs. `Close` triggers `wal_checkpoint(TRUNCATE)` to shrink it; ensure the process exits cleanly to reap the file.

## Testing helpers

Unit tests can create isolated databases with `NewWithPath` and leverage the small helpers defined in `db_test.go` for scanning pragma results. Run the package tests (including examples) with:

```sh
go test ./db
```

## Examples

See `example_test.go` for runnable examples demonstrating initialization, read/write helpers, and transactions. Running `go test ./db` executes them along with the rest of the test suite.
