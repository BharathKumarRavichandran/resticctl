package schedule

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"resticctl/internal/configlock"
)

func TestConcurrentCronMutationCannotOverwriteJob(t *testing.T) {
	directory := t.TempDir()
	entered := make(chan struct{})
	resume := make(chan struct{})
	finished := make(chan error, 1)
	executor := &fakeExecutor{}
	executor.callback = func(call execution) {
		if call.name == "crontab" && slices.Equal(call.arguments, []string{"-l"}) {
			close(entered)
			<-resume
		}
	}
	manager := NewManager(WithExecutor(executor), WithPlatform("linux", 1000), WithClock(time.Now))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go func() {
		_, err := manager.Install(ctx, directory, "first", "0 1 * * *", BackendCron, "/bin/resticctl", false)
		finished <- err
	}()
	select {
	case <-entered:
	case err := <-finished:
		t.Fatalf("first install exited early: %v", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	_, err := manager.Install(ctx, directory, "second", "0 2 * * *", BackendCron, "/bin/resticctl", false)
	if !errors.Is(err, configlock.ErrLocked) {
		close(resume)
		<-finished
		t.Fatalf("concurrent install = %v, want locked", err)
	}
	err = manager.Remove(ctx, directory, "first")
	if !errors.Is(err, configlock.ErrLocked) {
		close(resume)
		<-finished
		t.Fatalf("concurrent removal = %v, want locked", err)
	}
	executor.callback = nil
	close(resume)
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Install(ctx, directory, "second", "0 2 * * *", BackendCron, "/bin/resticctl", false); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"first", "second"} {
		state, err := Load(directory, name)
		if err != nil {
			t.Fatal(err)
		}
		if err := manager.Verify(ctx, state); err != nil {
			t.Fatal(err)
		}
	}
}

func TestExplicitCronFileSharesLockAcrossConfigurations(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "crontab")
	manager := NewManager(WithExecutor(&fakeExecutor{}), WithPlatform("linux", 1000), WithClock(time.Now))
	err := configlock.With(path+".resticctl.lock", func() error {
		_, err := manager.InstallSpec(context.Background(), Spec{Name: "example", Action: ActionBackup, Backend: BackendCron, Executable: "/bin/resticctl", ConfigDir: t.TempDir(), Expressions: []string{"0 1 * * *"}, CronFile: path, Enabled: true, Start: true})
		if !errors.Is(err, configlock.ErrLocked) {
			t.Fatalf("Install = %v, want locked", err)
		}
		if err := removeCronFile(path, "example", ActionBackup); !errors.Is(err, configlock.ErrLocked) {
			t.Fatalf("remove = %v, want locked", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
