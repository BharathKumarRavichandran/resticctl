package app

import (
	"context"

	"resticctl/internal/restic"
)

type recordedRun struct {
	config    restic.Config
	arguments []string
	cwd       string
}

type recordingRunner struct{ runs []recordedRun }

func (runner *recordingRunner) Run(_ context.Context, config restic.Config, arguments []string, cwd string) error {
	runner.runs = append(runner.runs, recordedRun{config: config, arguments: append([]string(nil), arguments...), cwd: cwd})
	return nil
}

func (runner *recordingRunner) RunHook(_ context.Context, arguments []string) error { return nil }
func (runner *recordingRunner) RunDatabase(_ context.Context, arguments []string, _ map[string]string, cwd string) error {
	runner.runs = append(runner.runs, recordedRun{arguments: append([]string(nil), arguments...), cwd: cwd})
	return nil
}

type errorWriter struct{ err error }

func (writer errorWriter) Write([]byte) (int, error) { return 0, writer.err }
