package process

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type helperResult struct {
	Directory string `json:"directory"`
	Value     string `json:"value"`
	Reserved  string `json:"reserved"`
}

func TestProcessHelper(t *testing.T) {
	if marker := os.Getenv("GO_PROCESS_TREE_CHILD"); marker != "" {
		time.Sleep(500 * time.Millisecond)
		if err := os.WriteFile(marker, []byte("survived"), 0o600); err != nil {
			os.Exit(4)
		}
		os.Exit(0)
	}
	if os.Getenv("GO_WANT_PROCESS_HELPER") != "1" {
		return
	}
	if marker := os.Getenv("GO_PROCESS_TREE_MARKER"); marker != "" {
		child := exec.Command(os.Args[0], "-test.run=TestProcessHelper")
		child.Env = append(os.Environ(), "GO_WANT_PROCESS_HELPER=", "GO_PROCESS_TREE_CHILD="+marker)
		if err := child.Start(); err != nil {
			os.Exit(5)
		}
		if err := os.WriteFile(marker+".ready", []byte("ready"), 0o600); err != nil {
			os.Exit(6)
		}
		_ = child.Wait()
		os.Exit(0)
	}
	directory, err := os.Getwd()
	if err != nil {
		os.Exit(2)
	}
	if err := json.NewEncoder(os.Stdout).Encode(helperResult{
		Directory: directory,
		Value:     os.Getenv("PROCESS_HELPER_VALUE"),
		Reserved:  os.Getenv("RESTIC_PASSWORD"),
	}); err != nil {
		os.Exit(3)
	}
	os.Exit(0)
}

func TestCancellationTerminatesProcessTree(t *testing.T) {
	directory := t.TempDir()
	marker := filepath.Join(directory, "child-finished")
	t.Setenv("GO_WANT_PROCESS_HELPER", "1")
	t.Setenv("GO_PROCESS_TREE_MARKER", marker)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- NewExecutor(nil, nil, nil, nil).RunHook(ctx, []string{os.Args[0], "-test.run=TestProcessHelper"})
	}()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(marker + ".ready"); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("child process did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("RunHook error = %v", err)
	}
	time.Sleep(700 * time.Millisecond)
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("descendant survived cancellation: %v", err)
	}
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

func TestRunDatabaseFiltersAmbientReservedEnvironmentWithoutOverrides(t *testing.T) {
	t.Setenv("GO_WANT_PROCESS_HELPER", "1")
	t.Setenv("RESTIC_PASSWORD", "must-not-leak")
	var output bytes.Buffer
	executor := NewExecutor(nil, &output, nil, isResticEnvironment)
	if err := executor.RunDatabase(context.Background(), []string{os.Args[0], "-test.run=TestProcessHelper"}, nil, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	var result helperResult
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Reserved != "" {
		t.Fatal("database process inherited RESTIC_PASSWORD")
	}
}

func TestRunHookPreservesAmbientEnvironment(t *testing.T) {
	t.Setenv("GO_WANT_PROCESS_HELPER", "1")
	t.Setenv("RESTIC_PASSWORD", "hook-secret")
	var output bytes.Buffer
	executor := NewExecutor(nil, &output, nil, isResticEnvironment)
	if err := executor.RunHook(context.Background(), []string{os.Args[0], "-test.run=TestProcessHelper"}); err != nil {
		t.Fatal(err)
	}
	var result helperResult
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Reserved != "hook-secret" {
		t.Fatalf("hook environment value = %q", result.Reserved)
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
