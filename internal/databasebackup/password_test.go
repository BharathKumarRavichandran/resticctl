package databasebackup

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"resticctl/internal/profile"
)

func TestDatabasePasswordHelper(t *testing.T) {
	if os.Getenv("GO_WANT_DATABASE_PASSWORD_HELPER") != "1" {
		return
	}
	fmt.Print(strings.Repeat("x", maximumPasswordBytes+1))
	os.Exit(0)
}

func TestResolvePasswordRejectsOversizedCommandOutput(t *testing.T) {
	t.Setenv("GO_WANT_DATABASE_PASSWORD_HELPER", "1")
	_, err := ResolvePassword(context.Background(), profile.PasswordSource{
		Command: []string{os.Args[0], "-test.run=^TestDatabasePasswordHelper$"},
	})
	if err == nil || !strings.Contains(err.Error(), "1 MiB") {
		t.Fatalf("ResolvePassword() error = %v", err)
	}
}

func TestResolvePasswordReadsFileAndTrimsLineEnding(t *testing.T) {
	path := filepath.Join(t.TempDir(), "password")
	if err := os.WriteFile(path, []byte("private\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	password, err := ResolvePassword(context.Background(), profile.PasswordSource{File: path})
	if err != nil || password != "private" {
		t.Fatalf("ResolvePassword() = %q, %v", password, err)
	}
}

func TestResolvePasswordRejectsEmptyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "password")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := ResolvePassword(context.Background(), profile.PasswordSource{File: path})
	if err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("ResolvePassword() error = %v", err)
	}
}

func TestResolvePasswordRejectsOversizedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "password")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", maximumPasswordBytes+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := ResolvePassword(context.Background(), profile.PasswordSource{File: path})
	if err == nil || !strings.Contains(err.Error(), "1 MiB") {
		t.Fatalf("ResolvePassword() error = %v", err)
	}
}

func TestResolvePasswordRejectsNUL(t *testing.T) {
	_, err := ResolvePassword(context.Background(), profile.PasswordSource{Value: "secret\x00suffix"})
	if err == nil || !strings.Contains(err.Error(), "NUL") {
		t.Fatalf("ResolvePassword() error = %v", err)
	}
}

func TestResolvePasswordRejectsOversizedValue(t *testing.T) {
	_, err := ResolvePassword(context.Background(), profile.PasswordSource{Value: strings.Repeat("x", maximumPasswordBytes+1)})
	if err == nil || !strings.Contains(err.Error(), "1 MiB") {
		t.Fatalf("ResolvePassword() error = %v", err)
	}
}
