package runstatus

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestRecorderPersistsSuccessfulRun(t *testing.T) {
	directory := t.TempDir()
	started := time.Date(2026, 8, 30, 1, 2, 3, 0, time.FixedZone("test", 3600))
	recorder, err := Begin(directory, "example", started)
	if err != nil {
		t.Fatal(err)
	}
	running, err := Load(directory, "example")
	if err != nil {
		t.Fatal(err)
	}
	if running.State != "running" || running.FinishedAt != nil {
		t.Fatalf("running status = %#v", running)
	}
	if err := recorder.Finish(nil, started.Add(1500*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	finished, err := Load(directory, "example")
	if err != nil {
		t.Fatal(err)
	}
	if finished.State != "succeeded" || finished.DurationMS != 1500 || finished.FinishedAt == nil {
		t.Fatalf("finished status = %#v", finished)
	}
	info, err := os.Stat(filepath.Join(directory, "status", statusKey("example", "backup")+".json"))
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("status mode = %o, want 600", info.Mode().Perm())
	}
}

func TestRecorderRejectsOverlappingBackup(t *testing.T) {
	directory := t.TempDir()
	first, err := Begin(directory, "example", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Begin(directory, "example", time.Now()); err == nil {
		t.Fatal("overlapping backup was accepted")
	}
	if err := first.Finish(errors.New("backup failed"), time.Now()); err != nil {
		t.Fatal(err)
	}
	status, err := Load(directory, "example")
	if err != nil {
		t.Fatal(err)
	}
	if status.State != "failed" {
		t.Fatalf("state = %q, want failed", status.State)
	}
}

func TestCopyTargetsUseProfileLockAndIndependentStatus(t *testing.T) {
	directory := t.TempDir()
	now := time.Now()
	offsite, err := BeginCopyTarget(directory, "home", "offsite", now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := BeginCopyTarget(directory, "home", "archive", now); !errors.Is(err, ErrLocked) {
		t.Fatalf("overlapping target error = %v", err)
	}
	if err := offsite.Finish(nil, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	archive, err := BeginCopyTarget(directory, "home", "archive", now)
	if err != nil {
		t.Fatal(err)
	}
	if err := archive.Finish(nil, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	status, err := LoadCopyTarget(directory, "home", "offsite")
	if err != nil {
		t.Fatal(err)
	}
	if status.Profile != "home" || status.TargetType != "copy" || status.TargetName != "offsite" {
		t.Fatalf("status = %#v", status)
	}
}

func TestWithProfileLockExcludesRecordedActions(t *testing.T) {
	directory := t.TempDir()
	err := WithProfileLock(context.Background(), directory, "home", func() error {
		if _, err := BeginAction(directory, "home", "backup", time.Now()); !errors.Is(err, ErrLocked) {
			t.Fatalf("concurrent action error = %v, want ErrLocked", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	recorder, err := BeginAction(directory, "home", "backup", time.Now())
	if err != nil {
		t.Fatalf("profile lock was not released: %v", err)
	}
	if err := recorder.Finish(nil, time.Now()); err != nil {
		t.Fatal(err)
	}
}

func TestRecorderRejectsOverlappingActions(t *testing.T) {
	directory := t.TempDir()
	backup, err := BeginAction(directory, "example", "backup", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := BeginAction(directory, "example", "forget", time.Now()); err == nil {
		t.Fatal("overlapping forget was accepted")
	}
	if err := backup.Finish(nil, time.Now()); err != nil {
		t.Fatal(err)
	}
}

func TestRecorderStoresEachSupportedActionIndependently(t *testing.T) {
	directory := t.TempDir()
	actions := []string{"backup", "check", "forget", "prune", "copy"}
	for index, action := range actions {
		started := time.Unix(int64(index), 0)
		recorder, err := BeginAction(directory, "example", action, started)
		if err != nil {
			t.Fatal(err)
		}
		if err := recorder.Finish(nil, started.Add(time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	for _, action := range actions {
		status, err := LoadAction(directory, "example", action)
		if err != nil {
			t.Fatal(err)
		}
		if status.Action != action || status.Command != action || status.State != "succeeded" {
			t.Fatalf("%s status = %#v", action, status)
		}
	}
}

func TestLoadReportsMissingStatus(t *testing.T) {
	_, err := Load(t.TempDir(), "example")
	if !errors.Is(err, ErrNotRecorded) {
		t.Fatalf("error = %v, want ErrNotRecorded", err)
	}
}

func TestRecorderPreservesLastSuccessAcrossFailure(t *testing.T) {
	directory := t.TempDir()
	firstStart := time.Date(2026, 8, 30, 1, 0, 0, 0, time.UTC)
	first, err := Begin(directory, "example", firstStart)
	if err != nil {
		t.Fatal(err)
	}
	firstFinish := firstStart.Add(time.Minute)
	if err := first.Finish(nil, firstFinish); err != nil {
		t.Fatal(err)
	}
	second, err := Begin(directory, "example", firstFinish.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err := second.Finish(errors.New("failed"), firstFinish.Add(time.Hour+time.Minute)); err != nil {
		t.Fatal(err)
	}
	status, err := Load(directory, "example")
	if err != nil {
		t.Fatal(err)
	}
	if status.State != "failed" || status.LastSuccessAt == nil || !status.LastSuccessAt.Equal(firstFinish) {
		t.Fatalf("status = %#v", status)
	}
}

type testExitError struct{ code int }

func (err testExitError) Error() string { return "failed" }
func (err testExitError) ExitCode() int { return err.code }

func TestRecorderCancellationPreservesCategoryAndExitCode(t *testing.T) {
	for _, test := range []struct {
		name     string
		err      error
		category string
	}{
		{"cancelled", context.Canceled, "cancelled"},
		{"timeout", context.DeadlineExceeded, "timeout"},
	} {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			started := time.Unix(100, 0)
			recorder, err := Begin(directory, "example", started)
			if err != nil {
				t.Fatal(err)
			}
			if err := recorder.Finish(errors.Join(test.err, testExitError{code: 7}), started.Add(time.Second)); err != nil {
				t.Fatal(err)
			}
			status, err := Load(directory, "example")
			if err != nil {
				t.Fatal(err)
			}
			if status.State != "cancelled" || status.ErrorCategory != test.category || status.ExitCode == nil || *status.ExitCode != 7 {
				t.Fatalf("status = %#v", status)
			}
		})
	}
}

func TestRecorderPersistsBoundedHistoryAndStructuredOutcome(t *testing.T) {
	directory := t.TempDir()
	for index := range 3 {
		started := time.Unix(int64(index), 0)
		recorder, err := BeginAction(directory, "example", "check", started)
		if err != nil {
			t.Fatal(err)
		}
		if err := recorder.FinishOutcome(Outcome{Err: testExitError{code: 7}, HistoryLimit: 2}, started.Add(time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	history, err := LoadHistory(directory, "example", "check")
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 2 || history[0].ExitCode == nil || *history[0].ExitCode != 7 || history[0].ErrorCategory != "command_exit" || history[0].Command != "check" {
		t.Fatalf("history = %#v", history)
	}
}

func TestLoadHistoryRejectsInvalidRecords(t *testing.T) {
	finished := time.Now().UTC()
	valid := Status{
		Profile: "example", Action: "check", Command: "check", State: "succeeded",
		StartedAt: finished.Add(-time.Second), FinishedAt: &finished,
	}
	for _, test := range []struct {
		name   string
		mutate func(*Status)
	}{
		{name: "wrong profile", mutate: func(status *Status) { status.Profile = "other" }},
		{name: "wrong action", mutate: func(status *Status) { status.Action = "backup" }},
		{name: "running", mutate: func(status *Status) { status.State, status.FinishedAt = "running", nil }},
		{name: "missing finish", mutate: func(status *Status) { status.FinishedAt = nil }},
		{name: "finish before start", mutate: func(status *Status) {
			finished := status.StartedAt.Add(-time.Second)
			status.FinishedAt = &finished
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			status := valid
			test.mutate(&status)
			data, err := json.Marshal([]Status{status})
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(directory, "status", "profiles", "example", "history", "check.json")
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadHistory(directory, "example", "check"); err == nil {
				t.Fatal("LoadHistory succeeded")
			}
		})
	}
}

func TestDottedProfileStatusAndHistoryRemainIndependent(t *testing.T) {
	directory := t.TempDir()
	now := time.Now()
	recorder, err := Begin(directory, "photos.check", now)
	if err != nil {
		t.Fatal(err)
	}
	if err := recorder.Finish(nil, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	check, err := BeginAction(directory, "photos", "check", now.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err := check.Finish(nil, now.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	recorder, err = Begin(directory, "photos.check", now.Add(4*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if recorder.Status().LastSuccessAt == nil || !recorder.Status().LastSuccessAt.Equal(now.Add(time.Second)) {
		t.Fatal("last success lost")
	}
	if err := recorder.Finish(nil, now.Add(5*time.Second)); err != nil {
		t.Fatal(err)
	}
	for _, target := range []struct {
		name, action string
		history      int
	}{{"photos.check", "backup", 2}, {"photos", "check", 1}} {
		status, err := LoadAction(directory, target.name, target.action)
		if err != nil || status.Profile != target.name || status.Action != target.action {
			t.Fatalf("status=%+v, err=%v", status, err)
		}
		history, err := LoadHistory(directory, target.name, target.action)
		if err != nil || len(history) != target.history {
			t.Fatalf("history=%+v, err=%v", history, err)
		}
	}
}

func TestCancelledProfileLockDoesNotRunOrCreateState(t *testing.T) {
	directory := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	err := WithProfileLock(ctx, directory, "example", func() error { called = true; return nil })
	if !errors.Is(err, context.Canceled) || called {
		t.Fatalf("called=%t, err=%v", called, err)
	}
	if _, err := os.Stat(filepath.Join(directory, "status")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cancelled lock created state: %v", err)
	}
}

func TestGroupAndProfileStateUseSeparateDirectories(t *testing.T) {
	directory := t.TempDir()
	now := time.Now()
	recorder, err := Begin(directory, "daily", now)
	if err != nil {
		t.Fatal(err)
	}
	group, err := BeginGroupAction(directory, "daily", "backup", now)
	if err != nil {
		t.Fatal(err)
	}
	if err := recorder.Finish(nil, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := group.Finish(nil, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"profiles", "groups"} {
		for _, relative := range []string{"backup.json", filepath.Join("history", "backup.json"), "run.lock"} {
			if _, err := os.Stat(filepath.Join(directory, "status", target, "daily", relative)); err != nil {
				t.Fatal(err)
			}
		}
	}
	history, err := LoadHistory(directory, "group+daily", "backup")
	if err != nil || len(history) != 1 || history[0].TargetType != "group" {
		t.Fatalf("history=%+v, err=%v", history, err)
	}
}

func TestFlatStatusFilesAreNotLoaded(t *testing.T) {
	directory := t.TempDir()
	root := filepath.Join(directory, "status")
	if err := os.MkdirAll(filepath.Join(root, "history"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{"example.json", "v2+example+backup.json"} {
		for _, subdir := range []string{"", "history"} {
			if err := os.WriteFile(filepath.Join(root, subdir, file), []byte(`invalid JSON`), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := Load(directory, "example"); !errors.Is(err, ErrNotRecorded) {
		t.Fatalf("Load: %v", err)
	}
	if _, err := LoadHistory(directory, "example", "backup"); !errors.Is(err, ErrNotRecorded) {
		t.Fatalf("LoadHistory: %v", err)
	}
}

func TestStatusRejectsTargetIdentityMismatch(t *testing.T) {
	directory := t.TempDir()
	now := time.Now()
	for _, group := range []bool{false, true} {
		var recorder *Recorder
		var err error
		name := "home"
		if group {
			name = "group+home"
			recorder, err = BeginGroupAction(directory, "home", "backup", now)
		} else {
			recorder, err = Begin(directory, "home", now)
		}
		if err != nil {
			t.Fatal(err)
		}
		if err := recorder.Finish(nil, now.Add(time.Second)); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(directory, "status", statusKey(name, "backup")+".json")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var status Status
		if err := json.Unmarshal(data, &status); err != nil {
			t.Fatal(err)
		}
		status.TargetName = "other"
		if err := write(directory, path, status); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadAction(directory, name, "backup"); err == nil {
			t.Fatal("accepted mismatched target")
		}
	}
}
