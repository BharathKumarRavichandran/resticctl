package schedule

import (
	"context"
	"encoding/xml"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func TestMovingCronFileRemovesPreviousInstallation(t *testing.T) {
	directory := t.TempDir()
	manager := NewManager(WithPlatform("linux", 1000), WithExecutor(&fakeExecutor{}))
	spec := Spec{Name: "home", Action: ActionBackup, ConfigDir: directory, Executable: filepath.Join(directory, "resticctl"), Backend: BackendCron, Expressions: []string{"0 2 * * *"}, CronFile: filepath.Join(directory, "old.cron"), Enabled: true, Start: true}
	if _, err := manager.InstallSpec(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	old := spec.CronFile
	spec.CronFile = filepath.Join(directory, "new.cron")
	if _, err := manager.InstallSpec(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(old)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "resticctl:home") {
		t.Fatalf("old job remains: %s", data)
	}
	if err := manager.Remove(context.Background(), directory, "home"); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(spec.CronFile)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "resticctl:home") {
		t.Fatalf("new job remains: %s", data)
	}
}

func TestSystemdReconcilesActivationAndPermission(t *testing.T) {
	directory := t.TempDir()
	executor := &fakeExecutor{}
	manager := NewManager(WithPlatform("linux", 1000), WithExecutor(executor), WithSystemdDirs(filepath.Join(directory, "user"), filepath.Join(directory, "system")))
	spec := Spec{Name: "home", Action: ActionBackup, ConfigDir: directory, Executable: filepath.Join(directory, "resticctl"), Backend: BackendSystemd, Expressions: []string{"0 2 * * *"}, Enabled: true, Start: true}
	old, err := manager.InstallSpec(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	executor.executions = nil
	spec.Enabled = false
	spec.Start = false
	if _, err := manager.InstallSpec(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"disable", "stop"} {
		found := false
		for _, call := range executor.executions {
			if slices.Equal(call.arguments, []string{"--user", action, "resticctl-backup-home.timer"}) {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing %s: %v", action, executor.executions)
		}
	}
	spec.Permission = PermissionSystem
	if _, err := manager.InstallSpec(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(old.JobFile); !os.IsNotExist(err) {
		t.Fatalf("old unit remains: %v", err)
	}
}

func TestWindowsTaskNamespaceAndDefaultPrincipal(t *testing.T) {
	manager := NewManager()
	definition, err := manager.renderWindows(t.TempDir(), State{Expressions: []string{"0 2 * * *"}}, "resticctl.exe")
	if err != nil {
		t.Fatal(err)
	}
	var task struct {
		XMLName    xml.Name
		Principals struct {
			Principal struct {
				UserID string `xml:"UserId"`
			}
		}
	}
	if err := xml.Unmarshal(definition, &task); err != nil {
		t.Fatal(err)
	}
	if task.XMLName.Space != "http://schemas.microsoft.com/windows/2004/02/mit/task" || task.Principals.Principal.UserID == "" {
		t.Fatalf("invalid task: %s", definition)
	}
}

func TestUnstartedLaunchdInstallationVerifies(t *testing.T) {
	directory := t.TempDir()
	executor := &fakeExecutor{}
	manager := NewManager(WithExecutor(executor), WithPlatform("darwin", 1000), WithLaunchAgentsDir(filepath.Join(directory, "agents")))
	state, err := manager.InstallSpec(context.Background(), Spec{Name: "home", Action: ActionBackup, ConfigDir: directory, Executable: filepath.Join(directory, "resticctl"), Backend: BackendLaunchd, Expressions: []string{"0 2 * * *"}, Enabled: true, Start: false})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Verify(context.Background(), state); err != nil {
		t.Fatal(err)
	}
	if len(executor.executions) != 0 {
		t.Fatalf("unstarted service inspected: %v", executor.executions)
	}
	if err := os.Remove(state.JobFile); err != nil {
		t.Fatal(err)
	}
	if err := manager.Verify(context.Background(), state); err == nil {
		t.Fatal("missing definition accepted")
	}
}

func TestMovingNativeDirectoryRemovesRecordedJob(t *testing.T) {
	for _, backend := range []string{BackendLaunchd, BackendSystemd} {
		t.Run(backend, func(t *testing.T) {
			directory := t.TempDir()
			executor := &fakeExecutor{}
			platform := "linux"
			if backend == BackendLaunchd {
				platform = "darwin"
			}
			oldDir := filepath.Join(directory, "old")
			newDir := filepath.Join(directory, "new")
			manager := NewManager(WithExecutor(executor), WithPlatform(platform, 1000), WithLaunchAgentsDir(oldDir), WithSystemdDirs(oldDir, oldDir))
			spec := Spec{Name: "home", Action: ActionBackup, ConfigDir: directory, Executable: filepath.Join(directory, "resticctl"), Backend: backend, Expressions: []string{"0 2 * * *"}, Enabled: true, Start: true}
			old, err := manager.InstallSpec(context.Background(), spec)
			if err != nil {
				t.Fatal(err)
			}
			manager = NewManager(WithExecutor(executor), WithPlatform(platform, 1000), WithLaunchAgentsDir(newDir), WithSystemdDirs(newDir, newDir))
			state, err := manager.InstallSpec(context.Background(), spec)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(old.JobFile); !os.IsNotExist(err) {
				t.Fatalf("previous job remains: %v", err)
			}
			if err := manager.Verify(context.Background(), state); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestInstallSpecUsesExplicitEnvironmentPath(t *testing.T) {
	directory := t.TempDir()
	for _, backend := range []string{BackendCron, BackendLaunchd, BackendSystemd, BackendWindows} {
		t.Run(backend, func(t *testing.T) {
			platform := "linux"
			if backend == BackendLaunchd {
				platform = "darwin"
			}
			if backend == BackendWindows {
				platform = "windows"
			}
			manager := NewManager(WithEnvironmentPath("current-path"), WithPlatform(platform, 1000), WithLaunchAgentsDir(directory))
			recorded := "recorded-path"
			state, err := manager.InstallSpec(context.Background(), Spec{Name: "home", Action: ActionBackup, ConfigDir: directory, Executable: filepath.Join(directory, "resticctl"), Backend: backend, Expressions: []string{"0 2 * * *"}, EnvironmentPath: &recorded, DryRun: true})
			if err != nil {
				t.Fatal(err)
			}
			if state.EnvironmentPath != recorded || !strings.Contains(state.Rendered, recorded) || strings.Contains(state.Rendered, "current-path") {
				t.Fatalf("PATH=%q definition=%s", state.EnvironmentPath, state.Rendered)
			}
		})
	}
}

func TestLaunchdUpdateToNoStartUnloadsPreviousJob(t *testing.T) {
	directory := t.TempDir()
	executor := &fakeExecutor{}
	manager := NewManager(WithExecutor(executor), WithPlatform("darwin", 1000), WithLaunchAgentsDir(filepath.Join(directory, "agents")))
	spec := Spec{Name: "home", Action: ActionBackup, ConfigDir: directory, Executable: filepath.Join(directory, "resticctl"), Backend: BackendLaunchd, Expressions: []string{"0 2 * * *"}, Enabled: true, Start: true}
	if _, err := manager.InstallSpec(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	executor.executions = nil
	spec.Start = false
	state, err := manager.InstallSpec(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if len(executor.executions) != 1 || executor.executions[0].arguments[0] != "bootout" {
		t.Fatalf("old service not unloaded: %v", executor.executions)
	}
	if err := manager.Verify(context.Background(), state); err != nil {
		t.Fatal(err)
	}
}

func TestFailedCronMoveRestoresPreviousInstallation(t *testing.T) {
	directory := t.TempDir()
	manager := NewManager(WithExecutor(&fakeExecutor{}), WithPlatform("linux", 1000))
	spec := Spec{Name: "home", Action: ActionBackup, ConfigDir: directory, Executable: filepath.Join(directory, "resticctl"), Backend: BackendCron, Expressions: []string{"0 2 * * *"}, CronFile: filepath.Join(directory, "old.cron"), Enabled: true, Start: true}
	old, err := manager.InstallSpec(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	blocked := filepath.Join(directory, "blocked")
	if err := os.WriteFile(blocked, []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	spec.CronFile = filepath.Join(blocked, "new.cron")
	if _, err := manager.InstallSpec(context.Background(), spec); err == nil {
		t.Fatal("invalid destination accepted")
	}
	restored, err := Load(directory, "home")
	if err != nil {
		t.Fatal(err)
	}
	if restored.CronFile != old.CronFile {
		t.Fatalf("restored location=%s", restored.CronFile)
	}
	if err := manager.Verify(context.Background(), restored); err != nil {
		t.Fatal(err)
	}
}

func TestSystemdVerificationDetectsUnexpectedActivation(t *testing.T) {
	for _, test := range []struct{ enabled, active bool }{{true, false}, {false, true}, {false, false}} {
		t.Run("enabled="+strconv.FormatBool(test.enabled)+" active="+strconv.FormatBool(test.active), func(t *testing.T) {
			executor := &fakeExecutor{systemdEnabled: map[string]bool{"user:home.timer": test.enabled}, systemdActive: map[string]bool{"user:home.timer": test.active}}
			manager := NewManager(WithExecutor(executor))
			err := manager.verifyNative(context.Background(), State{JobFile: "home.timer", Permission: PermissionUser})
			if test.enabled || test.active {
				if !errors.Is(err, ErrDrift) {
					t.Fatalf("error=%v want drift", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSystemdVerificationDoesNotIgnoreCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager := NewManager(WithExecutor(executorFunc(func(context.Context, []byte, string, ...string) ([]byte, error) {
		cancel()
		return []byte("disabled\n"), context.Canceled
	})))
	if err := manager.verifyNative(ctx, State{JobFile: "home.timer"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v", err)
	}
}

func TestInvalidReplacementDoesNotRemoveInstalledSchedule(t *testing.T) {
	directory := t.TempDir()
	executor := &fakeExecutor{}
	manager := NewManager(WithExecutor(executor), WithPlatform("darwin", 1000), WithLaunchAgentsDir(filepath.Join(directory, "agents")))
	spec := Spec{Name: "home", Action: ActionBackup, ConfigDir: directory, Executable: filepath.Join(directory, "resticctl"), Backend: BackendCron, Expressions: []string{"0 2 * * *"}, Enabled: true, Start: true}
	old, err := manager.InstallSpec(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	executor.executions = nil
	spec.Backend = BackendLaunchd
	spec.Expressions = []string{"*/5 * * * *"}
	if _, err := manager.InstallSpec(context.Background(), spec); err == nil {
		t.Fatal("unsupported launchd calendar accepted")
	}
	if len(executor.executions) != 0 {
		t.Fatalf("invalid replacement touched the scheduler: %v", executor.executions)
	}
	if err := manager.Verify(context.Background(), old); err != nil {
		t.Fatal(err)
	}
}

func TestFailedRemovalDuringMoveRestoresInstalledSchedule(t *testing.T) {
	directory := t.TempDir()
	executor := &fakeExecutor{}
	manager := NewManager(WithExecutor(executor), WithPlatform("linux", 1000), WithSystemdDirs(filepath.Join(directory, "user"), filepath.Join(directory, "system")))
	spec := Spec{Name: "home", Action: ActionBackup, ConfigDir: directory, Executable: filepath.Join(directory, "resticctl"), Backend: BackendSystemd, Expressions: []string{"0 2 * * *"}, Enabled: true, Start: true}
	old, err := manager.InstallSpec(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	removeErr := errors.New("disable failed")
	failed := false
	manager.executor = executorFunc(func(ctx context.Context, input []byte, name string, arguments ...string) ([]byte, error) {
		output, err := executor.Run(ctx, input, name, arguments...)
		if name == "systemctl" && slices.Contains(arguments, "disable") && !failed {
			failed = true
			return []byte("permission denied"), removeErr
		}
		return output, err
	})
	spec.Permission = PermissionSystem
	if _, err := manager.InstallSpec(context.Background(), spec); !errors.Is(err, removeErr) {
		t.Fatalf("move error=%v, want removal error", err)
	}
	restored, err := Load(directory, spec.Name)
	if err != nil {
		t.Fatal(err)
	}
	if restored.JobFile != old.JobFile || restored.DefinitionHash != old.DefinitionHash {
		t.Fatalf("previous schedule state was changed: %+v", restored)
	}
	if err := manager.Verify(context.Background(), restored); err != nil {
		t.Fatalf("previous schedule was not restored after partial removal: %v", err)
	}
}
