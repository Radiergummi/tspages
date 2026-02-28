package sqlmigrate

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func userVersion(t *testing.T, db *sql.DB) int {
	t.Helper()
	var v int
	if err := db.QueryRow("PRAGMA user_version").Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestApply_FreshDB(t *testing.T) {
	db := openTestDB(t)

	migrations := []func(*sql.Tx) error{
		func(tx *sql.Tx) error {
			_, err := tx.Exec(`CREATE TABLE t1 (id INTEGER PRIMARY KEY)`)
			return err
		},
	}

	if err := Apply(db, migrations); err != nil {
		t.Fatal(err)
	}

	if v := userVersion(t, db); v != 1 {
		t.Fatalf("want version 1, got %d", v)
	}

	// Table should exist.
	var name string
	err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name='t1'`).Scan(&name)
	if err != nil {
		t.Fatal("table t1 not created")
	}
}

func TestApply_AlreadyMigrated(t *testing.T) {
	db := openTestDB(t)

	called := 0
	migrations := []func(*sql.Tx) error{
		func(tx *sql.Tx) error {
			called++
			_, err := tx.Exec(`CREATE TABLE t1 (id INTEGER PRIMARY KEY)`)
			return err
		},
	}

	if err := Apply(db, migrations); err != nil {
		t.Fatal(err)
	}
	if called != 1 {
		t.Fatalf("expected migration to run once, ran %d times", called)
	}

	// Run again — migration should be skipped.
	called = 0
	if err := Apply(db, migrations); err != nil {
		t.Fatal(err)
	}
	if called != 0 {
		t.Fatalf("expected migration to be skipped, ran %d times", called)
	}
}

func TestApply_MultiStep(t *testing.T) {
	db := openTestDB(t)

	migrations := []func(*sql.Tx) error{
		func(tx *sql.Tx) error {
			_, err := tx.Exec(`CREATE TABLE t1 (id INTEGER PRIMARY KEY)`)
			return err
		},
		func(tx *sql.Tx) error {
			_, err := tx.Exec(`ALTER TABLE t1 ADD COLUMN name TEXT NOT NULL DEFAULT ''`)
			return err
		},
	}

	if err := Apply(db, migrations); err != nil {
		t.Fatal(err)
	}

	if v := userVersion(t, db); v != 2 {
		t.Fatalf("want version 2, got %d", v)
	}

	// Column should exist.
	_, err := db.Exec(`INSERT INTO t1 (name) VALUES ('test')`)
	if err != nil {
		t.Fatal("column 'name' not added:", err)
	}
}

func TestApply_FailureRollsBack(t *testing.T) {
	db := openTestDB(t)

	migrations := []func(*sql.Tx) error{
		func(tx *sql.Tx) error {
			_, err := tx.Exec(`CREATE TABLE t1 (id INTEGER PRIMARY KEY)`)
			return err
		},
		func(tx *sql.Tx) error {
			return fmt.Errorf("intentional failure")
		},
	}

	err := Apply(db, migrations)
	if err == nil {
		t.Fatal("expected error")
	}

	// Version should be 1 (first migration succeeded).
	if v := userVersion(t, db); v != 1 {
		t.Fatalf("want version 1 after partial failure, got %d", v)
	}
}

func TestApply_ResumesFromVersion(t *testing.T) {
	db := openTestDB(t)

	// Run first migration only.
	first := []func(*sql.Tx) error{
		func(tx *sql.Tx) error {
			_, err := tx.Exec(`CREATE TABLE t1 (id INTEGER PRIMARY KEY)`)
			return err
		},
	}
	if err := Apply(db, first); err != nil {
		t.Fatal(err)
	}

	// Now run with both migrations — only the second should execute.
	secondCalled := false
	both := []func(*sql.Tx) error{
		func(tx *sql.Tx) error {
			t.Fatal("migration 1 should not run again")
			return nil
		},
		func(tx *sql.Tx) error {
			secondCalled = true
			_, err := tx.Exec(`ALTER TABLE t1 ADD COLUMN name TEXT NOT NULL DEFAULT ''`)
			return err
		},
	}

	if err := Apply(db, both); err != nil {
		t.Fatal(err)
	}

	if !secondCalled {
		t.Fatal("migration 2 was not called")
	}
	if v := userVersion(t, db); v != 2 {
		t.Fatalf("want version 2, got %d", v)
	}
}
