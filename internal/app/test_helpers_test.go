package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"

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
	return createDatabaseArtifact(arguments, cwd)
}

func createDatabaseArtifact(arguments []string, cwd string) error {
	for index, argument := range arguments {
		var path string
		switch {
		case argument == "--file" && index+1 < len(arguments):
			path = arguments[index+1]
		case strings.HasPrefix(argument, "--result-file="):
			path = strings.TrimPrefix(argument, "--result-file=")
		case argument == "--out" && index+1 < len(arguments):
			path = filepath.Join(arguments[index+1], "dump.bson")
		}
		if path == "" {
			continue
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(cwd, path)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte("dump"), 0o600); err != nil {
			return err
		}
	}
	return nil
}

type errorWriter struct{ err error }

func (writer errorWriter) Write([]byte) (int, error) { return 0, writer.err }
