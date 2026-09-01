package app

import (
	"context"
	"errors"
	"sync"

	"resticctl/internal/profile"
	"resticctl/internal/restic"
	"resticctl/internal/runstatus"
)

type runObservation struct {
	mu           sync.Mutex
	warning      bool
	warningState bool
	statistics   *runstatus.Statistics
	exitCode     *int
}

type observationKey struct{}

func observe(ctx context.Context) (context.Context, *runObservation) {
	observation := &runObservation{}
	return context.WithValue(ctx, observationKey{}, observation), observation
}

func invokeRestic(ctx context.Context, runner ResticRunner, backupProfile profile.Profile, arguments []string, cwd string) error {
	var err error
	if backupProfile.Monitoring.BackupStatistics && len(arguments) > 0 && arguments[0] == "backup" {
		if capable, ok := runner.(resultRunner); ok {
			jsonArguments := arguments
			if !hasOption(arguments, "--json") {
				jsonArguments = insertOption(arguments, "--json")
			}
			result, runErr := capable.RunWithResult(ctx, resticConfig(backupProfile), jsonArguments, cwd)
			err = runErr
			if result.Summary != nil {
				if observation, ok := ctx.Value(observationKey{}).(*runObservation); ok {
					summary := result.Summary
					observation.mu.Lock()
					observation.statistics = &runstatus.Statistics{FilesNew: summary.FilesNew, FilesChanged: summary.FilesChanged, FilesUnmodified: summary.FilesUnmodified, DirsNew: summary.DirsNew, DirsChanged: summary.DirsChanged, DirsUnmodified: summary.DirsUnmodified, DataBlobs: summary.DataBlobs, TreeBlobs: summary.TreeBlobs, DataAddedBytes: summary.DataAddedBytes, TotalFilesProcessed: summary.TotalFilesProcessed, TotalBytesProcessed: summary.TotalBytesProcessed}
					observation.mu.Unlock()
				}
			}
		} else {
			err = runner.Run(ctx, resticConfig(backupProfile), arguments, cwd)
		}
	} else {
		err = runner.Run(ctx, resticConfig(backupProfile), arguments, cwd)
	}
	var exitError *restic.ExitError
	if !errors.As(err, &exitError) || exitError.Code != 3 {
		return err
	}
	if observation, ok := ctx.Value(observationKey{}).(*runObservation); ok {
		observation.mu.Lock()
		observation.warning = true
		observation.warningState = backupProfile.Monitoring.WarningPolicy == "warning"
		code := exitError.Code
		observation.exitCode = &code
		observation.mu.Unlock()
	}
	if backupProfile.Monitoring.WarningPolicy == "warning" || backupProfile.Monitoring.WarningPolicy == "success" {
		return nil
	}
	return err
}

func hasOption(arguments []string, target string) bool {
	for _, argument := range arguments {
		if argument == "--" {
			return false
		}
		if argument == target {
			return true
		}
	}
	return false
}

func insertOption(arguments []string, option string) []string {
	result := append([]string(nil), arguments...)
	for index, argument := range result {
		if argument == "--" {
			return append(result[:index], append([]string{option}, result[index:]...)...)
		}
	}
	return append(result, option)
}

func (observation *runObservation) outcome(err error, historyLimit int) runstatus.Outcome {
	observation.mu.Lock()
	defer observation.mu.Unlock()
	return runstatus.Outcome{Err: err, ExitCode: observation.exitCode, Warning: observation.warning, WarningState: observation.warningState, Statistics: observation.statistics, HistoryLimit: historyLimit}
}
