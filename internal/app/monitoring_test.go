package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"resticctl/internal/profile"
	"resticctl/internal/restic"
)

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
