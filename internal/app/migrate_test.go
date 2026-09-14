package app

import (
	"context"
	"errors"
	"io"
	"os"
	"slices"
	"strings"
	"testing"

	"resticctl/internal/profile"
	"resticctl/internal/restic"
)

type migrationRecordingRunner struct {
	recordingRunner
	snapshots map[string][]restic.SnapshotIdentity
}

type failingMigrationRestoreRunner struct {
	migrationRecordingRunner
	target string
}

func (runner *failingMigrationRestoreRunner) Run(_ context.Context, config restic.Config, arguments []string, cwd string) error {
	runner.runs = append(runner.runs, recordedRun{config: config, arguments: append([]string(nil), arguments...), cwd: cwd})
	if len(arguments) == 0 || arguments[0] != "restore" {
		return nil
	}
	for index, argument := range arguments {
		if argument == "--target" && index+1 < len(arguments) {
			runner.target = arguments[index+1]
			break
		}
	}
	if err := os.WriteFile(runner.target+"/plaintext", []byte("test"), 0o600); err != nil {
		return err
	}
	return errors.New("restore failed")
}

func (runner *migrationRecordingRunner) SnapshotIdentities(_ context.Context, config restic.Config, arguments []string) ([]restic.SnapshotIdentity, error) {
	runner.runs = append(runner.runs, recordedRun{config: config, arguments: append([]string{"snapshots", "--json"}, arguments...)})
	return append([]restic.SnapshotIdentity(nil), runner.snapshots[config.Repository]...), nil
}

func TestVerifyMigrationUsesDestination(t *testing.T) {
	runner := &migrationRecordingRunner{snapshots: map[string][]restic.SnapshotIdentity{
		"local:source": {{ID: "snapshot-1"}}, "local:destination": {{ID: "new-id", Original: "snapshot-1"}},
	}}
	value := profile.Profile{
		Name: "home", Repository: "local:source",
		Copies: map[string]profile.CopyTarget{"offsite": {
			Repository:  "local:destination",
			Credentials: profile.RepositoryCredentials{Password: profile.PasswordSource{Value: "destination-secret"}},
		}},
	}
	err := VerifyMigration(context.Background(), func() (Runner, error) { return runner, nil }, t.TempDir(), value, "offsite", false, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if len(runner.runs) != 3 || runner.runs[0].config.Repository != "local:source" || runner.runs[1].config.Repository != "local:destination" || runner.runs[2].config.Repository != "local:destination" {
		t.Fatalf("migration verification runs = %#v", runner.runs)
	}
	if runner.runs[0].arguments[0] != "snapshots" || runner.runs[1].arguments[0] != "snapshots" || runner.runs[2].arguments[0] != "check" {
		t.Fatalf("migration verification commands = %#v", runner.runs)
	}
}

func TestVerifyMigrationRejectsMissingSnapshots(t *testing.T) {
	runner := &migrationRecordingRunner{snapshots: map[string][]restic.SnapshotIdentity{
		"local:source": {{ID: "present"}, {ID: "missing"}}, "local:destination": {{ID: "new-id", Original: "present"}},
	}}
	value := profile.Profile{
		Name: "home", Repository: "local:source",
		Copies: map[string]profile.CopyTarget{"offsite": {Repository: "local:destination"}},
	}
	err := VerifyMigration(context.Background(), func() (Runner, error) { return runner, nil }, t.TempDir(), value, "offsite", false, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("verification error = %v", err)
	}
}

func TestMissingSnapshotIDsUsesOriginalIdentityAcrossCopyChains(t *testing.T) {
	source := []restic.SnapshotIdentity{{ID: "second-repository-id", Original: "first-repository-id"}}
	destination := []restic.SnapshotIdentity{{ID: "third-repository-id", Original: "first-repository-id"}}
	if missing := missingSnapshotIDs(source, destination); len(missing) != 0 {
		t.Fatalf("missing snapshots = %q", missing)
	}
}

func TestVerifyMigrationTestRestoreUsesDestinationAndCleansPlaintext(t *testing.T) {
	runner := &migrationRecordingRunner{snapshots: map[string][]restic.SnapshotIdentity{
		"local:source": {{ID: "source-id"}}, "local:destination": {{ID: "destination-id", Original: "source-id"}},
	}}
	value := profile.Profile{
		Name: "home", Repository: "local:source",
		Commands: map[string]profile.ResticCommand{"restore": {Args: []string{"--target", "/uncontrolled"}}},
		Copies:   map[string]profile.CopyTarget{"offsite": {Repository: "local:destination"}},
	}
	if err := VerifyMigration(context.Background(), func() (Runner, error) { return runner, nil }, t.TempDir(), value, "offsite", true, io.Discard); err != nil {
		t.Fatal(err)
	}
	restore := runner.runs[len(runner.runs)-1]
	if restore.config.Repository != "local:destination" || restore.arguments[0] != "restore" || restore.arguments[1] != "destination-id" {
		t.Fatalf("restore run = %#v", restore)
	}
	var target string
	for index, argument := range restore.arguments {
		if argument == "--target" && index+1 < len(restore.arguments) {
			target = restore.arguments[index+1]
		}
	}
	if target == "" {
		t.Fatalf("restore has no target: %q", restore.arguments)
	}
	if slices.Contains(restore.arguments, "/uncontrolled") {
		t.Fatalf("test restore used profile restore arguments: %q", restore.arguments)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("test restore directory was not removed: %s: %v", target, err)
	}
}

func TestVerifyMigrationCleansFailedTestRestore(t *testing.T) {
	runner := &failingMigrationRestoreRunner{migrationRecordingRunner: migrationRecordingRunner{snapshots: map[string][]restic.SnapshotIdentity{
		"local:source": {{ID: "source-id"}}, "local:destination": {{ID: "destination-id", Original: "source-id"}},
	}}}
	value := profile.Profile{
		Name: "home", Repository: "local:source",
		Copies: map[string]profile.CopyTarget{"offsite": {Repository: "local:destination"}},
	}
	err := VerifyMigration(context.Background(), func() (Runner, error) { return runner, nil }, t.TempDir(), value, "offsite", true, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "restore failed") {
		t.Fatalf("verification error = %v", err)
	}
	if _, statErr := os.Stat(runner.target); !os.IsNotExist(statErr) {
		t.Fatalf("failed test restore directory was not removed: %s: %v", runner.target, statErr)
	}
}
