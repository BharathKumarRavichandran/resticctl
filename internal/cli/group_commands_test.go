package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"resticctl/internal/app"
	"resticctl/internal/profile"
	"resticctl/internal/restic"
)

type groupRunner struct {
	runs int
	fail map[int]error
}

func (runner *groupRunner) Run(_ context.Context, _ restic.Config, _ []string, _ string) error {
	runner.runs++
	return runner.fail[runner.runs]
}

func (runner *groupRunner) RunHook(context.Context, []string) error { return nil }
func (runner *groupRunner) RunDatabase(context.Context, []string, map[string]string, string) error {
	return nil
}

func TestGroupBackupRunsProfilesInOrderAndContinues(t *testing.T) {
	directory := t.TempDir()
	writeGroupCLIProfile(t, directory, "home")
	writeGroupCLIProfile(t, directory, "databases")
	writeCLIGroup(t, directory, `{"name":"daily","profiles":["home","databases"],"continue_on_error":true}`)
	runner := &groupRunner{fail: map[int]error{1: errors.New("backup failed")}}
	var output bytes.Buffer
	cli := newTestCommandLine(&output, io.Discard)
	cli.newRunner = func() (app.Runner, error) { return runner, nil }

	status, err := cli.run(context.Background(), []string{"group", "backup", "daily", "--dry-run", "--config-dir", directory})
	if status != 1 || err == nil || !strings.Contains(err.Error(), "profile home") {
		t.Fatalf("status=%d error=%v", status, err)
	}
	if runner.runs != 2 {
		t.Fatalf("runs = %d, want 2", runner.runs)
	}
	for _, expected := range []string{"[1/2] Backing up profile home", "Profile home failed", "[2/2] Backing up profile databases", "Profile databases succeeded"} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("output does not contain %q:\n%s", expected, output.String())
		}
	}
}

func TestGroupBackupStopsAfterFailureByDefault(t *testing.T) {
	directory := t.TempDir()
	writeGroupCLIProfile(t, directory, "home")
	writeGroupCLIProfile(t, directory, "databases")
	writeCLIGroup(t, directory, `{"profiles":["home","databases"]}`)
	runner := &groupRunner{fail: map[int]error{1: errors.New("backup failed")}}
	cli := newTestCommandLine(io.Discard, io.Discard)
	cli.newRunner = func() (app.Runner, error) { return runner, nil }

	status, err := cli.run(context.Background(), []string{"group", "backup", "daily", "--dry-run", "--config-dir", directory})
	if status != 1 || err == nil || runner.runs != 1 {
		t.Fatalf("status=%d error=%v runs=%d", status, err, runner.runs)
	}
}

func TestGroupValidateLoadsEveryMemberBeforeExecution(t *testing.T) {
	directory := t.TempDir()
	writeGroupCLIProfile(t, directory, "home")
	writeCLIGroup(t, directory, `{"profiles":["home","missing"]}`)
	runner := &groupRunner{}
	cli := newTestCommandLine(io.Discard, io.Discard)
	cli.newRunner = func() (app.Runner, error) { return runner, nil }

	status, err := cli.run(context.Background(), []string{"group", "backup", "daily", "--config-dir", directory})
	if status != 1 || err == nil || !strings.Contains(err.Error(), "invalid profile missing") || runner.runs != 0 {
		t.Fatalf("status=%d error=%v runs=%d", status, err, runner.runs)
	}
}

func writeGroupCLIProfile(t *testing.T, directory, name string) {
	t.Helper()
	profilesDir := profile.Dir(directory)
	if err := os.MkdirAll(profilesDir, 0o700); err != nil {
		t.Fatal(err)
	}
	value := struct {
		Repository  string                        `json:"repository"`
		Credentials profile.RepositoryCredentials `json:"credentials"`
		BackupPaths []string                      `json:"backup_paths"`
	}{
		Repository:  "local:" + name,
		Credentials: profile.RepositoryCredentials{Password: profile.PasswordSource{Command: []string{"unused"}}},
		BackupPaths: []string{"."},
	}
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profilesDir, name+".json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeCLIGroup(t *testing.T, directory, content string) {
	t.Helper()
	groupsDir := filepath.Join(directory, "groups")
	if err := os.MkdirAll(groupsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(groupsDir, "daily.json"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
