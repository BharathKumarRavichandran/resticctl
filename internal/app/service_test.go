package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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

func TestBackupPreflightRunsBeforeHooks(t *testing.T) {
	runner := &hookRecordingRunner{}
	backupProfile := profile.Profile{
		Name: "example", PostgreSQLDatabases: []profile.PostgreSQLDatabase{{Name: "main", Database: "app", Executable: "resticctl-definitely-missing-pg-dump"}},
		RunBefore: []profile.Hook{{Command: []string{"must-not-run"}}},
	}
	err := Backup(context.Background(), runner, backupProfile, false, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "resticctl-definitely-missing-pg-dump") {
		t.Fatalf("Backup error = %v", err)
	}
	if len(runner.events) != 0 {
		t.Fatalf("events = %v, want none", runner.events)
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

func TestRunResticPassesArgumentsThrough(t *testing.T) {
	runner := &recordingRunner{}
	if err := RunRestic(context.Background(), runner, profile.Profile{Name: "example"}, "tag", []string{"--add", "old", "new"}); err != nil {
		t.Fatal(err)
	}
	want := []string{"tag", "--add", "old", "new"}
	if !slices.Equal(runner.runs[0].arguments, want) {
		t.Fatalf("arguments = %v, want %v", runner.runs[0].arguments, want)
	}
}

func TestRunResticRejectsReservedArgumentsAndUnknownCommands(t *testing.T) {
	for _, argument := range []string{"--repo", "--repo=/tmp/repo", "--password-file", "--password-file=/tmp/pw"} {
		if err := RunRestic(context.Background(), &recordingRunner{}, profile.Profile{}, "snapshots", []string{argument}); err == nil {
			t.Fatalf("reserved argument %q accepted", argument)
		}
	}
	if err := RunRestic(context.Background(), &recordingRunner{}, profile.Profile{}, "not-a-command", nil); err == nil {
		t.Fatal("unknown command accepted")
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

func TestBackupOrchestrationOrder(t *testing.T) {
	directory := t.TempDir()
	backupProfile := profile.Profile{
		Name: "example", BackupPaths: []string{directory},
		ForgetArgs: []string{"--keep-last", "2"}, CheckArgs: []string{"--read-data-subset=1%"},
		CheckBefore: true, PruneBefore: true, CheckAfter: true, PruneAfter: true,
	}
	runner := &recordingRunner{}
	if err := Backup(context.Background(), runner, backupProfile, true, io.Discard); err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"check", "--read-data-subset=1%"},
		{"forget", "--tag", "profile:example", "--group-by", "host,tags", "--keep-last", "2", "--prune", "--dry-run"},
		{"backup", "--group-by", "host,tags", "--tag", "profile:example", "--dry-run", "--", directory},
		{"check", "--read-data-subset=1%"},
		{"forget", "--tag", "profile:example", "--group-by", "host,tags", "--keep-last", "2", "--prune", "--dry-run"},
	}
	if len(runner.runs) != len(want) {
		t.Fatalf("runs = %d, want %d", len(runner.runs), len(want))
	}
	for index := range want {
		if !slices.Equal(runner.runs[index].arguments, want[index]) {
			t.Fatalf("run %d arguments = %v, want %v", index, runner.runs[index].arguments, want[index])
		}
	}
}

func TestBackupStopsAfterOrchestrationFailure(t *testing.T) {
	wantErr := errors.New("check failed")
	runner := &failingRunner{failAt: 1, err: wantErr}
	backupProfile := profile.Profile{
		Name: "example", BackupPaths: []string{t.TempDir()},
		CheckBefore: true, PruneBefore: true, ForgetArgs: []string{"--keep-last", "2"},
	}
	err := Backup(context.Background(), runner, backupProfile, false, io.Discard)
	if !errors.Is(err, wantErr) || !strings.Contains(err.Error(), "check before backup") {
		t.Fatalf("error = %v, want wrapped check failure", err)
	}
	if len(runner.runs) != 1 {
		t.Fatalf("runs = %d, want 1", len(runner.runs))
	}
}

func TestBackupCleansSQLiteStagingBeforeAfterActions(t *testing.T) {
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

	runner := &cleanupCheckingRunner{}
	backupProfile := profile.Profile{
		Name: "example", CheckAfter: true,
		SQLiteDatabases: []profile.SQLiteDatabase{{Name: "primary", Path: source}},
	}
	if err := Backup(context.Background(), runner, backupProfile, false, io.Discard); err != nil {
		t.Fatal(err)
	}
	if runner.staging == "" {
		t.Fatal("backup did not use a staging directory")
	}
}

func TestBackupCleansSQLiteStagingAfterBackupFailure(t *testing.T) {
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

	wantErr := errors.New("backup failed")
	runner := &failingRunner{failAt: 1, err: wantErr}
	backupProfile := profile.Profile{
		Name:            "example",
		SQLiteDatabases: []profile.SQLiteDatabase{{Name: "primary", Path: source}},
	}
	err = Backup(context.Background(), runner, backupProfile, false, io.Discard)
	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want %v", err, wantErr)
	}
	staging := runner.runs[0].cwd
	if _, err := os.Stat(staging); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("staging directory was not removed after failure: %v", err)
	}
}

func TestBackupHookLifecycle(t *testing.T) {
	runner := &hookRecordingRunner{failRestic: errors.New("backup failed")}
	backupProfile := profile.Profile{
		Name: "example", BackupPaths: []string{t.TempDir()},
		RunBefore:    []profile.Hook{{Command: []string{"before", "one"}}},
		RunAfter:     []profile.Hook{{Command: []string{"after"}}},
		RunAfterFail: []profile.Hook{{Command: []string{"failure"}}},
		RunFinally:   []profile.Hook{{Command: []string{"finally"}}},
	}
	err := Backup(context.Background(), runner, backupProfile, false, io.Discard)
	if !errors.Is(err, runner.failRestic) {
		t.Fatalf("error = %v", err)
	}
	want := []string{"hook:before one", "restic:backup", "hook:failure", "hook:finally"}
	if !slices.Equal(runner.events, want) {
		t.Fatalf("events = %v, want %v", runner.events, want)
	}
}

func TestBackupSuccessfulHookLifecycle(t *testing.T) {
	runner := &hookRecordingRunner{}
	backupProfile := profile.Profile{
		Name: "example", BackupPaths: []string{t.TempDir()},
		RunBefore:    []profile.Hook{{Command: []string{"before"}}},
		RunAfter:     []profile.Hook{{Command: []string{"after"}}},
		RunAfterFail: []profile.Hook{{Command: []string{"failure"}}},
		RunFinally:   []profile.Hook{{Command: []string{"finally"}}},
	}
	if err := Backup(context.Background(), runner, backupProfile, false, io.Discard); err != nil {
		t.Fatal(err)
	}
	want := []string{"hook:before", "restic:backup", "hook:after", "hook:finally"}
	if !slices.Equal(runner.events, want) {
		t.Fatalf("events = %v, want %v", runner.events, want)
	}
}

func TestBackupBeforeHookFailureStopsWorkflow(t *testing.T) {
	wantErr := errors.New("before failed")
	runner := &hookRecordingRunner{failHook: "before", hookErr: wantErr}
	backupProfile := profile.Profile{
		Name: "example", BackupPaths: []string{t.TempDir()},
		RunBefore:    []profile.Hook{{Command: []string{"before"}}, {Command: []string{"skipped"}}},
		RunAfterFail: []profile.Hook{{Command: []string{"failure"}}},
		RunFinally:   []profile.Hook{{Command: []string{"finally"}}},
	}
	err := Backup(context.Background(), runner, backupProfile, false, io.Discard)
	if !errors.Is(err, wantErr) || !strings.Contains(err.Error(), "run-before hook 1") {
		t.Fatalf("error = %v", err)
	}
	want := []string{"hook:before", "hook:failure", "hook:finally"}
	if !slices.Equal(runner.events, want) {
		t.Fatalf("events = %v, want %v", runner.events, want)
	}
}

func TestBackupHookTimeout(t *testing.T) {
	runner := &hookRecordingRunner{waitForCancellation: "slow"}
	backupProfile := profile.Profile{
		Name: "example", BackupPaths: []string{t.TempDir()},
		RunBefore:  []profile.Hook{{Command: []string{"slow"}, Timeout: "1ms"}},
		RunFinally: []profile.Hook{{Command: []string{"finally"}}},
	}
	err := Backup(context.Background(), runner, backupProfile, false, io.Discard)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want deadline exceeded", err)
	}
}

func TestBackupCleansSQLiteStagingBeforeFailureHooks(t *testing.T) {
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
	runner := &hookRecordingRunner{failRestic: errors.New("backup failed"), checkStagingCleanup: true}
	backupProfile := profile.Profile{
		Name: "example", SQLiteDatabases: []profile.SQLiteDatabase{{Name: "primary", Path: source}},
		RunAfterFail: []profile.Hook{{Command: []string{"failure"}}},
	}
	if err := Backup(context.Background(), runner, backupProfile, false, io.Discard); !errors.Is(err, runner.failRestic) {
		t.Fatalf("error = %v", err)
	}
}

type hookRecordingRunner struct {
	events              []string
	failRestic          error
	failHook            string
	hookErr             error
	waitForCancellation string
	staging             string
	checkStagingCleanup bool
}

func (runner *hookRecordingRunner) Run(_ context.Context, _ profile.Profile, arguments []string, cwd string) error {
	runner.events = append(runner.events, "restic:"+arguments[0])
	runner.staging = cwd
	return runner.failRestic
}

func (runner *hookRecordingRunner) RunHook(ctx context.Context, arguments []string) error {
	runner.events = append(runner.events, "hook:"+strings.Join(arguments, " "))
	if runner.checkStagingCleanup && arguments[0] == "failure" {
		if _, err := os.Stat(runner.staging); !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("staging directory still exists: %v", err)
		}
	}
	if arguments[0] == runner.waitForCancellation {
		<-ctx.Done()
		return ctx.Err()
	}
	if arguments[0] == runner.failHook {
		return runner.hookErr
	}
	return nil
}
func (runner *hookRecordingRunner) RunDatabase(_ context.Context, _ []string, _ map[string]string, _ string) error {
	return nil
}

type failingRunner struct {
	runs   []recordedRun
	failAt int
	err    error
}

func (runner *failingRunner) Run(_ context.Context, _ profile.Profile, arguments []string, cwd string) error {
	runner.runs = append(runner.runs, recordedRun{arguments: append([]string(nil), arguments...), cwd: cwd})
	if len(runner.runs) == runner.failAt {
		return runner.err
	}
	return nil
}

func (runner *failingRunner) RunHook(_ context.Context, _ []string) error { return nil }
func (runner *failingRunner) RunDatabase(_ context.Context, _ []string, _ map[string]string, _ string) error {
	return nil
}

type cleanupCheckingRunner struct{ staging string }

func (runner *cleanupCheckingRunner) Run(_ context.Context, _ profile.Profile, arguments []string, cwd string) error {
	if arguments[0] == "backup" {
		runner.staging = cwd
		return nil
	}
	if _, err := os.Stat(runner.staging); !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("staging directory still exists during %s: %v", arguments[0], err)
	}
	return nil
}

func (runner *cleanupCheckingRunner) RunHook(_ context.Context, _ []string) error { return nil }
func (runner *cleanupCheckingRunner) RunDatabase(_ context.Context, _ []string, _ map[string]string, _ string) error {
	return nil
}
