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
	"time"

	"resticctl/internal/app"
	"resticctl/internal/profile"
	"resticctl/internal/runstatus"
	"resticctl/internal/schedule"
)

type scheduleExecution struct {
	input     string
	name      string
	arguments []string
}

type recordingScheduleExecutor struct {
	executions []scheduleExecution
	crontab    string
}

func (executor *recordingScheduleExecutor) Run(_ context.Context, input []byte, name string, arguments ...string) ([]byte, error) {
	executor.executions = append(executor.executions, scheduleExecution{
		input: string(input), name: name, arguments: append([]string(nil), arguments...),
	})
	if name == "crontab" && slices.Equal(arguments, []string{"-l"}) {
		return []byte(executor.crontab), nil
	}
	if name == "crontab" && slices.Equal(arguments, []string{"-"}) {
		executor.crontab = string(input)
	}
	return nil, nil
}

func TestBackupRecordsStatusAndStatusCommandReadsIt(t *testing.T) {
	directory := t.TempDir()
	writeCLIProfile(t, directory)
	runner := &recordingRunner{}
	var output bytes.Buffer
	cli := newTestCommandLine(&output, io.Discard)
	cli.newRunner = func() (app.Runner, error) { return runner, nil }

	statusCode, err := cli.run(context.Background(), []string{"backup", "example", "--config-dir", directory})
	if err != nil || statusCode != 0 {
		t.Fatalf("backup status=%d error=%v", statusCode, err)
	}
	output.Reset()
	statusCode, err = cli.run(context.Background(), []string{"status", "example", "--json", "--config-dir", directory})
	if err != nil || statusCode != 0 {
		t.Fatalf("status status=%d error=%v", statusCode, err)
	}
	var status runstatus.Status
	if err := json.Unmarshal(output.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if status.Profile != "example" || status.State != "succeeded" || status.FinishedAt == nil {
		t.Fatalf("status = %#v", status)
	}
}

func TestScheduleInstallAndStatusCommands(t *testing.T) {
	directory := t.TempDir()
	writeCLIProfile(t, directory)
	executor := &recordingScheduleExecutor{}
	manager := newCronManager(executor, func() time.Time {
		return time.Date(2026, 8, 30, 2, 0, 0, 0, time.UTC)
	})
	var output bytes.Buffer
	cli := newTestCommandLine(&output, io.Discard)
	cli.newScheduleManager = func() schedule.Manager { return manager }
	cli.executable = func() (string, error) { return "/usr/local/bin/resticctl", nil }

	statusCode, err := cli.run(context.Background(), []string{
		"schedule", "install", "example", "--cron", "0 2 * * *", "--config-dir", directory,
	})
	if err != nil || statusCode != 0 {
		t.Fatalf("install status=%d error=%v", statusCode, err)
	}
	if !strings.Contains(executor.crontab, "0 2 * * *") || !strings.Contains(executor.crontab, "backup' 'example") {
		t.Fatalf("crontab = %q", executor.crontab)
	}
	output.Reset()
	statusCode, err = cli.run(context.Background(), []string{
		"schedule", "status", "example", "--json", "--config-dir", directory,
	})
	if err != nil || statusCode != 0 {
		t.Fatalf("schedule status=%d error=%v", statusCode, err)
	}
	var result struct {
		Schedule schedule.State    `json:"schedule"`
		LastRun  *runstatus.Status `json:"last_run"`
	}
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Schedule.Backend != schedule.BackendCron || result.LastRun != nil {
		t.Fatalf("result = %#v", result)
	}
	executor.crontab = ""
	statusCode, err = cli.run(context.Background(), []string{
		"schedule", "status", "example", "--config-dir", directory,
	})
	if statusCode != 1 || !errors.Is(err, schedule.ErrDrift) {
		t.Fatalf("drift status=%d error=%v", statusCode, err)
	}
}

func TestDryRunDoesNotReplaceLastBackupStatus(t *testing.T) {
	directory := t.TempDir()
	writeCLIProfile(t, directory)
	recorder, err := runstatus.Begin(directory, "example", time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err := recorder.Finish(nil, time.Now().Add(-time.Hour+time.Second)); err != nil {
		t.Fatal(err)
	}
	before, err := runstatus.Load(directory, "example")
	if err != nil {
		t.Fatal(err)
	}
	runner := &recordingRunner{}
	cli := newTestCommandLine(io.Discard, io.Discard)
	cli.newRunner = func() (app.Runner, error) { return runner, nil }
	statusCode, err := cli.run(context.Background(), []string{"backup", "example", "--dry-run", "--config-dir", directory})
	if err != nil || statusCode != 0 {
		t.Fatalf("dry-run status=%d error=%v", statusCode, err)
	}
	after, err := runstatus.Load(directory, "example")
	if err != nil {
		t.Fatal(err)
	}
	if !after.StartedAt.Equal(before.StartedAt) {
		t.Fatalf("dry run replaced status: before=%s after=%s", before.StartedAt, after.StartedAt)
	}
}

func TestScheduleInstallUsesProfileConfiguration(t *testing.T) {
	directory := t.TempDir()
	writeCLIProfile(t, directory)
	setCLIProfileSchedule(t, directory, &profile.Schedule{Backend: "cron", Cron: "0 4 * * *", CatchUp: true})
	executor := &recordingScheduleExecutor{}
	manager := newCronManager(executor, time.Now)
	cli := newTestCommandLine(io.Discard, io.Discard)
	cli.newScheduleManager = func() schedule.Manager { return manager }
	cli.executable = func() (string, error) { return "/usr/local/bin/resticctl", nil }

	statusCode, err := cli.run(context.Background(), []string{"schedule", "install", "example", "--config-dir", directory})
	if err != nil || statusCode != 0 {
		t.Fatalf("install status=%d error=%v", statusCode, err)
	}
	for _, expected := range []string{"0 4 * * *", "@reboot", "'schedule' 'run' 'example'"} {
		if !strings.Contains(executor.crontab, expected) {
			t.Fatalf("crontab does not contain %q:\n%s", expected, executor.crontab)
		}
	}
	state, err := schedule.Load(directory, "example")
	if err != nil {
		t.Fatal(err)
	}
	if !state.CatchUp {
		t.Fatal("installed schedule does not enable catch-up")
	}
}

func TestScheduleInstallRejectsMissingDatabaseClient(t *testing.T) {
	directory := t.TempDir()
	writeCLIProfile(t, directory)
	setCLIProfileSchedule(t, directory, &profile.Schedule{Cron: "0 2 * * *", Backend: "cron"})
	path := filepath.Join(directory, "example.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var value profile.Profile
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatal(err)
	}
	value.PostgreSQLDatabases = []profile.PostgreSQLDatabase{{Name: "main", Database: "app", Executable: "resticctl-definitely-missing-pg-dump"}}
	data, err = json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	executor := &recordingScheduleExecutor{}
	manager := newCronManager(executor, time.Now)
	cli := newTestCommandLine(io.Discard, io.Discard)
	cli.newScheduleManager = func() schedule.Manager { return manager }
	cli.executable = func() (string, error) { return "/usr/local/bin/resticctl", nil }
	statusCode, runErr := cli.run(context.Background(), []string{"schedule", "install", "example", "--config-dir", directory})
	if statusCode != 1 || runErr == nil || !strings.Contains(runErr.Error(), "resticctl-definitely-missing-pg-dump") {
		t.Fatalf("status=%d error=%v", statusCode, runErr)
	}
	if executor.crontab != "" {
		t.Fatalf("schedule was installed: %s", executor.crontab)
	}
}

func TestScheduleRunSkipsCurrentAndRunsOverdueBackup(t *testing.T) {
	directory := t.TempDir()
	writeCLIProfile(t, directory)
	executor := &recordingScheduleExecutor{}
	installedAt := time.Date(2026, 8, 30, 3, 0, 0, 0, time.Local)
	manager := newCronManager(executor, func() time.Time { return installedAt })
	if _, err := manager.Install(context.Background(), directory, "example", "0 2 * * *", schedule.BackendCron, "/usr/local/bin/resticctl", true); err != nil {
		t.Fatal(err)
	}
	recorder, err := runstatus.Begin(directory, "example", installedAt.Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err := recorder.Finish(nil, installedAt); err != nil {
		t.Fatal(err)
	}
	runner := &recordingRunner{}
	var output bytes.Buffer
	cli := newTestCommandLine(&output, io.Discard)
	cli.newRunner = func() (app.Runner, error) { return runner, nil }
	cli.newScheduleManager = func() schedule.Manager { return manager }
	cli.now = func() time.Time { return installedAt.Add(time.Hour) }

	statusCode, err := cli.run(context.Background(), []string{"schedule", "run", "example", "--config-dir", directory})
	if err != nil || statusCode != 0 {
		t.Fatalf("not-due status=%d error=%v", statusCode, err)
	}
	if len(runner.runs) != 0 || !strings.Contains(output.String(), "not due") {
		t.Fatalf("not-due runs=%d output=%q", len(runner.runs), output.String())
	}

	output.Reset()
	cli.now = func() time.Time { return installedAt.Add(24*time.Hour + time.Minute) }
	statusCode, err = cli.run(context.Background(), []string{"schedule", "run", "example", "--config-dir", directory})
	if err != nil || statusCode != 0 {
		t.Fatalf("overdue status=%d error=%v", statusCode, err)
	}
	if len(runner.runs) != 1 {
		t.Fatalf("overdue runs=%d, want 1", len(runner.runs))
	}
}

func TestScheduledForgetUsesProfileAliasAndPrune(t *testing.T) {
	directory := t.TempDir()
	writeCLIProfile(t, directory)
	setCLIProfileForget(t, directory, &profile.ForgetSchedule{
		Cron: "@daily", Backend: "cron", CatchUp: true, Prune: true,
	})
	executor := &recordingScheduleExecutor{}
	now := time.Date(2026, 8, 30, 3, 0, 0, 0, time.Local)
	manager := newCronManager(executor, func() time.Time { return now })
	runner := &recordingRunner{}
	cli := newTestCommandLine(io.Discard, io.Discard)
	cli.newScheduleManager = func() schedule.Manager { return manager }
	cli.executable = func() (string, error) { return "/usr/local/bin/resticctl", nil }
	cli.newRunner = func() (app.Runner, error) { return runner, nil }
	cli.now = func() time.Time { return now.Add(24*time.Hour + time.Minute) }

	statusCode, err := cli.run(context.Background(), []string{"schedule", "install", "example", "forget", "--config-dir", directory})
	if err != nil || statusCode != 0 {
		t.Fatalf("install status=%d error=%v", statusCode, err)
	}
	for _, expected := range []string{"0 0 * * *", "resticctl:example:forget:begin", "'schedule' 'run' 'example' '--action' 'forget'"} {
		if !strings.Contains(executor.crontab, expected) {
			t.Fatalf("crontab does not contain %q:\n%s", expected, executor.crontab)
		}
	}
	statusCode, err = cli.run(context.Background(), []string{"schedule", "run", "example", "--action", "forget", "--config-dir", directory})
	if err != nil || statusCode != 0 {
		t.Fatalf("run status=%d error=%v", statusCode, err)
	}
	if len(runner.runs) != 1 || !slices.Contains(runner.runs[0].arguments, "--prune") || runner.runs[0].arguments[0] != "forget" {
		t.Fatalf("forget runs = %#v", runner.runs)
	}
	status, err := runstatus.LoadAction(directory, "example", "forget")
	if err != nil {
		t.Fatal(err)
	}
	if status.State != "succeeded" || status.Action != "forget" {
		t.Fatalf("forget status = %#v", status)
	}
}

func newCronManager(executor schedule.Executor, now func() time.Time) schedule.Manager {
	return schedule.NewManager(
		schedule.WithExecutor(executor),
		schedule.WithPlatform("linux", 1000),
		schedule.WithEnvironmentPath(""),
		schedule.WithClock(now),
	)
}

func setCLIProfileSchedule(t *testing.T, directory string, configured *profile.Schedule) {
	t.Helper()
	path := filepath.Join(directory, "example.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var value profile.Profile
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatal(err)
	}
	value.Schedule = configured
	data, err = json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func setCLIProfileForget(t *testing.T, directory string, configured *profile.ForgetSchedule) {
	t.Helper()
	path := filepath.Join(directory, "example.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var value profile.Profile
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatal(err)
	}
	value.Forget = configured
	data, err = json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}
