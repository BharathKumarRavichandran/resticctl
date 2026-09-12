package restic

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"

	"resticctl/internal/secretvalue"
	"resticctl/internal/securefile"
)

const maximumPasswordBytes = secretvalue.MaximumBytes

func preparePasswordFile(ctx context.Context, config Config) (path string, temporary bool, err error) {
	if config.PasswordFile != "" {
		return config.PasswordFile, false, nil
	}
	if config.PasswordValue != "" {
		password := []byte(config.PasswordValue)
		defer clear(password)
		switch err := secretvalue.Validate(password); {
		case errors.Is(err, secretvalue.ErrTooLarge):
			return "", false, errors.New("password value exceeds 1 MiB")
		case errors.Is(err, secretvalue.ErrNUL):
			return "", false, errors.New("password value contains a NUL byte")
		}
		return writeTemporaryPassword(password)
	}
	commandParts := config.PasswordCommand
	if len(commandParts) == 0 {
		return "", false, errors.New("password source is not configured")
	}
	password, err := secretvalue.RunCommand(ctx, commandParts)
	defer clear(password)
	if err != nil {
		if errors.Is(err, secretvalue.ErrTooLarge) {
			return "", false, errors.New("password command output exceeds 1 MiB")
		}
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			return "", false, fmt.Errorf("password command exited with status %d", exitError.ExitCode())
		}
		return "", false, fmt.Errorf("cannot execute password command %s: %w", commandParts[0], err)
	}
	switch err := secretvalue.Validate(password); {
	case errors.Is(err, secretvalue.ErrEmpty):
		return "", false, errors.New("password command returned an empty password")
	case errors.Is(err, secretvalue.ErrNUL):
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
