package restic

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"

	"resticctl/internal/profile"
)

type Client struct {
	executable      string
	prefixArguments []string
	stdin           io.Reader
	stdout          io.Writer
	stderr          io.Writer
}

func New(stdin io.Reader, stdout, stderr io.Writer) (*Client, error) {
	requested := os.Getenv("RESTICCTL_RESTIC_COMMAND")
	if requested == "" {
		requested = "restic"
	}
	resolved, err := exec.LookPath(requested)
	if err != nil {
		return nil, fmt.Errorf("required command not found: %s", requested)
	}
	return &Client{executable: resolved, stdin: stdin, stdout: stdout, stderr: stderr}, nil
}

func (client *Client) Run(
	ctx context.Context,
	configuredProfile profile.Profile,
	arguments []string,
	cwd string,
) (runErr error) {
	passwordFile, temporary, err := preparePasswordFile(ctx, configuredProfile.Credentials)
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

	commandArgs := append([]string{}, client.prefixArguments...)
	commandArgs = append(commandArgs, configuredProfile.ResticArgs...)
	commandArgs = append(commandArgs, "--repo", configuredProfile.Repository, "--password-file", passwordFile)
	commandArgs = append(commandArgs, arguments...)
	command := exec.CommandContext(ctx, client.executable, commandArgs...)
	command.Dir = cwd
	command.Env = mergeEnvironment(os.Environ(), configuredProfile.Credentials.Environment)
	command.Stdin = client.stdin
	command.Stdout = client.stdout
	command.Stderr = client.stderr
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

func (client *Client) RunHook(ctx context.Context, arguments []string) error {
	command := exec.CommandContext(ctx, arguments[0], arguments[1:]...)
	command.Stdin = client.stdin
	command.Stdout = client.stdout
	command.Stderr = client.stderr
	if err := command.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			return fmt.Errorf("hook exited with status %d", exitError.ExitCode())
		}
		return fmt.Errorf("cannot execute hook: %w", err)
	}
	return nil
}

func (client *Client) RunDatabase(ctx context.Context, arguments []string, environment map[string]string, cwd string) error {
	command := exec.CommandContext(ctx, arguments[0], arguments[1:]...)
	command.Dir = cwd
	command.Env = mergeEnvironment(os.Environ(), environment)
	command.Stdin, command.Stdout, command.Stderr = client.stdin, client.stdout, client.stderr
	if err := command.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			return fmt.Errorf("database client exited with status %d", exitError.ExitCode())
		}
		return fmt.Errorf("cannot execute database client: %w", err)
	}
	return nil
}
