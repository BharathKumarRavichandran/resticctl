package databasebackup

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"resticctl/internal/profile"
	"resticctl/internal/secretvalue"
)

const maximumPasswordBytes = secretvalue.MaximumBytes

func ResolvePassword(ctx context.Context, source profile.PasswordSource) (string, error) {
	var password string
	if source.Value != "" {
		if len(source.Value) > maximumPasswordBytes {
			return "", errors.New("database password value exceeds 1 MiB")
		}
		password = source.Value
	} else if source.File != "" {
		data, err := secretvalue.ReadFile(source.File)
		if err != nil {
			if errors.Is(err, secretvalue.ErrTooLarge) {
				return "", errors.New("database password file exceeds 1 MiB")
			}
			return "", err
		}
		defer clear(data)
		password = strings.TrimRight(string(data), "\r\n")
	} else if len(source.Command) == 0 {
		return "", nil
	} else {
		output, err := secretvalue.RunCommand(ctx, source.Command)
		defer clear(output)
		if err != nil {
			if errors.Is(err, secretvalue.ErrTooLarge) {
				return "", errors.New("database password command output exceeds 1 MiB")
			}
			var exitError *exec.ExitError
			if errors.As(err, &exitError) {
				return "", fmt.Errorf("database password command exited with status %d", exitError.ExitCode())
			}
			return "", fmt.Errorf("cannot execute database password command: %w", err)
		}
		password = strings.TrimRight(string(output), "\r\n")
	}
	if password == "" {
		return "", errors.New("database password source returned an empty password")
	}
	if strings.ContainsRune(password, 0) {
		return "", errors.New("database password source returned a NUL byte")
	}
	return password, nil
}
