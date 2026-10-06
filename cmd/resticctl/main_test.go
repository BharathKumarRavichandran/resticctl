package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"resticctl/internal/process"
	"resticctl/internal/restic"
)

func TestFinalStatusPreservesUsageErrors(t *testing.T) {
	var stderr bytes.Buffer
	status := finalStatus(2, errors.New("bad arguments"), 0, &stderr)
	if status != 2 {
		t.Fatalf("status = %d, want 2", status)
	}
	if !strings.Contains(stderr.String(), "bad arguments") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestFinalStatusPrefersSignalCode(t *testing.T) {
	if status := finalStatus(1, errors.New("cancelled"), 143, &bytes.Buffer{}); status != 143 {
		t.Fatalf("status = %d, want 143", status)
	}
}

func TestStreamCancelsIdleProducerAfterResticFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses fake Unix executables")
	}
	directory := t.TempDir()
	fakeRestic := filepath.Join(directory, "restic")
	producer := filepath.Join(directory, "producer")
	for path, script := range map[string]string{fakeRestic: "#!/bin/sh\nexit 12\n", producer: "#!/bin/sh\nexec sleep 30\n"} {
		if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("RESTICCTL_RESTIC_COMMAND", fakeRestic)
	client, err := restic.New(nil, io.Discard, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	runner := applicationRunner{Client: client, Executor: process.NewExecutor(nil, io.Discard, io.Discard, nil)}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	started := time.Now()
	_, err = runner.RunStream(ctx, restic.Config{Repository: "fake", PasswordValue: "test"}, []string{"backup", "--stdin"}, "", []string{producer})
	var exited *restic.ExitError
	if !errors.As(err, &exited) || exited.Code != 12 {
		t.Fatalf("stream error=%v", err)
	}
	if errors.Is(err, context.Canceled) {
		t.Fatalf("Restic failure was classified as cancellation: %v", err)
	}
	if elapsed := time.Since(started); elapsed >= 2*time.Second {
		t.Fatalf("failed consumer waited for idle producer: %s", elapsed)
	}
}

func TestStreamCancellationPreservesCleanupFailure(t *testing.T) {
	cleanupErr := errors.New("failed to terminate producer")
	err := withoutStreamCancellation(errors.Join(context.Canceled, cleanupErr))
	if errors.Is(err, context.Canceled) || !errors.Is(err, cleanupErr) {
		t.Fatalf("producer cleanup error = %v", err)
	}
}
