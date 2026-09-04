package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"resticctl/internal/cronexpr"
	"resticctl/internal/monitoring"
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
	if dryRun || configuredDryRun(backupProfile, schedule.ActionBackup) {
		return Backup(ctx, runner, backupProfile, true, output)
	}
	return recordRun(ctx, configDir, backupProfile, schedule.ActionBackup, now, output, func(runCtx context.Context) error {
		return Backup(runCtx, runner, backupProfile, false, output)
	})
}

// RunForget applies retention and records non-dry-run status.
func RunForget(ctx context.Context, newRunner RunnerFactory, configDir string, backupProfile profile.Profile, dryRun, prune bool, now func() time.Time) error {
	runner, err := newRunner()
	if err != nil {
		return err
	}
	if dryRun || configuredDryRun(backupProfile, schedule.ActionForget) {
		return Forget(ctx, runner, backupProfile, true, prune)
	}
	return recordRun(ctx, configDir, backupProfile, schedule.ActionForget, now, nil, func(runCtx context.Context) error {
		return Forget(runCtx, runner, backupProfile, false, prune)
	})
}

// RunCheck checks a repository and records the action independently.
func RunCheck(ctx context.Context, newRunner RunnerFactory, configDir string, backupProfile profile.Profile, now func() time.Time) error {
	runner, err := newRunner()
	if err != nil {
		return err
	}
	return recordRun(ctx, configDir, backupProfile, schedule.ActionCheck, now, nil, func(runCtx context.Context) error {
		return Check(runCtx, runner, backupProfile)
	})
}

// RunRecordedRestic executes a raw monitored action while preserving the same
// status, warning, and notification semantics as first-class commands.
func RunRecordedRestic(ctx context.Context, newRunner RunnerFactory, configDir string, backupProfile profile.Profile, command string, arguments []string, now func() time.Time, output io.Writer) error {
	runner, err := newRunner()
	if err != nil {
		return err
	}
	if hasDryRunOption(arguments) || configuredDryRun(backupProfile, command) {
		return RunRestic(ctx, runner, backupProfile, command, arguments)
	}
	return recordRun(ctx, configDir, backupProfile, command, now, output, func(runCtx context.Context) error {
		return RunRestic(runCtx, runner, backupProfile, command, arguments)
	})
}

func configuredDryRun(backupProfile profile.Profile, command string) bool {
	var workflowArguments []string
	switch command {
	case schedule.ActionBackup:
		workflowArguments = backupProfile.BackupArgs
	case schedule.ActionForget:
		workflowArguments = backupProfile.ForgetArgs
	}
	for _, argument := range workflowArguments {
		if profile.IsDryRunOption(argument) {
			return true
		}
	}
	for _, argument := range backupProfile.Commands[command].Args {
		if profile.IsDryRunOption(argument) {
			return true
		}
	}
	return false
}

func hasDryRunOption(arguments []string) bool {
	for _, argument := range arguments {
		if argument == "--" {
			return false
		}
		if profile.IsDryRunOption(argument) {
			return true
		}
	}
	return false
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
	if configuredDryRun(backupProfile, action) {
		return false, fmt.Errorf("scheduled %s cannot use a configured Restic dry-run option", action)
	}
	run := func(runCtx context.Context) error {
		runner, runnerErr := newRunner()
		if runnerErr != nil {
			return runnerErr
		}
		switch action {
		case schedule.ActionBackup:
			return Backup(runCtx, runner, backupProfile, false, output)
		case schedule.ActionForget:
			return Forget(runCtx, runner, backupProfile, false, state.Prune)
		case schedule.ActionCheck:
			return Check(runCtx, runner, backupProfile)
		case schedule.ActionPrune:
			return RunRestic(runCtx, runner, backupProfile, "prune", nil)
		case schedule.ActionCopy:
			return RunRestic(runCtx, runner, backupProfile, "copy", nil)
		default:
			return scheduleActionError(action)
		}
	}
	wait := time.Duration(0)
	if state.LockMode == schedule.LockWait {
		wait, err = time.ParseDuration(state.LockWait)
		if err != nil {
			return false, err
		}
	}
	recorder, due, err := runstatus.BeginActionIf(ctx, configDir, backupProfile.Name, action, wait, now, func(lastSuccess *time.Time) (bool, error) {
		if !state.CatchUp {
			return true, nil
		}
		if lastSuccess == nil {
			lastSuccess = &state.Installed
		}
		checkTime := now()
		for _, expression := range state.Expressions {
			expressionDue, dueErr := cronexpr.Due(expression, lastSuccess, checkTime)
			if dueErr != nil {
				return false, dueErr
			}
			if expressionDue {
				return true, nil
			}
		}
		return false, nil
	})
	if err != nil || !due {
		return due, err
	}
	return true, finishRecordedRun(ctx, recorder, backupProfile, now, output, run)
}

func scheduleActionError(action string) error {
	return errors.New("unsupported scheduled action " + action)
}

func recordRun(ctx context.Context, configDir string, backupProfile profile.Profile, action string, now func() time.Time, output io.Writer, run func(context.Context) error) error {
	recorder, err := runstatus.BeginAction(configDir, backupProfile.Name, action, now())
	if err != nil {
		return err
	}
	return finishRecordedRun(ctx, recorder, backupProfile, now, output, run)
}

func finishRecordedRun(ctx context.Context, recorder *runstatus.Recorder, backupProfile profile.Profile, now func() time.Time, output io.Writer, run func(context.Context) error) error {
	reporter := monitoring.New(backupProfile, output)
	_ = reporter.Report(ctx, "send-before", recorder.Status())
	runCtx, observation := observe(ctx)
	runErr := run(runCtx)
	outcome := observation.outcome(runErr, backupProfile.Monitoring.HistoryLimit)
	finishErr := recorder.FinishOutcome(outcome, now())
	status := recorder.Status()
	if status.Warning {
		_ = reporter.Report(ctx, "warning", status)
	}
	if runErr == nil {
		_ = reporter.Report(ctx, "send-after", status)
	} else {
		_ = reporter.Report(ctx, "send-after-fail", status)
	}
	_ = reporter.Report(ctx, "send-finally", status)
	return errors.Join(runErr, finishErr)
}
