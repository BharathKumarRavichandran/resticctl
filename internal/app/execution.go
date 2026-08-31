package app

import (
	"context"
	"errors"
	"io"
	"time"

	"resticctl/internal/cronexpr"
	"resticctl/internal/profile"
	"resticctl/internal/runstatus"
	"resticctl/internal/schedule"
)

// RunnerFactory creates the subprocess runner used by an application workflow.
type RunnerFactory func() (Runner, error)

// RunBackup executes a backup and records non-dry-run status.
func RunBackup(ctx context.Context, newRunner RunnerFactory, configDir string, backupProfile profile.Profile, dryRun bool, output io.Writer, now func() time.Time) error {
	runner, err := newRunner()
	if err != nil {
		return err
	}
	if dryRun {
		return Backup(ctx, runner, backupProfile, true, output)
	}
	return recordRun(configDir, backupProfile.Name, schedule.ActionBackup, now, func() error {
		return Backup(ctx, runner, backupProfile, false, output)
	})
}

// RunForget applies retention and records non-dry-run status.
func RunForget(ctx context.Context, newRunner RunnerFactory, configDir string, backupProfile profile.Profile, dryRun, prune bool, now func() time.Time) error {
	runner, err := newRunner()
	if err != nil {
		return err
	}
	if dryRun {
		return Forget(ctx, runner, backupProfile, true, prune)
	}
	return recordRun(configDir, backupProfile.Name, schedule.ActionForget, now, func() error {
		return Forget(ctx, runner, backupProfile, false, prune)
	})
}

// ScheduledRun verifies and runs an overdue scheduled action.
func ScheduledRun(ctx context.Context, newRunner RunnerFactory, manager schedule.Manager, configDir string, backupProfile profile.Profile, action string, now func() time.Time, output io.Writer) (bool, error) {
	state, err := schedule.LoadAction(configDir, backupProfile.Name, action)
	if err != nil {
		return false, err
	}
	if err := manager.Verify(ctx, state); err != nil {
		return false, err
	}
	lastSuccess := &state.Installed
	status, statusErr := runstatus.LoadAction(configDir, backupProfile.Name, action)
	if statusErr == nil {
		if status.LastSuccessAt != nil {
			lastSuccess = status.LastSuccessAt
		} else if status.State == "succeeded" && status.FinishedAt != nil {
			lastSuccess = status.FinishedAt
		}
	} else if !errors.Is(statusErr, runstatus.ErrNotRecorded) {
		return false, statusErr
	}
	due, err := cronexpr.Due(state.Expression, lastSuccess, now())
	if err != nil || !due {
		return due, err
	}
	if action == schedule.ActionForget {
		return true, RunForget(ctx, newRunner, configDir, backupProfile, false, state.Prune, now)
	}
	return true, RunBackup(ctx, newRunner, configDir, backupProfile, false, output, now)
}

func recordRun(configDir, name, action string, now func() time.Time, run func() error) error {
	recorder, err := runstatus.BeginAction(configDir, name, action, now())
	if err != nil {
		return err
	}
	runErr := run()
	return errors.Join(runErr, recorder.Finish(runErr, now()))
}
