package sqlitebackup

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func TestSnapshotIsConsistentWhileSourceIsOpen(t *testing.T) {
	directory := t.TempDir()
	sourcePath := filepath.Join(directory, "source.sqlite3")
	destination := filepath.Join(directory, "snapshot.sqlite3")
	source, err := sql.Open("sqlite", sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	if _, err := source.Exec("PRAGMA journal_mode=WAL"); err != nil {
		t.Fatal(err)
	}
	if _, err := source.Exec("CREATE TABLE items (value TEXT NOT NULL)"); err != nil {
		t.Fatal(err)
	}
	if _, err := source.Exec("INSERT INTO items VALUES ('committed')"); err != nil {
		t.Fatal(err)
	}
	transaction, err := source.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer transaction.Rollback()
	if _, err := transaction.Exec("INSERT INTO items VALUES ('uncommitted')"); err != nil {
		t.Fatal(err)
	}

	if err := Create(context.Background(), sourcePath, destination); err != nil {
		t.Fatal(err)
	}
	snapshot, err := sql.Open("sqlite", destination)
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Close()
	rows, err := snapshot.Query("SELECT value FROM items")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var values []string
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			t.Fatal(err)
		}
		values = append(values, value)
	}
	if len(values) != 1 || values[0] != "committed" {
		t.Fatalf("snapshot values = %v", values)
	}
}

func TestFailedSnapshotDoesNotLeaveDestination(t *testing.T) {
	directory := t.TempDir()
	source := filepath.Join(directory, "invalid.sqlite3")
	destination := filepath.Join(directory, "snapshot.sqlite3")
	if err := os.WriteFile(source, []byte("not a database"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Create(context.Background(), source, destination); err == nil {
		t.Fatal("Create succeeded for an invalid database")
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatalf("incomplete snapshot still exists: %v", err)
	}
}
