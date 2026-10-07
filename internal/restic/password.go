package restic

import (
	"context"
	"errors"
	"fmt"
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
		case errors.Is(err, secretvalue.ErrEmpty):
			return "", false, errors.New("password value is empty")
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

func writeTemporaryPassword(password []byte) (string, bool, error) {
	path, err := securefile.WriteTemporary("", "resticctl-password-*", password, maximumPasswordBytes)
	return path, err == nil, err
}
