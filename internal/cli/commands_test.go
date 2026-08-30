package cli

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"resticctl/internal/app"
	"resticctl/internal/profile"
)

func TestProfileCommandDispatch(t *testing.T) {
	directory := t.TempDir()
	writeCLIProfile(t, directory)
	tests := []struct {
		name      string
		arguments []string
		want      []string
	}{
		{name: "init", arguments: []string{"init", "example"}, want: []string{"init"}},
		{
			name:      "snapshots",
			arguments: []string{"snapshots", "example"},
			want:      []string{"snapshots", "--tag", "profile:example"},
		},
		{name: "check", arguments: []string{"check", "example"}, want: []string{"check"}},
		{
			name:      "backup flags",
			arguments: []string{"backup", "example", "--dry-run"},
			want: []string{
				"backup", "--group-by", "host,tags", "--tag", "profile:example", "--dry-run", "--", directory,
			},
		},
		{
			name:      "forget flags",
			arguments: []string{"forget", "example", "--prune", "--dry-run"},
			want: []string{
				"forget", "--tag", "profile:example", "--group-by", "host,tags", "--keep-last", "2", "--prune", "--dry-run",
			},
		},
		{
			name:      "restore flags",
			arguments: []string{"restore", "example", "latest", "target", "--dry-run"},
			want: []string{
				"restore", "latest", "--tag", "profile:example", "--target", "target", "--dry-run", "--verbose=2",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runner := &recordingRunner{}
			cli := newCommandLine(strings.NewReader(""), io.Discard, io.Discard)
			cli.newRunner = func() (app.Runner, error) { return runner, nil }
			arguments := append(append([]string(nil), test.arguments...), "--config-dir", directory)

			status, err := cli.run(context.Background(), arguments)
			if err != nil || status != 0 {
				t.Fatalf("status=%d error=%v", status, err)
			}
			if len(runner.runs) != 1 {
				t.Fatalf("runs = %d, want 1", len(runner.runs))
			}
			if got := runner.runs[0].arguments; !slices.Equal(got, test.want) {
				t.Fatalf("arguments = %v, want %v", got, test.want)
			}
		})
	}
}

func TestExecutionErrorHasRuntimeStatus(t *testing.T) {
	directory := t.TempDir()
	writeCLIProfile(t, directory)
	wantErr := errors.New("runner unavailable")
	cli := newCommandLine(strings.NewReader(""), io.Discard, io.Discard)
	cli.newRunner = func() (app.Runner, error) { return nil, wantErr }

	status, err := cli.run(
		context.Background(),
		[]string{"snapshots", "example", "--config-dir", directory},
	)
	if status != 1 {
		t.Fatalf("status = %d, want 1", status)
	}
	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want %v", err, wantErr)
	}
}

func writeCLIProfile(t *testing.T, directory string) {
	t.Helper()
	files := []struct {
		name  string
		value any
	}{
		{
			name: "example.json",
			value: profile.Profile{
				Repository:      "test-repository",
				CredentialsFile: "example.credentials.json",
				BackupPaths:     []string{"."},
				ForgetArgs:      []string{"--keep-last", "2"},
			},
		},
		{
			name: "example.credentials.json",
			value: profile.Credentials{
				Environment: map[string]string{},
				Password:    profile.PasswordSource{Command: []string{"unused"}},
			},
		},
	}
	for _, file := range files {
		content, err := json.Marshal(file.value)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(directory, file.name), content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}
