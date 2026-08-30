package app

import (
	"context"
	"database/sql"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestRealResticEncryptedBackupAndRestore(t *testing.T) {
	if os.Getenv("RESTIC_INTEGRATION") != "1" {
		t.Skip("set RESTIC_INTEGRATION=1 and install Restic")
	}
	resticPath, err := exec.LookPath("restic")
	if err != nil {
		t.Skip("set RESTIC_INTEGRATION=1 and install Restic")
	}
	t.Setenv("GO_WANT_PASSWORD_HELPER", "1")
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
	profile := Profile{
		Name:            "example",
		Repository:      repository,
		BackupPaths:     []string{ordinary},
		SQLiteDatabases: []SQLiteDatabase{{Name: "primary", Path: sourcePath}},
		ResticArgs:      []string{"--no-cache"},
		BackupArgs:      []string{"--skip-if-unchanged"},
		Credentials: Credentials{Password: PasswordSource{
			Command: []string{os.Args[0], "-test.run=TestPasswordHelper"},
		}},
	}
	restic := &Restic{executable: resticPath, stdin: nil, stdout: io.Discard, stderr: io.Discard}
	ctx := context.Background()
	if err := restic.Run(ctx, profile, []string{"init"}, ""); err != nil {
		t.Fatal(err)
	}
	if err := Backup(ctx, restic, profile, false, io.Discard); err != nil {
		t.Fatal(err)
	}
	if err := Snapshots(ctx, restic, profile); err != nil {
		t.Fatal(err)
	}
	if err := Check(ctx, restic, profile); err != nil {
		t.Fatal(err)
	}
	restoreTarget := filepath.Join(directory, "restore")
	if err := Restore(ctx, restic, profile, "latest", restoreTarget, false); err != nil {
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
