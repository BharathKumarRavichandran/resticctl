package app

import (
	"context"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"resticctl/internal/profile"
	"resticctl/internal/schedule"
)

func TestRepositoryOperandsCannotBecomeOptions(t *testing.T) {
	p := profile.Profile{Name: "home"}
	operand := "--repo=local:other"
	tests := []struct {
		name     string
		run      func(*recordingRunner) error
		operands []string
	}{
		{"find", func(r *recordingRunner) error {
			return Find(context.Background(), r, p, []string{operand}, false, false, false, false)
		}, []string{operand}},
		{"ls", func(r *recordingRunner) error {
			return ListSnapshot(context.Background(), r, p, operand, []string{operand}, false, false, false, "", false)
		}, []string{operand, operand}},
		{"diff", func(r *recordingRunner) error { return Diff(context.Background(), r, p, operand, operand, true) }, []string{operand, operand}},
		{"dump", func(r *recordingRunner) error { return Dump(context.Background(), r, p, operand, operand, "", "") }, []string{operand, operand}},
		{"restore", func(r *recordingRunner) error { return Restore(context.Background(), r, p, operand, "target", false) }, []string{operand}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			r := &recordingRunner{}
			if err := test.run(r); err != nil {
				t.Fatal(err)
			}
			args := r.runs[0].arguments
			index := slices.Index(args, "--")
			if index < 0 || !slices.Equal(args[index+1:], test.operands) {
				t.Fatalf("unsafe operands: %v", args)
			}
		})
	}
}

func TestScheduledRunRejectsStdinWithoutProducer(t *testing.T) {
	p := profile.Profile{Name: "home", Stream: &profile.Stream{Filename: "stdin"}}
	_, err := ScheduledRun(context.Background(), nil, schedule.NewManager(), t.TempDir(), p, "backup", time.Now, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "stream.command") {
		t.Fatalf("error=%v", err)
	}
}

func TestRestoreRawAttachedIncludeIsNotAReservedFlag(t *testing.T) {
	runner := &recordingRunner{}
	if err := RunRestic(context.Background(), runner, profile.Profile{Name: "home"}, "restore", []string{"-qiprivate", "latest", "--target", "output"}); err != nil {
		t.Fatal(err)
	}
	if len(runner.runs) != 1 {
		t.Fatalf("runs=%d", len(runner.runs))
	}
}
