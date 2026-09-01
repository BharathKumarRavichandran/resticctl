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
	due := true
	if state.CatchUp {
		due, err = cronexpr.Due(state.Expression, lastSuccess, now())
		for _, expression := range state.Expressions[1:] {
			otherDue, otherErr := cronexpr.Due(expression, lastSuccess, now())
			if otherErr != nil {
				return false, otherErr
			}
			due = due || otherDue
		}
	}
	if err != nil || !due {
		return due, err
	}
	runner, err := newRunner()
	if err != nil {
		return true, err
	}
	run := func() error {
		switch action {
		case schedule.ActionBackup:
			return Backup(ctx, runner, backupProfile, false, output)
		case schedule.ActionForget:
			return Forget(ctx, runner, backupProfile, false, state.Prune)
		case schedule.ActionCheck:
			return Check(ctx, runner, backupProfile)
		case schedule.ActionPrune:
			return RunRestic(ctx, runner, backupProfile, "prune", nil)
		case schedule.ActionCopy:
			return RunRestic(ctx, runner, backupProfile, "copy", nil)
		default:
			return scheduleActionError(action)
		}
	}
	return true, recordScheduledRun(ctx, configDir, backupProfile.Name, action, state, now, run)
}

func recordScheduledRun(ctx context.Context, configDir, name, action string, state schedule.State, now func() time.Time, run func() error) error {
	if state.LockMode != schedule.LockWait {
		return recordRun(configDir, name, action, now, run)
	}
	wait, err := time.ParseDuration(state.LockWait)
	if err != nil {
		return err
	}
	recorder, err := runstatus.BeginActionWait(ctx, configDir, name, action, now(), wait)
	if err != nil {
		return err
	}
	runErr := run()
	return errors.Join(runErr, recorder.Finish(runErr, now()))
}

func scheduleActionError(action string) error {
	return errors.New("unsupported scheduled action " + action)
}

func recordRun(configDir, name, action string, now func() time.Time, run func() error) error {
	recorder, err := runstatus.BeginAction(configDir, name, action, now())
	if err != nil {
		return err
	}
	runErr := run()
	return errors.Join(runErr, recorder.Finish(runErr, now()))
}
