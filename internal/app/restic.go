package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
)

type Runner interface {
	Run(context.Context, Profile, []string, string) error
}

type Restic struct {
	executable      string
	prefixArguments []string
	stdin           io.Reader
	stdout          io.Writer
	stderr          io.Writer
}

func NewRestic(stdin io.Reader, stdout, stderr io.Writer) (*Restic, error) {
	requested := os.Getenv("RESTICCTL_RESTIC_COMMAND")
	if requested == "" {
		requested = "restic"
	}
	resolved, err := exec.LookPath(requested)
	if err != nil {
		return nil, fmt.Errorf("required command not found: %s", requested)
	}
	return &Restic{executable: resolved, stdin: stdin, stdout: stdout, stderr: stderr}, nil
}

func (restic *Restic) Run(ctx context.Context, profile Profile, arguments []string, cwd string) (runErr error) {
	passwordFile, temporary, err := preparePasswordFile(ctx, profile.Credentials)
	if err != nil {
		return err
	}
	if temporary {
		defer func() {
			if err := os.Remove(passwordFile); err != nil && !errors.Is(err, os.ErrNotExist) {
				runErr = errors.Join(runErr, fmt.Errorf("cannot remove temporary password file %s: %w", passwordFile, err))
			}
		}()
	}

	commandArgs := append([]string{}, restic.prefixArguments...)
	commandArgs = append(commandArgs, profile.ResticArgs...)
	commandArgs = append(commandArgs, "--repo", profile.Repository, "--password-file", passwordFile)
	commandArgs = append(commandArgs, arguments...)
	command := exec.CommandContext(ctx, restic.executable, commandArgs...)
	command.Dir = cwd
	command.Env = mergeEnvironment(os.Environ(), profile.Credentials.Environment)
	command.Stdin = restic.stdin
	command.Stdout = restic.stdout
	command.Stderr = restic.stderr
	if err := command.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			return fmt.Errorf("restic exited with status %d", exitError.ExitCode())
		}
		return fmt.Errorf("cannot execute restic: %w", err)
	}
	return nil
}

func preparePasswordFile(ctx context.Context, credentials Credentials) (path string, temporary bool, err error) {
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

func mergeEnvironment(base []string, overrides map[string]string) []string {
	type variable struct{ key, value string }
	values := make(map[string]variable, len(base)+len(overrides))
	for _, entry := range base {
		if index := strings.IndexByte(entry, '='); index >= 0 {
			key := entry[:index]
			if !isManagedResticEnvironment(key) {
				values[normalizeEnvKey(key)] = variable{key, entry[index+1:]}
			}
		}
	}
	for key, value := range overrides {
		if !isManagedResticEnvironment(key) {
			values[normalizeEnvKey(key)] = variable{key, value}
		}
	}
	result := make([]string, 0, len(values))
	for _, item := range values {
		result = append(result, item.key+"="+item.value)
	}
	sort.Strings(result)
	return result
}
