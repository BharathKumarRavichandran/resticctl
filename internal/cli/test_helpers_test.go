package cli

import (
	"context"
	"io"
	"os"
	"time"

	"resticctl/internal/app"
	"resticctl/internal/profile"
	"resticctl/internal/schedule"
)

const testVersion = "0.1.0-test"

func testDependencies() Dependencies {
	return Dependencies{
		NewRunner:          func() (app.Runner, error) { return &recordingRunner{}, nil },
		NewScheduleManager: func() schedule.Manager { return schedule.NewManager() },
		Executable:         os.Executable,
		Now:                time.Now,
		Version:            testVersion,
	}
}

func runForTest(ctx context.Context, arguments []string, stdout, stderr io.Writer) (int, error) {
	return Run(ctx, arguments, stdout, stderr, testDependencies())
}

func newTestCommandLine(stdout, stderr io.Writer) *commandLine {
	return newCommandLine(stdout, stderr, testDependencies())
}

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

func (runner *recordingRunner) RunHook(_ context.Context, _ []string) error { return nil }
func (runner *recordingRunner) RunDatabase(_ context.Context, _ []string, _ map[string]string, _ string) error {
	return nil
}

type errorWriter struct{ err error }

func (writer errorWriter) Write([]byte) (int, error) { return 0, writer.err }
