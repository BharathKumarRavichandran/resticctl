package app

import (
	"context"
	"database/sql"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"resticctl/internal/process"
	"resticctl/internal/profile"
	"resticctl/internal/restic"
)

type integrationRunner struct {
	*restic.Client
	*process.Executor
}

func TestRealResticEncryptedBackupAndRestore(t *testing.T) {
	if os.Getenv("RESTIC_INTEGRATION") != "1" {
		t.Skip("set RESTIC_INTEGRATION=1 and install Restic")
	}
	resticPath, err := exec.LookPath("restic")
	if err != nil {
		t.Skip("set RESTIC_INTEGRATION=1 and install Restic")
	}
	directory := t.TempDir()
	repository := filepath.Join(directory, "repository")
	sourcePath := filepath.Join(directory, "source.sqlite3")
	source, err := sql.Open("sqlite", sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.Exec("CREATE TABLE items (value TEXT NOT NULL)"); err != nil {
		t.Fatal(err)
	}
	if _, err := source.Exec("INSERT INTO items VALUES ('sqlite data')"); err != nil {
		t.Fatal(err)
	}
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}
	ordinary := filepath.Join(directory, "ordinary")
	if err := os.Mkdir(ordinary, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ordinary, "file.txt"), []byte("ordinary data"), 0o600); err != nil {
		t.Fatal(err)
	}
	passwordFile := filepath.Join(directory, "password")
	if err := os.WriteFile(passwordFile, []byte("test-password\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	backupProfile := profile.Profile{
		Name:            "example",
		Repository:      repository,
		BackupPaths:     []string{ordinary},
		SQLiteDatabases: []profile.SQLiteDatabase{{Name: "primary", Path: sourcePath}},
		ResticArgs:      []string{"--no-cache"},
		BackupArgs:      []string{"--skip-if-unchanged"},
		Credentials:     profile.Credentials{Password: profile.PasswordSource{File: passwordFile}},
	}
	t.Setenv("RESTICCTL_RESTIC_COMMAND", resticPath)
	client, err := restic.New(nil, io.Discard, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	runner := &integrationRunner{
		Client:   client,
		Executor: process.NewExecutor(nil, io.Discard, io.Discard, profile.IsReservedEnvironment),
	}
	ctx := context.Background()
	if err := RunRestic(ctx, runner, backupProfile, "init", nil); err != nil {
		t.Fatal(err)
	}
	if err := Backup(ctx, runner, backupProfile, false, io.Discard); err != nil {
		t.Fatal(err)
	}
	if err := Snapshots(ctx, runner, backupProfile); err != nil {
		t.Fatal(err)
	}
	if err := Check(ctx, runner, backupProfile); err != nil {
		t.Fatal(err)
	}
	restoreTarget := filepath.Join(directory, "restore")
	if err := Restore(ctx, runner, backupProfile, "latest", restoreTarget, false); err != nil {
		t.Fatal(err)
	}
	restored, err := sql.Open("sqlite", filepath.Join(restoreTarget, "databases", "primary.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	var value string
	if err := restored.QueryRow("SELECT value FROM items").Scan(&value); err != nil {
		t.Fatal(err)
	}
	if value != "sqlite data" {
		t.Fatalf("restored value = %q", value)
	}
}
