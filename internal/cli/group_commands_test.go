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
	"time"

	"resticctl/internal/app"
	"resticctl/internal/profile"
	"resticctl/internal/restic"
	"resticctl/internal/runstatus"
	"resticctl/internal/schedule"
	"resticctl/internal/securefile"
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

func TestGroupCreateSupportsPositionalAndFlagForms(t *testing.T) {
	for _, test := range []struct {
		name      string
		arguments []string
	}{
		{"positional", []string{"group", "create", "daily", "home", "databases", "--continue-on-error"}},
		{"flags", []string{"group", "create", "--group", "daily", "--profile", "home", "--profile", "databases", "--continue-on-error"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			writeGroupCLIProfile(t, directory, "home")
			writeGroupCLIProfile(t, directory, "databases")
			arguments := append(test.arguments, "--config-dir", directory)
			var output bytes.Buffer
			status, err := runForTest(context.Background(), arguments, &output, io.Discard)
			if status != 0 || err != nil {
				t.Fatalf("status=%d error=%v", status, err)
			}
			data, err := os.ReadFile(filepath.Join(directory, "groups", "daily.json"))
			if err != nil {
				t.Fatal(err)
			}
			var configured struct {
				Name            string   `json:"name"`
				Profiles        []string `json:"profiles"`
				ContinueOnError bool     `json:"continue_on_error"`
			}
			if err := json.Unmarshal(data, &configured); err != nil {
				t.Fatal(err)
			}
			if configured.Name != "daily" || strings.Join(configured.Profiles, ",") != "home,databases" || !configured.ContinueOnError {
				t.Fatalf("group = %#v", configured)
			}
			if !strings.Contains(output.String(), "Created group daily") {
				t.Fatalf("output = %q", output.String())
			}
		})
	}
}

func TestGroupCreateRejectsMixedSelectors(t *testing.T) {
	var stderr bytes.Buffer
	status, err := runForTest(context.Background(), []string{
		"group", "create", "daily", "home", "--profile", "databases",
	}, io.Discard, &stderr)
	if status != 2 || err == nil || !strings.Contains(err.Error(), "must not be combined") {
		t.Fatalf("status=%d error=%v", status, err)
	}
	if !strings.Contains(stderr.String(), "Usage:") {
		t.Fatalf("usage not printed:\n%s", stderr.String())
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

func TestGroupStopsAfterCancellationDespiteContinueOnError(t *testing.T) {
	directory := t.TempDir()
	writeGroupCLIProfile(t, directory, "home")
	writeGroupCLIProfile(t, directory, "databases")
	writeCLIGroup(t, directory, `{"profiles":["home","databases"],"continue_on_error":true}`)
	runner := &groupRunner{fail: map[int]error{1: context.Canceled}}
	cli := newTestCommandLine(io.Discard, io.Discard)
	cli.newRunner = func() (app.Runner, error) { return runner, nil }

	status, err := cli.run(context.Background(), []string{"group", "backup", "daily", "--dry-run", "--config-dir", directory})
	if status != 1 || !errors.Is(err, context.Canceled) || runner.runs != 1 {
		t.Fatalf("status=%d error=%v runs=%d", status, err, runner.runs)
	}
}

func TestGroupCheckRecordsMemberAndAggregateStatus(t *testing.T) {
	directory := t.TempDir()
	writeGroupCLIProfile(t, directory, "home")
	writeGroupCLIProfile(t, directory, "databases")
	writeCLIGroup(t, directory, `{"profiles":["home","databases"]}`)
	runner := &groupRunner{}
	cli := newTestCommandLine(io.Discard, io.Discard)
	cli.newRunner = func() (app.Runner, error) { return runner, nil }

	statusCode, err := cli.run(context.Background(), []string{"group", "check", "daily", "--config-dir", directory})
	if statusCode != 0 || err != nil {
		t.Fatalf("status=%d error=%v", statusCode, err)
	}
	if runner.runs != 2 {
		t.Fatalf("runs = %d, want 2", runner.runs)
	}
	groupStatus, err := runstatus.LoadGroupAction(directory, "daily", "check")
	if err != nil || groupStatus.State != "succeeded" {
		t.Fatalf("group status = %#v, error = %v", groupStatus, err)
	}
	for _, name := range []string{"home", "databases"} {
		memberStatus, err := runstatus.LoadAction(directory, name, "check")
		if err != nil || memberStatus.State != "succeeded" {
			t.Fatalf("member %s status = %#v, error = %v", name, memberStatus, err)
		}
	}
}

func TestGroupActionCannotOverlapMemberAction(t *testing.T) {
	directory := t.TempDir()
	writeGroupCLIProfile(t, directory, "home")
	writeCLIGroup(t, directory, `{"profiles":["home"]}`)
	member, err := runstatus.BeginAction(directory, "home", schedule.ActionCheck, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	defer member.Finish(nil, time.Now())
	cli := newTestCommandLine(io.Discard, io.Discard)
	cli.newRunner = func() (app.Runner, error) { return &groupRunner{}, nil }

	statusCode, err := cli.run(context.Background(), []string{"group", "check", "daily", "--config-dir", directory})
	if statusCode != 1 || !errors.Is(err, runstatus.ErrLocked) {
		t.Fatalf("status=%d error=%v", statusCode, err)
	}
	groupStatus, loadErr := runstatus.LoadGroupAction(directory, "daily", schedule.ActionCheck)
	if loadErr != nil || groupStatus.State != "failed" {
		t.Fatalf("group status = %#v, error = %v", groupStatus, loadErr)
	}
}

func TestScheduledGroupActionRunsMembersAndRecordsStatus(t *testing.T) {
	directory := t.TempDir()
	writeGroupCLIProfile(t, directory, "home")
	writeGroupCLIProfile(t, directory, "databases")
	writeCLIGroup(t, directory, `{"profiles":["home","databases"]}`)
	executor := &recordingScheduleExecutor{}
	manager := newCronManager(executor, time.Now)
	_, err := manager.InstallSpec(context.Background(), schedule.Spec{
		Name: "daily", TargetType: schedule.TargetGroup, Action: schedule.ActionCheck,
		Expressions: []string{"@daily"}, Backend: schedule.BackendCron, Executable: "/bin/resticctl",
		ConfigDir: directory, Permission: schedule.PermissionUser, Enabled: true, Start: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	runner := &groupRunner{}
	cli := newTestCommandLine(io.Discard, io.Discard)
	cli.newRunner = func() (app.Runner, error) { return runner, nil }
	cli.newScheduleManager = func() schedule.Manager { return manager }

	statusCode, err := cli.run(context.Background(), []string{"schedule", "run", "daily", "--group", "--action", "check", "--config-dir", directory})
	if statusCode != 0 || err != nil || runner.runs != 2 {
		t.Fatalf("status=%d error=%v runs=%d", statusCode, err, runner.runs)
	}
	groupStatus, err := runstatus.LoadGroupAction(directory, "daily", schedule.ActionCheck)
	if err != nil || groupStatus.State != "succeeded" {
		t.Fatalf("group status = %#v, error = %v", groupStatus, err)
	}
}

func TestGroupScheduleReconcileInstallsAndRemovesDeclarations(t *testing.T) {
	directory := t.TempDir()
	writeGroupCLIProfile(t, directory, "home")
	writeCLIGroup(t, directory, `{"profiles":["home"],"schedules":{"backup":{"cron":"@daily","backend":"cron","catch_up":true}}}`)
	executor := &recordingScheduleExecutor{}
	manager := newCronManager(executor, time.Now)
	cli := newTestCommandLine(io.Discard, io.Discard)
	cli.newScheduleManager = func() schedule.Manager { return manager }

	statusCode, err := cli.run(context.Background(), []string{"schedule", "reconcile", "daily", "--group", "--config-dir", directory})
	if statusCode != 0 || err != nil {
		t.Fatalf("install status=%d error=%v", statusCode, err)
	}
	state, err := schedule.LoadTargetAction(directory, schedule.TargetGroup, "daily", schedule.ActionBackup)
	if err != nil || !state.CatchUp {
		t.Fatalf("state = %#v, error = %v", state, err)
	}
	writeCLIGroup(t, directory, `{"profiles":["home"]}`)
	statusCode, err = cli.run(context.Background(), []string{"schedule", "reconcile", "daily", "--group", "--config-dir", directory})
	if statusCode != 0 || err != nil {
		t.Fatalf("remove status=%d error=%v", statusCode, err)
	}
	if _, err := schedule.LoadTargetAction(directory, schedule.TargetGroup, "daily", schedule.ActionBackup); !errors.Is(err, schedule.ErrNotInstalled) {
		t.Fatalf("schedule was not removed: %v", err)
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
	path := filepath.Join(profilesDir, name+".json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := securefile.Protect(path); err != nil {
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
