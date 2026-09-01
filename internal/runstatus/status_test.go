package runstatus

import (
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
	info, err := os.Stat(filepath.Join(directory, "status", "example.json"))
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
