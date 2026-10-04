package process

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
)

// CommandError classifies a failed command without retaining its output.
func CommandError(ctx context.Context, label string, err error) error {
	if err == nil {
		return nil
	}
	classified := classifyCommandError(label, err)
	if ctx.Err() != nil && !errors.Is(classified, ctx.Err()) {
		return errors.Join(ctx.Err(), classified)
	}
	return classified
}

func classifyCommandError(label string, err error) error {
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		causes := joined.Unwrap()
		classified := make([]error, len(causes))
		for i, cause := range causes {
			classified[i] = classifyCommandError(label, cause)
		}
		return errors.Join(classified...)
	}
	var exitError *exec.ExitError
	if errors.As(err, &exitError) {
		if wrapped, ok := err.(interface{ Unwrap() error }); ok {
			return classifyCommandError(label, wrapped.Unwrap())
		}
		return &ExitError{Label: label, Code: exitError.ExitCode()}
	}
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	if _, ok := err.(*ExitError); ok {
		return err
	}
	return fmt.Errorf("cannot execute %s: %w", label, err)
}
