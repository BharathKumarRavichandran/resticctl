package app

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"resticctl/internal/profile"
	"resticctl/internal/restic"
	"resticctl/internal/runstatus"
	"resticctl/internal/schedule"
)

func TestConfiguredBackupDryRunIsNotRecorded(t *testing.T) {
	for _, test := range []struct {
		name    string
		args    []string
		command bool
	}{
		{"long", []string{"--dry-run"}, false},
		{"short", []string{"-n"}, false},
		{"explicit true", []string{"--dry-run=true"}, false},
		{"command arguments", []string{"-n=true"}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			source := t.TempDir()
			backupProfile := profile.Profile{Name: "example", BackupPaths: []string{source}}
			if test.command {
				backupProfile.Commands = map[string]profile.ResticCommand{"backup": {Args: test.args}}
			} else {
				backupProfile.BackupArgs = test.args
			}
			runner := &recordingRunner{}
			err := RunBackup(context.Background(), func() (Runner, error) { return runner, nil }, directory, backupProfile, false, io.Discard, time.Now)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := runstatus.LoadAction(directory, "example", schedule.ActionBackup); !errors.Is(err, runstatus.ErrNotRecorded) {
				t.Fatalf("dry run status was recorded: %v", err)
			}
			if len(runner.runs) != 1 || !hasDryRunOption(runner.runs[0].arguments) {
				t.Fatalf("Restic arguments = %#v", runner.runs)
			}
		})
	}
}

func TestRawDryRunSpellingsAreNotRecorded(t *testing.T) {
	for _, argument := range []string{"--dry-run", "-n", "--dry-run=true", "-n=true", "--dry-run=1", "-n=t"} {
		t.Run(argument, func(t *testing.T) {
			directory := t.TempDir()
			runner := &recordingRunner{}
			err := RunRecordedRestic(context.Background(), func() (Runner, error) { return runner, nil }, directory, profile.Profile{Name: "example"}, "forget", []string{argument}, time.Now, io.Discard)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := runstatus.LoadAction(directory, "example", schedule.ActionForget); !errors.Is(err, runstatus.ErrNotRecorded) {
				t.Fatalf("dry run status was recorded: %v", err)
			}
		})
	}
}

func TestConfiguredDryRunForOtherActionsIsNotRecorded(t *testing.T) {
	tests := []struct {
		name, command string
		profile       profile.Profile
		run           func(context.Context, RunnerFactory, string, profile.Profile) error
	}{
		{
			name: "forget arguments", command: schedule.ActionForget,
			profile: profile.Profile{Name: "example", ForgetArgs: []string{"-n"}},
			run: func(ctx context.Context, factory RunnerFactory, directory string, configured profile.Profile) error {
				return RunForget(ctx, factory, directory, configured, false, false, time.Now)
			},
		},
		{
			name: "configured raw command", command: schedule.ActionCopy,
			profile: profile.Profile{Name: "example", Commands: map[string]profile.ResticCommand{
				"copy": {Args: []string{"--dry-run=true"}},
			}},
			run: func(ctx context.Context, factory RunnerFactory, directory string, configured profile.Profile) error {
				return RunRecordedRestic(ctx, factory, directory, configured, schedule.ActionCopy, nil, time.Now, io.Discard)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			runner := &recordingRunner{}
			factory := func() (Runner, error) { return runner, nil }
			if err := test.run(context.Background(), factory, directory, test.profile); err != nil {
				t.Fatal(err)
			}
			if _, err := runstatus.LoadAction(directory, "example", test.command); !errors.Is(err, runstatus.ErrNotRecorded) {
				t.Fatalf("configured dry run status was recorded: %v", err)
			}
		})
	}
}

type concurrentScheduleExecutor struct {
	mu      sync.Mutex
	crontab []byte
}

func (executor *concurrentScheduleExecutor) Run(_ context.Context, input []byte, name string, arguments ...string) ([]byte, error) {
	executor.mu.Lock()
	defer executor.mu.Unlock()
	if name == "crontab" && slices.Equal(arguments, []string{"-l"}) {
		return append([]byte(nil), executor.crontab...), nil
	}
	if name == "crontab" && slices.Equal(arguments, []string{"-"}) {
		executor.crontab = append([]byte(nil), input...)
	}
	return nil, nil
}

type blockingBackupRunner struct {
	mu      sync.Mutex
	runs    int
	started chan struct{}
	release chan struct{}
}

func (runner *blockingBackupRunner) Run(_ context.Context, _ restic.Config, _ []string, _ string) error {
	runner.mu.Lock()
	runner.runs++
	current := runner.runs
	runner.mu.Unlock()
	if current == 1 {
		close(runner.started)
		<-runner.release
	}
	return nil
}

func (runner *blockingBackupRunner) RunHook(context.Context, []string) error { return nil }
func (runner *blockingBackupRunner) RunDatabase(context.Context, []string, map[string]string, string) error {
	return nil
}

func TestScheduledCatchUpRechecksDueStateAfterWaitingForLock(t *testing.T) {
	directory := t.TempDir()
	source := t.TempDir()
	installedAt := time.Date(2026, 9, 4, 8, 0, 0, 0, time.UTC)
	runAt := installedAt.Add(2 * time.Minute)
	executor := &concurrentScheduleExecutor{}
	manager := schedule.NewManager(
		schedule.WithExecutor(executor), schedule.WithPlatform("linux", 1000),
		schedule.WithClock(func() time.Time { return installedAt }),
	)
	_, err := manager.InstallSpec(context.Background(), schedule.Spec{
		Name: "example", Action: schedule.ActionBackup, Backend: schedule.BackendCron,
		Executable: filepath.Join(directory, "resticctl"), ConfigDir: directory,
		Expressions: []string{"* * * * *"}, CatchUp: true,
		LockMode: schedule.LockWait, LockWait: "2s", Permission: schedule.PermissionUser,
		Enabled: true, Start: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	runner := &blockingBackupRunner{started: make(chan struct{}), release: make(chan struct{})}
	factory := func() (Runner, error) { return runner, nil }
	backupProfile := profile.Profile{Name: "example", BackupPaths: []string{source}}
	results := make(chan struct {
		due bool
		err error
	}, 2)
	for range 2 {
		go func() {
			due, runErr := ScheduledRun(context.Background(), factory, manager, directory, backupProfile, schedule.ActionBackup, func() time.Time { return runAt }, io.Discard)
			results <- struct {
				due bool
				err error
			}{due, runErr}
		}()
	}
	select {
	case <-runner.started:
	case <-time.After(time.Second):
		t.Fatal("first backup did not start")
	}
	close(runner.release)
	dueCount := 0
	for range 2 {
		result := <-results
		if result.err != nil {
			t.Fatal(result.err)
		}
		if result.due {
			dueCount++
		}
	}
	if runner.runs != 1 || dueCount != 1 {
		t.Fatalf("backup runs=%d due results=%d, want 1 each", runner.runs, dueCount)
	}
	status, err := runstatus.LoadAction(directory, "example", schedule.ActionBackup)
	if err != nil {
		t.Fatal(err)
	}
	if !status.StartedAt.Equal(runAt) || status.DurationMS != 0 {
		t.Fatalf("recorded timing = %#v", status)
	}
}

func TestRawDryRunOverridesAreRecordedAccordingToEffectiveArguments(t *testing.T) {
	for _, test := range []struct {
		name                  string
		configured, arguments []string
		recorded              bool
	}{
		{"command override", []string{"--dry-run"}, []string{"--dry-run=false"}, true},
		{"raw override", nil, []string{"--dry-run", "-n=false"}, true},
		{"enable raw", []string{"--dry-run=false"}, []string{"-n"}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			backupProfile := profile.Profile{Name: "example", Commands: map[string]profile.ResticCommand{"forget": {Args: test.configured}}}
			factory := func() (Runner, error) { return &recordingRunner{}, nil }
			if err := RunRecordedRestic(context.Background(), factory, directory, backupProfile, "forget", test.arguments, time.Now, io.Discard); err != nil {
				t.Fatal(err)
			}
			_, err := runstatus.LoadAction(directory, "example", "forget")
			if test.recorded && err != nil {
				t.Fatal(err)
			}
			if !test.recorded && !errors.Is(err, runstatus.ErrNotRecorded) {
				t.Fatalf("dry run was recorded: %v", err)
			}
		})
	}
}

func TestGlobalDryRunIsNotRecorded(t *testing.T) {
	directory := t.TempDir()
	backupProfile := profile.Profile{Name: "example", ResticArgs: []string{"--dry-run"}, BackupPaths: []string{t.TempDir()}}
	factory := func() (Runner, error) { return &recordingRunner{}, nil }
	if err := RunBackup(context.Background(), factory, directory, backupProfile, false, io.Discard, time.Now); err != nil {
		t.Fatal(err)
	}
	if _, err := runstatus.Load(directory, "example"); !errors.Is(err, runstatus.ErrNotRecorded) {
		t.Fatalf("dry run was recorded: %v", err)
	}
}

type remotePolicyRecorder struct{ remote bool }

func (policy *remotePolicyRecorder) Run(ctx context.Context, _ profile.Runtime, _, remote bool, run func(context.Context) error) error {
	policy.remote = remote
	return run(ctx)
}

func TestScheduledCopyChecksRemoteDestinations(t *testing.T) {
	directory := t.TempDir()
	executor := &concurrentScheduleExecutor{}
	manager := schedule.NewManager(schedule.WithExecutor(executor), schedule.WithPlatform("linux", 1000))
	if _, err := manager.InstallAction(context.Background(), directory, "example", "copy", "0 0 * * *", "cron", filepath.Join(directory, "resticctl"), false, false); err != nil {
		t.Fatal(err)
	}
	policy := &remotePolicyRecorder{}
	original := newPolicyRunner
	newPolicyRunner = func() policyRunner { return policy }
	t.Cleanup(func() { newPolicyRunner = original })
	backupProfile := profile.Profile{Name: "example", Repository: "/local/source", Copies: map[string]profile.CopyTarget{"remote": {Repository: "s3:bucket"}}}
	factory := func() (Runner, error) { return &recordingRunner{}, nil }
	if _, err := ScheduledRun(context.Background(), factory, manager, directory, backupProfile, "copy", time.Now, io.Discard); err != nil {
		t.Fatal(err)
	}
	if !policy.remote {
		t.Fatal("remote copy destination bypassed network policy")
	}
}

type scheduledPolicyRecorder struct{ scheduled bool }

func (policy *scheduledPolicyRecorder) Run(ctx context.Context, _ profile.Runtime, scheduled, _ bool, run func(context.Context) error) error {
	policy.scheduled = scheduled
	if scheduled {
		return errors.New("scheduled job requires AC power")
	}
	return run(ctx)
}

func TestMemberWorkflowsPreserveScheduledPolicy(t *testing.T) {
	policy := &scheduledPolicyRecorder{}
	original := newPolicyRunner
	newPolicyRunner = func() policyRunner { return policy }
	t.Cleanup(func() { newPolicyRunner = original })
	p := profile.Profile{Name: "example", Runtime: profile.Runtime{RequireACPower: true}, BackupPaths: []string{t.TempDir()}, ForgetArgs: []string{"--keep-last", "1"}, Copies: map[string]profile.CopyTarget{"target": {Repository: "local:target"}}}
	for _, action := range []string{"backup", "forget", "check", "prune", "copy"} {
		t.Run(action, func(t *testing.T) {
			directory := t.TempDir()
			runner := &recordingRunner{}
			factory := func() (Runner, error) { return runner, nil }
			ctx := WithScheduledExecution(context.Background())
			var err error
			switch action {
			case "backup":
				err = RunBackup(ctx, factory, directory, p, false, io.Discard, time.Now)
			case "forget":
				err = RunForget(ctx, factory, directory, p, false, false, time.Now)
			case "check":
				err = RunCheck(ctx, factory, directory, p, time.Now)
			case "prune":
				err = RunRecordedRestic(ctx, factory, directory, p, action, nil, time.Now, io.Discard)
			case "copy":
				err = RunCopy(ctx, factory, directory, p, "target", false, io.Discard, time.Now)
			}
			if err == nil || !policy.scheduled {
				t.Fatalf("scheduled policy ignored: %v", err)
			}
			if len(runner.runs) != 0 {
				t.Fatal("blocked scheduled workflow invoked Restic")
			}
		})
	}
}
