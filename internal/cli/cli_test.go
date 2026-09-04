package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"resticctl/internal/securefile"
)

func TestCreateAndListCommands(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "config")
	var output, stderr bytes.Buffer
	status, err := runForTest(context.Background(), []string{"create", "example", "--config-dir", directory}, &output, &stderr)
	if err != nil || status != 0 {
		t.Fatalf("create status=%d error=%v stderr=%s", status, err, stderr.String())
	}
	output.Reset()
	status, err = runForTest(context.Background(), []string{"list", "--config-dir", directory}, &output, &stderr)
	if err != nil || status != 0 {
		t.Fatalf("list status=%d error=%v stderr=%s", status, err, stderr.String())
	}
	if strings.TrimSpace(output.String()) != "example" {
		t.Fatalf("list output = %q", output.String())
	}
}

func TestShowCommandDisplaysResolvedProfileWithoutCredentials(t *testing.T) {
	directory := t.TempDir()
	writePrivateCLIFile(t, filepath.Join(directory, "base.json"), `{
          "repository":"rest:https://backup:repository-secret@example.test/repository",
          "backup_paths":["parent"],
          "tags":["inherited"]
        }`)
	writePrivateCLIFile(t, filepath.Join(directory, "example.json"), `{
          "parent":"base",
          "credentials_file":"example.credentials.json",
          "backup_paths":["child"],
          "monitoring":{"http":[{
            "url":"https://hooks.example.test/webhook-secret",
            "headers":{"Authorization":"header-secret"},
            "body":"body-secret"
          }]}
        }`)
	writePrivateCLIFile(t, filepath.Join(directory, "example.credentials.json"), `{
          "environment":{"TOKEN":"credential-secret"},
          "password":{"command":["password-command","command-secret"]}
        }`)

	var output, stderr bytes.Buffer
	status, err := runForTest(context.Background(), []string{"show", "example", "--config-dir", directory}, &output, &stderr)
	if err != nil || status != 0 {
		t.Fatalf("show status=%d error=%v stderr=%s", status, err, stderr.String())
	}
	var decoded struct {
		Name        string   `json:"name"`
		Parent      string   `json:"parent"`
		BackupPaths []string `json:"backup_paths"`
	}
	if err := json.Unmarshal(output.Bytes(), &decoded); err != nil {
		t.Fatalf("show output is not JSON: %v", err)
	}
	if decoded.Name != "example" || decoded.Parent != "base" {
		t.Fatalf("show identity = %q, %q", decoded.Name, decoded.Parent)
	}
	wantPaths := []string{filepath.Join(directory, "parent"), filepath.Join(directory, "child")}
	if !slices.Equal(decoded.BackupPaths, wantPaths) {
		t.Fatalf("backup paths = %q, want %q", decoded.BackupPaths, wantPaths)
	}
	if !strings.Contains(output.String(), `"Authorization": "[REDACTED]"`) {
		t.Fatalf("show output does not contain a redacted header:\n%s", output.String())
	}
	for _, secret := range []string{
		"repository-secret", "webhook-secret", "header-secret", "body-secret",
		"credential-secret", "password-command", "command-secret",
	} {
		if strings.Contains(output.String(), secret) {
			t.Fatalf("show output contains secret %q:\n%s", secret, output.String())
		}
	}
	var fields map[string]any
	if err := json.Unmarshal(output.Bytes(), &fields); err != nil {
		t.Fatalf("show output is not a JSON object: %v", err)
	}
	if _, exists := fields["credentials"]; exists {
		t.Fatal("show output contains credentials")
	}
}

func TestCommandHelp(t *testing.T) {
	var output, stderr bytes.Buffer
	status, err := runForTest(context.Background(), []string{"backup", "--help"}, &output, &stderr)
	if err != nil || status != 0 {
		t.Fatalf("help status=%d error=%v stderr=%s", status, err, stderr.String())
	}
	for _, expected := range []string{"Usage:", "resticctl backup <profile>", "--dry-run", "--config-dir"} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("help output does not contain %q:\n%s", expected, output.String())
		}
	}
}

func TestCommandArgumentErrorHasUsageStatus(t *testing.T) {
	var stderr bytes.Buffer
	status, err := runForTest(
		context.Background(),
		[]string{"restore", "example", "latest"},
		&bytes.Buffer{},
		&stderr,
	)
	if status != 2 {
		t.Fatalf("status = %d, want 2", status)
	}
	if err == nil || !strings.Contains(err.Error(), "accepts 3 arg(s)") {
		t.Fatalf("error = %v", err)
	}
	if !strings.Contains(stderr.String(), "Usage:") {
		t.Fatalf("usage was not printed:\n%s", stderr.String())
	}
}

func TestScheduleInstallArgumentErrorShowsExamples(t *testing.T) {
	var stderr bytes.Buffer
	status, err := runForTest(context.Background(), []string{"schedule", "install"}, io.Discard, &stderr)
	if status != 2 || err == nil {
		t.Fatalf("status=%d error=%v, want usage error", status, err)
	}
	for _, expected := range []string{"Usage:", "Examples:", "resticctl schedule install personal"} {
		if !strings.Contains(stderr.String(), expected) {
			t.Fatalf("output does not contain %q:\n%s", expected, stderr.String())
		}
	}
}

func TestVersionFlag(t *testing.T) {
	var output bytes.Buffer
	status, err := runForTest(context.Background(), []string{"--version"}, &output, &bytes.Buffer{})
	if err != nil || status != 0 {
		t.Fatalf("version status=%d error=%v", status, err)
	}
	if output.String() != "resticctl "+testVersion+"\n" {
		t.Fatalf("version output = %q", output.String())
	}
}

func TestCompletionCommand(t *testing.T) {
	var output bytes.Buffer
	status, err := runForTest(context.Background(), []string{"completion", "zsh"}, &output, &bytes.Buffer{})
	if err != nil || status != 0 {
		t.Fatalf("completion status=%d error=%v", status, err)
	}
	if output.Len() == 0 {
		t.Fatal("completion output is empty")
	}
}

func TestListReturnsOutputError(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "example.json"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	wantErr := errors.New("write failed")
	cli := newTestCommandLine(errorWriter{err: wantErr}, io.Discard)

	status, err := cli.run(context.Background(), []string{"list", "--config-dir", directory})
	if status != 1 {
		t.Fatalf("status = %d, want 1", status)
	}
	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want %v", err, wantErr)
	}
}

func writePrivateCLIFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := securefile.Protect(path); err != nil {
		t.Fatal(err)
	}
}

func TestProfileCompletionUsesConfigDirectory(t *testing.T) {
	directory := t.TempDir()
	for _, name := range []string{"example.json", "extra.json", "other.json", "example.credentials.json"} {
		if err := os.WriteFile(filepath.Join(directory, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cli := newTestCommandLine(io.Discard, io.Discard)
	cli.configDir = directory

	got, directive := cli.completeProfiles(nil, nil, "ex")
	want := []string{"example", "extra"}
	if !slices.Equal(got, want) {
		t.Fatalf("completions = %v, want %v", got, want)
	}
	if directive != cobra.ShellCompDirectiveNoFileComp {
		t.Fatalf("completion directive = %d", directive)
	}

	_, directive = cli.completeRestoreArguments(nil, []string{"example", "latest"}, "")
	if directive != cobra.ShellCompDirectiveFilterDirs {
		t.Fatalf("restore target completion directive = %d", directive)
	}
}
