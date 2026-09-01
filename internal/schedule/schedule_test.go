package schedule

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

type execution struct {
	input     string
	name      string
	arguments []string
}

type fakeExecutor struct {
	executions      []execution
	crontab         string
	crontabError    error
	launchctlOutput []byte
	launchctlError  error
}

func (executor *fakeExecutor) Run(_ context.Context, input []byte, name string, arguments ...string) ([]byte, error) {
	executor.executions = append(executor.executions, execution{input: string(input), name: name, arguments: append([]string(nil), arguments...)})
	if name == "crontab" && slices.Equal(arguments, []string{"-l"}) {
		if executor.crontabError != nil {
			return []byte("permission denied"), executor.crontabError
		}
		return []byte(executor.crontab), nil
	}
	if name == "crontab" && slices.Equal(arguments, []string{"-"}) {
		executor.crontab = string(input)
	}
	if name == "launchctl" && executor.launchctlError != nil {
		return executor.launchctlOutput, executor.launchctlError
	}
	return nil, nil
}

func TestCronInstallReplaceAndRemove(t *testing.T) {
	directory := t.TempDir()
	executor := &fakeExecutor{crontab: "MAILTO=user@example.com\n"}
	now := time.Date(2026, 8, 30, 1, 2, 3, 0, time.UTC)
	manager := Manager{executor: executor, goos: "linux", uid: 1000, now: func() time.Time { return now }}

	executable, err := filepath.Abs("/usr/local/bin/resticctl")
	if err != nil {
		t.Fatal(err)
	}
	state, err := manager.Install(context.Background(), directory, "example", "15 2 * * *", BackendAuto, executable, false)
	if err != nil {
		t.Fatal(err)
	}
	if state.Backend != BackendCron || state.Expression != "15 2 * * *" {
		t.Fatalf("state = %#v", state)
	}
	for _, expected := range []string{"MAILTO=user@example.com", "# resticctl:example:begin", "15 2 * * *", shellQuote(executable), "'--config-dir'", "'backup'", "'example'"} {
		if !strings.Contains(executor.crontab, expected) {
			t.Fatalf("crontab does not contain %q:\n%s", expected, executor.crontab)
		}
	}
	loaded, err := Load(directory, "example")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Installed != now {
		t.Fatalf("installed = %s, want %s", loaded.Installed, now)
	}

	if _, err := manager.Install(context.Background(), directory, "example", "30 3 * * *", BackendCron, executable, false); err != nil {
		t.Fatal(err)
	}
	if strings.Count(executor.crontab, "# resticctl:example:begin") != 1 || strings.Contains(executor.crontab, "15 2 * * *") {
		t.Fatalf("schedule was not replaced:\n%s", executor.crontab)
	}
	if err := manager.Remove(context.Background(), directory, "example"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(executor.crontab, "resticctl:example") {
		t.Fatalf("schedule was not removed:\n%s", executor.crontab)
	}
	if _, err := os.Stat(filepath.Join(directory, "schedules", "example.json")); !os.IsNotExist(err) {
		t.Fatalf("schedule state still exists: %v", err)
	}
}

func TestLaunchdInstallRendersPrivatePlist(t *testing.T) {
	directory := t.TempDir()
	executor := &fakeExecutor{}
	launchAgents := filepath.Join(directory, "Library", "LaunchAgents")
	manager := Manager{executor: executor, goos: "darwin", uid: 501, launchAgentsDir: launchAgents, now: time.Now}
	executable, err := filepath.Abs("/Applications/Restic Tools/resticctl")
	if err != nil {
		t.Fatal(err)
	}
	state, err := manager.Install(context.Background(), directory, "mac", "5 1 * * *", BackendAuto, executable, true)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(state.JobFile)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	for _, expected := range []string{"io.resticctl.backup.mac", "<key>Minute</key><integer>5</integer>", "<key>Hour</key><integer>1</integer>", xmlText(executable), "<string>schedule</string>", "<string>run</string>", "<string>mac</string>", "<key>RunAtLoad</key><true/>"} {
		if !strings.Contains(content, expected) {
			t.Fatalf("plist does not contain %q:\n%s", expected, content)
		}
	}
	info, err := os.Stat(state.JobFile)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("plist mode = %o, want 600", info.Mode().Perm())
	}
	if filepath.Dir(state.JobFile) != launchAgents {
		t.Fatalf("job file = %s, want directory %s", state.JobFile, launchAgents)
	}
	last := executor.executions[len(executor.executions)-1]
	if last.name != "launchctl" || !slices.Equal(last.arguments, []string{"bootstrap", "gui/501", state.JobFile}) {
		t.Fatalf("launchctl execution = %#v", last)
	}
	legacyPath := filepath.Join(directory, "schedules", launchdLabel("mac", ActionBackup)+".plist")
	if err := os.WriteFile(legacyPath, []byte("legacy"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := manager.Remove(context.Background(), directory, "mac"); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{state.JobFile, legacyPath} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("launchd job still exists at %s: %v", path, err)
		}
	}
}

func TestVerifyLaunchdScheduleDetectsMissingJob(t *testing.T) {
	directory := t.TempDir()
	manager := Manager{
		executor: &fakeExecutor{}, goos: "darwin", uid: 501,
		launchAgentsDir: filepath.Join(directory, "Library", "LaunchAgents"), now: time.Now,
	}
	state, err := manager.Install(context.Background(), directory, "mac", "5 1 * * *", BackendLaunchd, "/bin/resticctl", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Verify(context.Background(), state); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(state.JobFile); err != nil {
		t.Fatal(err)
	}
	if err := manager.Verify(context.Background(), state); !errors.Is(err, ErrDrift) {
		t.Fatalf("Verify error = %v, want drift", err)
	}
}

func TestBackupAndForgetSchedulesAreIndependent(t *testing.T) {
	directory := t.TempDir()
	executor := &fakeExecutor{}
	manager := Manager{executor: executor, goos: "linux", uid: 1000, now: time.Now}
	if _, err := manager.Install(context.Background(), directory, "example", "0 2 * * *", BackendCron, "/bin/resticctl", false); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.InstallAction(context.Background(), directory, "example", ActionForget, "@daily", BackendCron, "/bin/resticctl", true, true); err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{"resticctl:example:begin", "resticctl:example:forget:begin"} {
		if !strings.Contains(executor.crontab, marker) {
			t.Fatalf("crontab does not contain %q:\n%s", marker, executor.crontab)
		}
	}
	forget, err := LoadAction(directory, "example", ActionForget)
	if err != nil {
		t.Fatal(err)
	}
	if forget.Expression != "0 0 * * *" || !forget.Prune || !forget.CatchUp {
		t.Fatalf("forget schedule = %#v", forget)
	}
	if err := manager.RemoveAction(context.Background(), directory, "example", ActionForget); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(executor.crontab, "resticctl:example:begin") || strings.Contains(executor.crontab, "resticctl:example:forget:begin") {
		t.Fatalf("removing forget affected backup schedule:\n%s", executor.crontab)
	}
}

func TestScheduleValidation(t *testing.T) {
	manager := Manager{executor: &fakeExecutor{}, goos: "linux", uid: 1000, now: time.Now}
	for _, expression := range []string{"", "0 1 * *", "0 1 * * *\n* * * * *", "0 1 $ * *"} {
		if _, err := manager.Install(context.Background(), t.TempDir(), "example", expression, BackendCron, "/bin/resticctl", false); err == nil {
			t.Fatalf("expression %q was accepted", expression)
		}
	}
}

func TestVerifyCronScheduleDetectsDrift(t *testing.T) {
	directory := t.TempDir()
	executor := &fakeExecutor{}
	manager := Manager{executor: executor, goos: "linux", uid: 1000, now: time.Now}
	state, err := manager.Install(context.Background(), directory, "example", "15 2 * * *", BackendCron, "/bin/resticctl", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Verify(context.Background(), state); err != nil {
		t.Fatal(err)
	}
	executor.crontab = strings.Replace(executor.crontab, "'backup'", "'forget'", 1)
	if err := manager.Verify(context.Background(), state); !errors.Is(err, ErrDrift) {
		t.Fatalf("Verify error = %v, want drift", err)
	}
}

func TestVerifyCronSchedulePreservesReadError(t *testing.T) {
	directory := t.TempDir()
	executor := &fakeExecutor{}
	manager := Manager{executor: executor, goos: "linux", uid: 1000, now: time.Now}
	state, err := manager.Install(context.Background(), directory, "example", "15 2 * * *", BackendCron, "/bin/resticctl", false)
	if err != nil {
		t.Fatal(err)
	}
	executor.crontabError = errors.New("crontab unavailable")
	err = manager.Verify(context.Background(), state)
	if err == nil || errors.Is(err, ErrDrift) || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("Verify error = %v, want operational read error", err)
	}
}

func TestRemoveLaunchdScheduleAlreadyUnloaded(t *testing.T) {
	directory := t.TempDir()
	executor := &fakeExecutor{}
	manager := Manager{
		executor: executor, goos: "darwin", uid: 501,
		launchAgentsDir: filepath.Join(directory, "Library", "LaunchAgents"), now: time.Now,
	}
	state, err := manager.Install(context.Background(), directory, "mac", "5 1 * * *", BackendLaunchd, "/bin/resticctl", false)
	if err != nil {
		t.Fatal(err)
	}
	executor.launchctlOutput = []byte("Boot-out failed: 3: No such process")
	executor.launchctlError = errors.New("exit status 3")
	if err := manager.Remove(context.Background(), directory, "mac"); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{state.JobFile, statePath(directory, "mac", ActionBackup)} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("schedule file still exists at %s: %v", path, err)
		}
	}
}

func TestNoCrontabDetectionRequiresExpectedDiagnostic(t *testing.T) {
	if !isNoCrontab(1, []byte("no crontab for user")) {
		t.Fatal("expected missing crontab diagnostic was rejected")
	}
	for _, test := range []struct {
		code   int
		output string
	}{{1, "permission denied"}, {1, ""}, {2, "no crontab for user"}} {
		if isNoCrontab(test.code, []byte(test.output)) {
			t.Fatalf("exit %d output %q was treated as a missing crontab", test.code, test.output)
		}
	}
}

func TestLaunchdRejectsUnsupportedCronSyntax(t *testing.T) {
	manager := Manager{executor: &fakeExecutor{}, goos: "darwin", uid: 501, launchAgentsDir: t.TempDir(), now: time.Now}
	if _, err := manager.Install(context.Background(), t.TempDir(), "example", "*/5 * * * *", BackendLaunchd, "/bin/resticctl", false); err == nil {
		t.Fatal("launchd accepted a step expression")
	}
}

func TestSystemdUserInstallRendersMultipleCalendarsAndPolicies(t *testing.T) {
	directory := t.TempDir()
	units := filepath.Join(directory, "units")
	executable, err := filepath.Abs("/bin/resticctl")
	if err != nil {
		t.Fatal(err)
	}
	executor := &fakeExecutor{}
	manager := NewManager(
		WithExecutor(executor),
		WithPlatform("linux", 1000),
		WithSystemdDirs(units, filepath.Join(directory, "system")),
		WithClock(time.Now),
	)
	state, err := manager.InstallSpec(context.Background(), Spec{
		Name:        "example",
		Action:      ActionCheck,
		Backend:     BackendSystemd,
		Executable:  "/bin/resticctl",
		ConfigDir:   directory,
		Expressions: []string{"0 2 * * *", "30 14 * * *"},
		Permission:  PermissionUser,
		Priority:    PriorityBackground,
		Log:         filepath.Join(directory, "check.log"),
		CatchUp:     true,
		Enabled:     true,
		Network:     true,
	})
	if err != nil {
		t.Fatal(err)
	}
	service, err := os.ReadFile(filepath.Join(units, nativeID(state)+".service"))
	if err != nil {
		t.Fatal(err)
	}
	timer, err := os.ReadFile(state.JobFile)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"ExecStart=" + systemdEscape(executable), "\"--action\" \"check\"", "Nice=10", "network-online.target", "StandardOutput=append:"} {
		if !strings.Contains(string(service), expected) {
			t.Fatalf("service lacks %q:\n%s", expected, service)
		}
	}
	if strings.Count(string(timer), "OnCalendar=") != 2 || !strings.Contains(string(timer), "Persistent=true") {
		t.Fatalf("timer:\n%s", timer)
	}
	for _, execution := range executor.executions {
		if slices.Contains(execution.arguments, "start") || slices.Contains(execution.arguments, "--now") {
			t.Fatalf("no-start ran %#v", execution)
		}
	}
}

func TestWindowsDryRunRendersEveryScheduledActionWithoutChanges(t *testing.T) {
	for _, action := range []string{ActionBackup, ActionCheck, ActionForget, ActionPrune, ActionCopy} {
		t.Run(action, func(t *testing.T) {
			directory := t.TempDir()
			executor := &fakeExecutor{}
			manager := NewManager(WithExecutor(executor), WithPlatform("windows", 0), WithClock(time.Now))
			state, err := manager.InstallSpec(context.Background(), Spec{
				Name: "example", Action: action, Backend: BackendWindows,
				Executable: `C:\Tools\resticctl.exe`, ConfigDir: directory,
				Expressions: []string{"5 1 * * *"}, Permission: PermissionLoggedOn,
				User: "Example\\User", Enabled: true, DryRun: true,
			})
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(state.Rendered, "<Task") || !strings.Contains(state.Rendered, action) {
				t.Fatalf("rendered task:\n%s", state.Rendered)
			}
			if len(executor.executions) != 0 {
				t.Fatalf("dry run executed commands: %#v", executor.executions)
			}
			if _, err := os.Stat(statePath(directory, "example", action)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("dry run wrote state: %v", err)
			}
		})
	}
}

func TestExplicitSystemCrontabIncludesUserAndMultipleExpressions(t *testing.T) {
	directory := t.TempDir()
	cronFile := filepath.Join(directory, "etc", "cron.d", "resticctl")
	manager := NewManager(WithExecutor(&fakeExecutor{}), WithPlatform("linux", 0), WithClock(time.Now))
	state, err := manager.InstallSpec(context.Background(), Spec{
		Name: "example", Action: ActionPrune, Backend: BackendCron,
		Executable: "/bin/resticctl", ConfigDir: directory,
		Expressions: []string{"0 1 * * *", "0 13 * * *"},
		Permission:  PermissionSystem, User: "backup", CronFile: cronFile,
		Enabled: true, Start: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(cronFile)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(data), " backup ") != 2 || !strings.Contains(string(data), "'--action' 'prune'") {
		t.Fatalf("system crontab:\n%s", data)
	}
	if err := manager.Verify(context.Background(), state); err != nil {
		t.Fatal(err)
	}
}

func TestFakeInstallationCoversEveryBackendAndAction(t *testing.T) {
	backends := []struct{ backend, goos string }{
		{BackendCron, "linux"},
		{BackendLaunchd, "darwin"},
		{BackendSystemd, "linux"},
		{BackendWindows, "windows"},
	}
	actions := []string{ActionBackup, ActionCheck, ActionForget, ActionPrune, ActionCopy}
	for _, platform := range backends {
		for _, action := range actions {
			t.Run(platform.backend+"/"+action, func(t *testing.T) {
				directory := t.TempDir()
				executor := &fakeExecutor{}
				manager := NewManager(
					WithExecutor(executor), WithPlatform(platform.goos, 1000),
					WithLaunchAgentsDir(filepath.Join(directory, "launchd")),
					WithSystemdDirs(filepath.Join(directory, "systemd-user"), filepath.Join(directory, "systemd-system")),
					WithClock(time.Now),
				)
				state, err := manager.InstallSpec(context.Background(), Spec{
					Name: "example", Action: action, Backend: platform.backend,
					Executable: "/bin/resticctl", ConfigDir: directory,
					Expressions: []string{"5 1 * * *"}, Permission: PermissionUser,
					Enabled: true, Start: true,
				})
				if err != nil {
					t.Fatal(err)
				}
				if state.Action != action || state.DefinitionHash == "" {
					t.Fatalf("state = %#v", state)
				}
				if len(executor.executions) == 0 {
					t.Fatal("installation did not use fake executor")
				}
			})
		}
	}
}
