package app

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"resticctl/internal/profile"
	"resticctl/internal/restic"
	"resticctl/internal/runstatus"
)

type statusSabotageRunner struct {
	recordingRunner
	statusDirectory string
}

func (runner *statusSabotageRunner) Run(context.Context, restic.Config, []string, string) error {
	if err := os.Remove(runner.statusDirectory); err != nil {
		return err
	}
	return os.Mkdir(runner.statusDirectory, 0o700)
}

type capturedMonitoringEvent struct {
	phase  string
	status runstatus.Status
}

type capturingReporter struct {
	events *[]capturedMonitoringEvent
}

func (reporter capturingReporter) Report(_ context.Context, phase string, status runstatus.Status) error {
	*reporter.events = append(*reporter.events, capturedMonitoringEvent{phase: phase, status: status})
	return nil
}

type warningRunner struct{ recordingRunner }

func (runner *warningRunner) Run(context.Context, restic.Config, []string, string) error {
	return &restic.ExitError{Code: 3}
}

func TestInvokeResticWarningPolicy(t *testing.T) {
	for _, test := range []struct {
		policy                      string
		wantError, wantWarningState bool
	}{{"failure", true, false}, {"warning", false, true}, {"success", false, false}} {
		t.Run(test.policy, func(t *testing.T) {
			ctx, observation := observe(context.Background())
			err := invokeRestic(ctx, &warningRunner{}, profile.Profile{Monitoring: profile.Monitoring{WarningPolicy: test.policy}}, []string{"check"}, "")
			if (err != nil) != test.wantError {
				t.Fatalf("error = %v", err)
			}
			outcome := observation.outcome(err, 1)
			if !outcome.Warning || outcome.WarningState != test.wantWarningState || outcome.ExitCode == nil || *outcome.ExitCode != 3 {
				t.Fatalf("outcome = %#v", outcome)
			}
		})
	}
}

func TestInsertOptionBeforePathSeparator(t *testing.T) {
	arguments := insertOption([]string{"backup", "--", "files"}, "--json")
	if got := strings.Join(arguments, " "); got != "backup --json -- files" {
		t.Fatalf("arguments = %s", got)
	}
}

func TestMonitoringFailureDoesNotMaskSuccessfulAction(t *testing.T) {
	directory := t.TempDir()
	blockedParent := filepath.Join(directory, "not-a-directory")
	if err := os.WriteFile(blockedParent, []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	backupProfile := profile.Profile{Name: "example", Monitoring: profile.Monitoring{HistoryLimit: 1, WarningPolicy: "failure", StatusFile: filepath.Join(blockedParent, "status.json")}}
	err := RunCheck(context.Background(), func() (Runner, error) { return &recordingRunner{}, nil }, directory, backupProfile, time.Now)
	if err != nil {
		t.Fatalf("RunCheck error = %v", err)
	}
}

func TestStatusFinalizationFailureReportsControllerFailure(t *testing.T) {
	var events []capturedMonitoringEvent
	original := newMonitoringReporter
	newMonitoringReporter = func(profile.Profile, io.Writer) monitoringReporter {
		return capturingReporter{events: &events}
	}
	t.Cleanup(func() { newMonitoringReporter = original })
	directory := t.TempDir()
	runner := &statusSabotageRunner{statusDirectory: filepath.Join(directory, "status", "example.check.json")}
	backupProfile := profile.Profile{Name: "example", Monitoring: profile.Monitoring{HistoryLimit: 1}}
	err := RunCheck(context.Background(), func() (Runner, error) { return runner, nil }, directory, backupProfile, time.Now)
	if err == nil {
		t.Fatal("RunCheck succeeded despite status finalization failure")
	}
	if len(events) != 3 || events[0].phase != "send-before" || events[1].phase != "send-after-fail" || events[2].phase != "send-finally" {
		t.Fatalf("monitoring phases = %#v", events)
	}
	for _, event := range events[1:] {
		if event.status.State != "failed" || event.status.ErrorCategory != "controller" {
			t.Fatalf("monitoring event = %#v", event)
		}
	}
}
