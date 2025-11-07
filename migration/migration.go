package migration

import (
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"strings"

	"edev/db"
	"edev/log"
)

var (
	//go:embed *.up.sql
	filesystem embed.FS
)

func chkTableExists(tx *db.Transaction) (bool, error) {
	const query = `SELECT count(*)
                       FROM sqlite_master
                       WHERE type='table'
                       AND name='schema_migrations'`
	var count int
	err := tx.QueryRow(query).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("failed to check if schema_migrations table exists: %w", err)
	}
	return count > 0, nil
}

func createMigrationsTable(tx *db.Transaction) error {
	const createTableSQL = `CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY)`
	err := tx.Exec(createTableSQL)
	if err != nil {
		return fmt.Errorf("failed to create schema_migrations table: %w", err)
	}
	return nil
}

func getMigrationMaxTx(tx *db.Transaction) (int, error) {
	const query = "SELECT MAX(version) FROM schema_migrations"
	var max sql.NullInt64
	err := tx.QueryRow(query).Scan(&max)
	if err != nil {
		return 0, fmt.Errorf("failed to get max migration version: %w", err)
	}

	if !max.Valid {
		return 0, nil
	}

	return int(max.Int64), nil
}

func findMigrationFile(fsys fs.FS, version int) (string, error) {
	pattern := fmt.Sprintf("%03d_*.up.sql", version)
	matches, err := fs.Glob(fsys, pattern)
	if err != nil {
		return "", fmt.Errorf(
			"failed to glob migration files using pattern %q: %w",
			pattern,
			err)
	}

	if len(matches) == 0 {
		return "", fmt.Errorf(
			"no migration file matched pattern %q for version %03d",
			pattern,
			version)
	}

	if len(matches) > 1 {
		// If multiple matches exist, prefer a file that has non-empty, non-comment content.
		// This allows deprecating a migration by leaving an empty/comment-only file with same version.
		nonEmpty := make([]string, 0, len(matches))
		for _, m := range matches {
			b, rerr := fs.ReadFile(fsys, m)
			if rerr != nil {
				// If we cannot read, treat as non-empty to avoid false negatives
				nonEmpty = append(nonEmpty, m)
				continue
			}
			content := strings.TrimSpace(string(b))
			// Strip out leading comment lines
			lines := strings.Split(content, "\n")
			filtered := make([]string, 0, len(lines))
			for _, ln := range lines {
				s := strings.TrimSpace(ln)
				if s == "" {
					continue
				}
				if strings.HasPrefix(s, "--") {
					continue
				}
				filtered = append(filtered, s)
			}
			if len(filtered) > 0 {
				nonEmpty = append(nonEmpty, m)
			}
		}
		if len(nonEmpty) == 1 {
			return nonEmpty[0], nil
		}
		return "", fmt.Errorf(
			"multiple migration files matched pattern %q for version %03d: %v",
			pattern,
			version,
			matches)
	}

	return matches[0], nil
}

func Run() error {
	files, err := filesystem.ReadDir(".")
	if err != nil {
		log.Fatalf("failed to read migration files: %v", err)
	}

	tx, err := db.Storage.BeginTransaction()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() {
		if tx != nil {
			rberr := tx.Rollback()
			if rberr != nil {
				log.Printf("failed to rollback transaction: %v", rberr)
			}
		}
	}()

	exists, err := chkTableExists(tx)
	if err != nil {
		return fmt.Errorf(
			"failed to check if schema_migrations table exists: %w", err)
	}

	if !exists {
		err = createMigrationsTable(tx)
		if err != nil {
			return fmt.Errorf(
				"failed to ensure schema_migrations table exists: %w", err)
		}
	}

	maxVersion, err := getMigrationMaxTx(tx)
	if err != nil {
		return fmt.Errorf("failed to get max migration version: %w", err)
	}

	// Determine the highest migration version from filenames to avoid duplicate version files
	highestVersion := 0
	for _, de := range files {
		name := de.Name()
		if !strings.HasSuffix(name, ".up.sql") {
			continue
		}
		if len(name) < 7 { // e.g., 001_x.up.sql
			continue
		}
		numStr := name[:3]
		var v int
		_, perr := fmt.Sscanf(numStr, "%03d", &v)
		if perr == nil && v > highestVersion {
			highestVersion = v
		}
	}

	if maxVersion >= highestVersion {
		log.Printf("no new migrations to apply (current version: %d)", maxVersion)
		return tx.Commit()
	}

	log.Printf("applying migrations from version %d to %d", maxVersion+1, highestVersion)

	for i := maxVersion + 1; i <= highestVersion; i++ {
		filename, err := findMigrationFile(filesystem, i)
		if err != nil {
			return fmt.Errorf("failed to locate migration for version %d: %w", i, err)
		}

		file, err := filesystem.ReadFile(filename)
		if err != nil {
			return fmt.Errorf("failed to read migration file %s: %w", filename, err)
		}

		err = tx.Exec(string(file))
		if err != nil {
			return fmt.Errorf("failed to apply migration %s: %w", filename, err)
		}

		err = tx.Exec("INSERT INTO schema_migrations (version) VALUES (?)", i)
		if err != nil {
			return fmt.Errorf("failed to record migration version %d: %w", i, err)
		}
	}

	err = tx.Commit()
	if err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	tx = nil

	return nil
}
