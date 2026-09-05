package databasebackup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"resticctl/internal/process"
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
		file, err := os.Open(source.File)
		if err != nil {
			return "", err
		}
		data, readErr := io.ReadAll(io.LimitReader(file, maximumPasswordBytes+1))
		defer clear(data)
		closeErr := file.Close()
		if err := errors.Join(readErr, closeErr); err != nil {
			return "", err
		}
		if len(data) > maximumPasswordBytes {
			return "", errors.New("database password file exceeds 1 MiB")
		}
		password = strings.TrimRight(string(data), "\r\n")
	} else if len(source.Command) == 0 {
		return "", nil
	} else {
		command := exec.Command(source.Command[0], source.Command[1:]...)
		var output secretvalue.Buffer
		defer func() { clear(output.Bytes()) }()
		command.Stdout, command.Stderr = &output, io.Discard
		if err := process.Run(ctx, command); err != nil {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			var exitError *exec.ExitError
			if errors.As(err, &exitError) {
				return "", fmt.Errorf("database password command exited with status %d", exitError.ExitCode())
			}
			return "", fmt.Errorf("cannot execute database password command: %w", err)
		}
		if output.Exceeded() {
			return "", errors.New("database password command output exceeds 1 MiB")
		}
		password = strings.TrimRight(string(output.Bytes()), "\r\n")
	}
	if password == "" {
		return "", errors.New("database password source returned an empty password")
	}
	if strings.ContainsRune(password, 0) {
		return "", errors.New("database password source returned a NUL byte")
	}
	return password, nil
}
