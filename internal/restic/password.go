package restic

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"resticctl/internal/process"
	"resticctl/internal/secretvalue"
	"resticctl/internal/securefile"
)

const maximumPasswordBytes = secretvalue.MaximumBytes

func preparePasswordFile(ctx context.Context, config Config) (path string, temporary bool, err error) {
	if config.PasswordFile != "" {
		return config.PasswordFile, false, nil
	}
	if config.PasswordValue != "" {
		if len(config.PasswordValue) > maximumPasswordBytes {
			return "", false, errors.New("password value exceeds 1 MiB")
		}
		if strings.IndexByte(config.PasswordValue, 0) >= 0 {
			return "", false, errors.New("password value contains a NUL byte")
		}
		password := []byte(config.PasswordValue)
		defer clear(password)
		return writeTemporaryPassword(password)
	}
	commandParts := config.PasswordCommand
	if len(commandParts) == 0 {
		return "", false, errors.New("password source is not configured")
	}
	command := exec.Command(commandParts[0], commandParts[1:]...)
	var stdout secretvalue.Buffer
	defer func() { clear(stdout.Bytes()) }()
	command.Stdout = &stdout
	command.Stderr = io.Discard
	if err := process.Run(ctx, command); err != nil {
		if ctx.Err() != nil {
			return "", false, ctx.Err()
		}
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			return "", false, fmt.Errorf("password command exited with status %d", exitError.ExitCode())
		}
		return "", false, fmt.Errorf("cannot execute password command %s: %w", commandParts[0], err)
	}
	if stdout.Exceeded() {
		return "", false, errors.New("password command output exceeds 1 MiB")
	}
	password := stdout.Bytes()
	if len(bytes.TrimRight(password, "\r\n")) == 0 {
		return "", false, errors.New("password command returned an empty password")
	}
	if bytes.IndexByte(password, 0) >= 0 {
		return "", false, errors.New("password command returned a NUL byte")
	}
	return writeTemporaryPassword(password)
}

func writeTemporaryPassword(password []byte) (path string, temporary bool, err error) {
	file, err := os.CreateTemp("", "resticctl-password-")
	if err != nil {
		return "", false, fmt.Errorf("cannot create temporary password file: %w", err)
	}
	path = file.Name()
	ok := false
	closed := false
	defer func() {
		if !ok {
			if !closed {
				if closeErr := file.Close(); closeErr != nil {
					err = errors.Join(err, fmt.Errorf("cannot close temporary password file: %w", closeErr))
				}
			}
			if removeErr := os.Remove(path); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
				err = errors.Join(err, fmt.Errorf("cannot remove temporary password file %s: %w", path, removeErr))
			}
		}
	}()
	if err := securefile.Protect(path); err != nil {
		return "", false, fmt.Errorf("cannot protect temporary password file: %w", err)
	}
	if _, err := file.Write(password); err != nil {
		return "", false, fmt.Errorf("cannot write temporary password file: %w", err)
	}
	closeErr := file.Close()
	closed = true
	if closeErr != nil {
		return "", false, fmt.Errorf("cannot close temporary password file: %w", closeErr)
	}
	ok = true
	return path, true, nil
}
