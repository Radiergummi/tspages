package db

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// OpenTestDB opens a WAL-mode SQLite database in a temp directory,
// applies the given migration slices in order, and registers t.Cleanup
// to close it.
func OpenTestDB(t *testing.T, migrations ...[]func(*sql.Tx) error) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	var all []func(*sql.Tx) error
	for _, m := range migrations {
		all = append(all, m...)
	}
	if err := Apply(db, all); err != nil {
		db.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}
