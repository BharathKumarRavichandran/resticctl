package cli

import (
	"context"

	"resticctl/internal/profile"
)

type recordedRun struct {
	arguments []string
}

type recordingRunner struct{ runs []recordedRun }

func (runner *recordingRunner) Run(
	_ context.Context,
	_ profile.Profile,
	arguments []string,
	_ string,
) error {
	runner.runs = append(runner.runs, recordedRun{arguments: append([]string(nil), arguments...)})
	return nil
}

type errorWriter struct{ err error }

func (writer errorWriter) Write([]byte) (int, error) { return 0, writer.err }
