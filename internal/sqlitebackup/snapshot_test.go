package sqlitebackup

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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
	if runtime.GOOS != "windows" {
		info, err := os.Stat(destination)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("snapshot permissions = %o, want 600", info.Mode().Perm())
		}
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

func TestSnapshotPreservesExistingDestination(t *testing.T) {
	directory := t.TempDir()
	source := filepath.Join(directory, "source.sqlite3")
	destination := filepath.Join(directory, "snapshot.sqlite3")
	if err := os.WriteFile(source, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Create(context.Background(), source, destination); err == nil {
		t.Fatal("Create overwrote an existing destination")
	}
	data, err := os.ReadFile(destination)
	if err != nil || string(data) != "keep" {
		t.Fatalf("destination = %q, error = %v", data, err)
	}
}

func TestCancelledSnapshotDoesNotCreateDestination(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	destination := filepath.Join(t.TempDir(), "new-directory", "snapshot.sqlite3")
	if err := Create(ctx, "missing.sqlite3", destination); !errors.Is(err, context.Canceled) {
		t.Fatalf("Create error = %v, want cancellation", err)
	}
	if _, err := os.Stat(filepath.Dir(destination)); !os.IsNotExist(err) {
		t.Fatalf("cancelled snapshot created a directory: %v", err)
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

func TestSQLiteURIUsesAbsoluteFileURLOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows file URL behavior")
	}
	uri, err := sqliteURI(filepath.Join(t.TempDir(), "source.sqlite3"), "mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(uri, "file:///") {
		t.Fatalf("SQLite URI = %q, want an absolute file URL", uri)
	}
}
