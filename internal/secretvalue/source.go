package secretvalue

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"

	"resticctl/internal/process"
)

var (
	ErrEmpty    = errors.New("secret is empty")
	ErrNUL      = errors.New("secret contains a NUL byte")
	ErrTooLarge = errors.New("secret exceeds 1 MiB")
)

// ReadFile reads a bounded secret file. The caller owns and should clear the result.
func ReadFile(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	data, readErr := io.ReadAll(io.LimitReader(file, MaximumBytes+1))
	if err := errors.Join(readErr, file.Close()); err != nil {
		clear(data)
		return nil, err
	}
	if len(data) > MaximumBytes {
		clear(data)
		return nil, ErrTooLarge
	}
	return data, nil
}

// RunCommand captures bounded stdout from an argument-vector command and discards stderr.
// The caller owns and should clear the result.
func RunCommand(ctx context.Context, arguments []string) ([]byte, error) {
	if len(arguments) == 0 {
		return nil, errors.New("secret command is empty")
	}
	command := exec.Command(arguments[0], arguments[1:]...)
	var output Buffer
	command.Stdout = &output
	command.Stderr = io.Discard
	if err := process.Run(ctx, command); err != nil {
		clear(output.Bytes())
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, err
	}
	if output.Exceeded() {
		clear(output.Bytes())
		return nil, ErrTooLarge
	}
	return output.Bytes(), nil
}

// Validate rejects empty, oversized, and NUL-containing secret data.
// Line endings are ignored only when deciding whether the data is empty.
func Validate(data []byte) error {
	if len(data) > MaximumBytes {
		return ErrTooLarge
	}
	if len(bytes.TrimRight(data, "\r\n")) == 0 {
		return ErrEmpty
	}
	if bytes.IndexByte(data, 0) >= 0 {
		return ErrNUL
	}
	return nil
}
