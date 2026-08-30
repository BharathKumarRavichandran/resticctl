package restic

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"

	"resticctl/internal/profile"
)

func preparePasswordFile(ctx context.Context, credentials profile.Credentials) (path string, temporary bool, err error) {
	if credentials.Password.File != "" {
		return credentials.Password.File, false, nil
	}
	commandParts := credentials.Password.Command
	if len(commandParts) == 0 {
		return "", false, errors.New("password source is not configured")
	}
	command := exec.CommandContext(ctx, commandParts[0], commandParts[1:]...)
	var stdout bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = io.Discard
	if err := command.Run(); err != nil {
		if ctx.Err() != nil {
			return "", false, ctx.Err()
		}
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			return "", false, fmt.Errorf("password command exited with status %d", exitError.ExitCode())
		}
		return "", false, fmt.Errorf("cannot execute password command %s: %w", commandParts[0], err)
	}
	password := stdout.Bytes()
	defer clear(password)
	if len(bytes.TrimRight(password, "\r\n")) == 0 {
		return "", false, errors.New("password command returned an empty password")
	}
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
	if err := file.Chmod(0o600); err != nil {
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
