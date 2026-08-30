package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestCreateAndListCommands(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "config")
	var output, stderr bytes.Buffer
	status, err := Run(context.Background(), []string{"create", "example", "--config-dir", directory}, &output, &stderr)
	if err != nil || status != 0 {
		t.Fatalf("create status=%d error=%v stderr=%s", status, err, stderr.String())
	}
	output.Reset()
	status, err = Run(context.Background(), []string{"list", "--config-dir", directory}, &output, &stderr)
	if err != nil || status != 0 {
		t.Fatalf("list status=%d error=%v stderr=%s", status, err, stderr.String())
	}
	if strings.TrimSpace(output.String()) != "example" {
		t.Fatalf("list output = %q", output.String())
	}
}

func TestCommandHelp(t *testing.T) {
	var output, stderr bytes.Buffer
	status, err := Run(context.Background(), []string{"backup", "--help"}, &output, &stderr)
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
	status, err := Run(
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
	status, err := Run(context.Background(), []string{"schedule", "install"}, io.Discard, &stderr)
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
	status, err := Run(context.Background(), []string{"--version"}, &output, &bytes.Buffer{})
	if err != nil || status != 0 {
		t.Fatalf("version status=%d error=%v", status, err)
	}
	if output.String() != "resticctl "+Version+"\n" {
		t.Fatalf("version output = %q", output.String())
	}
}

func TestCompletionCommand(t *testing.T) {
	var output bytes.Buffer
	status, err := Run(context.Background(), []string{"completion", "zsh"}, &output, &bytes.Buffer{})
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
	cli := newCommandLine(strings.NewReader(""), errorWriter{err: wantErr}, io.Discard)

	status, err := cli.run(context.Background(), []string{"list", "--config-dir", directory})
	if status != 1 {
		t.Fatalf("status = %d, want 1", status)
	}
	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want %v", err, wantErr)
	}
}

func TestProfileCompletionUsesConfigDirectory(t *testing.T) {
	directory := t.TempDir()
	for _, name := range []string{"example.json", "extra.json", "other.json", "example.credentials.json"} {
		if err := os.WriteFile(filepath.Join(directory, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cli := newCommandLine(strings.NewReader(""), io.Discard, io.Discard)
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
