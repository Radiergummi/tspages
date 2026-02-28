// Package sqlmigrate applies numbered schema migrations to a SQLite database
// using PRAGMA user_version for version tracking.
package sqlmigrate

import (
	"database/sql"
	"fmt"
)

// Apply runs pending migrations against db. Migrations are indexed starting at
// 1. Each migration runs in its own transaction; PRAGMA user_version is bumped
// inside the same transaction so version and schema stay in sync.
func Apply(db *sql.DB, migrations []func(*sql.Tx) error) error {
	var current int
	if err := db.QueryRow("PRAGMA user_version").Scan(&current); err != nil {
		return fmt.Errorf("sqlmigrate: reading schema version: %w", err)
	}
	for i, fn := range migrations {
		version := i + 1
		if version <= current {
			continue
		}
		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("sqlmigrate: migration %d: begin: %w", version, err)
		}
		if err := fn(tx); err != nil {
			tx.Rollback()
			return fmt.Errorf("sqlmigrate: migration %d: %w", version, err)
		}
		if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", version)); err != nil {
			tx.Rollback()
			return fmt.Errorf("sqlmigrate: migration %d: setting version: %w", version, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("sqlmigrate: migration %d: commit: %w", version, err)
		}
	}
	return nil
}
