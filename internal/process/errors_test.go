package process

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestCommandErrorPreservesSupervisionFailuresAndSanitizesExit(t *testing.T) {
	t.Setenv("GO_WANT_PROCESS_HELPER", "1")
	t.Setenv("GO_PROCESS_EXIT_FAILURE", "1")
	_, err := exec.Command(os.Args[0], "-test.run=^TestProcessHelper$").Output()
	var rawExit *exec.ExitError
	if !errors.As(err, &rawExit) || !strings.Contains(string(rawExit.Stderr), "secret diagnostic") {
		t.Fatalf("helper did not produce expected exit and diagnostic: %v", err)
	}
	supervisionErr := errors.New("process tree cleanup failed")
	for _, cancelled := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		if cancelled {
			cancel()
		}
		classified := CommandError(ctx, "helper", fmt.Errorf("wrapped: %w", errors.Join(err, supervisionErr)))
		cancel()
		var exitError *ExitError
		if !errors.As(classified, &exitError) || exitError.Code != 7 {
			t.Fatalf("exit status lost: %v", classified)
		}
		if !errors.Is(classified, supervisionErr) {
			t.Fatalf("supervision failure lost: %v", classified)
		}
		if cancelled && !errors.Is(classified, context.Canceled) {
			t.Fatalf("cancellation lost: %v", classified)
		}
		var retained *exec.ExitError
		if errors.As(classified, &retained) || strings.Contains(classified.Error(), "secret diagnostic") {
			t.Fatalf("raw exit or diagnostic retained: %v", classified)
		}
	}
}

func TestCommandErrorPreservesCancellationCleanupFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cleanupErr := errors.New("failed to terminate process tree")
	err := CommandError(ctx, "helper", errors.Join(ctx.Err(), cleanupErr))
	if !errors.Is(err, context.Canceled) || !errors.Is(err, cleanupErr) {
		t.Fatalf("cancellation or cleanup failure lost: %v", err)
	}
}
