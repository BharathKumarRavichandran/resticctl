package process

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type helperResult struct {
	Directory string `json:"directory"`
	Value     string `json:"value"`
}

func TestProcessHelper(t *testing.T) {
	if os.Getenv("GO_WANT_PROCESS_HELPER") != "1" {
		return
	}
	directory, err := os.Getwd()
	if err != nil {
		os.Exit(2)
	}
	if err := json.NewEncoder(os.Stdout).Encode(helperResult{
		Directory: directory,
		Value:     os.Getenv("PROCESS_HELPER_VALUE"),
	}); err != nil {
		os.Exit(3)
	}
	os.Exit(0)
}

func TestRunDatabaseUsesEnvironmentAndWorkingDirectory(t *testing.T) {
	directory := t.TempDir()
	var output bytes.Buffer
	executor := NewExecutor(nil, &output, nil, isResticEnvironment)
	environment := map[string]string{
		"GO_WANT_PROCESS_HELPER": "1",
		"PROCESS_HELPER_VALUE":   "private-value",
		"RESTIC_PASSWORD":        "must-not-leak",
	}
	if err := executor.RunDatabase(context.Background(), []string{os.Args[0], "-test.run=TestProcessHelper"}, environment, directory); err != nil {
		t.Fatal(err)
	}
	var result helperResult
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	resolvedDirectory, err := filepath.EvalSymlinks(directory)
	if err != nil {
		t.Fatal(err)
	}
	actualDirectory, err := filepath.EvalSymlinks(result.Directory)
	if err != nil {
		t.Fatal(err)
	}
	if actualDirectory != resolvedDirectory || result.Value != "private-value" {
		t.Fatalf("helper result = %#v", result)
	}
}

func TestRunHookRejectsEmptyCommand(t *testing.T) {
	err := NewExecutor(nil, nil, nil, nil).RunHook(context.Background(), nil)
	if err == nil {
		t.Fatal("empty hook command succeeded")
	}
}

func TestMergeEnvironmentDropsResticSelectors(t *testing.T) {
	environment := mergeEnvironment(
		[]string{"PATH=/bin", "RESTIC_PASSWORD=ambient-secret", "RESTIC_REPOSITORY=wrong"},
		map[string]string{"AWS_ACCESS_KEY_ID": "key"},
		isResticEnvironment,
	)
	joined := strings.Join(environment, "\n")
	if strings.Contains(joined, "RESTIC_PASSWORD") || strings.Contains(joined, "RESTIC_REPOSITORY") {
		t.Fatalf("reserved Restic environment leaked through: %v", environment)
	}
	if !strings.Contains(joined, "AWS_ACCESS_KEY_ID=key") {
		t.Fatalf("credential environment missing: %v", environment)
	}
}

func isResticEnvironment(key string) bool {
	return strings.HasPrefix(key, "RESTIC_")
}
