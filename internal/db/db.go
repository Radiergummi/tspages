// Package db centralizes SQLite database management. It opens a single shared
// connection and runs all schema migrations in a unified sequence, preventing
// the PRAGMA user_version collision that occurs when independent packages each
// call Apply on the same database.
package db

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// MigrateFilename renames a database file (and its WAL/SHM sidecars) from
// oldName to newName within dir. If newName already exists, it does nothing.
func MigrateFilename(dir, oldName, newName string) error {
	oldPath := filepath.Join(dir, oldName)
	newPath := filepath.Join(dir, newName)

	if _, err := os.Stat(newPath); err == nil {
		return nil // already migrated
	}
	if _, err := os.Stat(oldPath); errors.Is(err, os.ErrNotExist) {
		return nil // nothing to migrate
	}

	for _, suffix := range []string{"", "-wal", "-shm"} {
		err := os.Rename(oldPath+suffix, newPath+suffix)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("db: renaming %s to %s: %w", oldName+suffix, newName+suffix, err)
		}
	}
	slog.Info("migrated database file", "component", "db", "from", oldName, "to", newName)
	return nil
}

// Open opens a SQLite database at dbPath with WAL mode and busy timeout, then
// applies all provided migrations in order. Each element of migrations is a
// module's migration slice; they are concatenated into a single sequence so
// PRAGMA user_version tracks them as one contiguous series.
func Open(dbPath string, migrations ...[]func(*sql.Tx) error) (*sql.DB, error) {
	db, err := sql.Open("sqlite", dbPath+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	var all []func(*sql.Tx) error
	for _, m := range migrations {
		all = append(all, m...)
	}
	if err := Apply(db, all); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// Apply runs pending migrations against db. Migrations are indexed starting at
// 1. Each migration runs in its own transaction; PRAGMA user_version is bumped
// inside the same transaction so version and schema stay in sync.
func Apply(db *sql.DB, migrations []func(*sql.Tx) error) error {
	var current int
	if err := db.QueryRow("PRAGMA user_version").Scan(&current); err != nil {
		return fmt.Errorf("db: reading schema version: %w", err)
	}
	for i, fn := range migrations {
		version := i + 1
		if version <= current {
			continue
		}
		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("db: migration %d: begin: %w", version, err)
		}
		if err := fn(tx); err != nil {
			tx.Rollback()
			return fmt.Errorf("db: migration %d: %w", version, err)
		}
		if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", version)); err != nil {
			tx.Rollback()
			return fmt.Errorf("db: migration %d: setting version: %w", version, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("db: migration %d: commit: %w", version, err)
		}
	}
	return nil
}
