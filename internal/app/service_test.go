package app

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"resticctl/internal/profile"
)

func TestBackupStagesDatabaseAndBuildsArguments(t *testing.T) {
	directory := t.TempDir()
	source := filepath.Join(directory, "source.sqlite3")
	database, err := sql.Open("sqlite", source)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec("CREATE TABLE data (value TEXT)"); err != nil {
		t.Fatal(err)
	}
	database.Close()
	ordinary := filepath.Join(directory, "ordinary")
	if err := os.Mkdir(ordinary, 0o700); err != nil {
		t.Fatal(err)
	}
	backupProfile := profile.Profile{
		Name: "example", BackupPaths: []string{ordinary},
		SQLiteDatabases: []profile.SQLiteDatabase{{Name: "primary", Path: source}},
		BackupArgs:      []string{"--skip-if-unchanged"}, Tags: []string{"database"},
	}
	runner := &recordingRunner{}
	if err := Backup(context.Background(), runner, backupProfile, true, io.Discard); err != nil {
		t.Fatal(err)
	}
	if len(runner.runs) != 1 {
		t.Fatalf("runs = %d", len(runner.runs))
	}
	want := []string{"backup", "--group-by", "host,tags", "--tag", "profile:example", "--tag", "database", "--skip-if-unchanged", "--dry-run", "--", ordinary, "databases"}
	if !slices.Equal(runner.runs[0].arguments, want) {
		t.Fatalf("arguments = %v, want %v", runner.runs[0].arguments, want)
	}
	if _, err := os.Stat(runner.runs[0].cwd); !os.IsNotExist(err) {
		t.Fatalf("staging directory was not removed: %v", err)
	}
}

func TestSnapshotFilterDoesNotIncludeCustomTags(t *testing.T) {
	runner := &recordingRunner{}
	backupProfile := profile.Profile{Name: "example", Tags: []string{"database"}}
	if err := Snapshots(context.Background(), runner, backupProfile); err != nil {
		t.Fatal(err)
	}
	want := []string{"snapshots", "--tag", "profile:example"}
	if !slices.Equal(runner.runs[0].arguments, want) {
		t.Fatalf("arguments = %v", runner.runs[0].arguments)
	}
}

func TestForgetRequiresExplicitPrune(t *testing.T) {
	runner := &recordingRunner{}
	backupProfile := profile.Profile{Name: "example", ForgetArgs: []string{"--keep-last", "2"}}
	if err := Forget(context.Background(), runner, backupProfile, true, false); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(runner.runs[0].arguments, " ")
	if !strings.Contains(joined, "--dry-run") || strings.Contains(joined, "--prune") {
		t.Fatalf("arguments = %v", runner.runs[0].arguments)
	}
}

func TestBackupReturnsProgressOutputError(t *testing.T) {
	wantErr := errors.New("write failed")
	runner := &recordingRunner{}
	backupProfile := profile.Profile{
		Name:            "example",
		SQLiteDatabases: []profile.SQLiteDatabase{{Name: "primary", Path: "unused"}},
	}

	err := Backup(context.Background(), runner, backupProfile, false, errorWriter{err: wantErr})
	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want %v", err, wantErr)
	}
	if len(runner.runs) != 0 {
		t.Fatalf("runs = %d, want 0", len(runner.runs))
	}
}
