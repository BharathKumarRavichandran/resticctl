package restic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"

	"resticctl/internal/process"
)

// Config contains only the repository settings needed to invoke Restic.
type Config struct {
	Repository      string
	Arguments       []string
	Environment     map[string]string
	PasswordCommand []string
	PasswordFile    string
	PasswordValue   string
}

// ExitError preserves Restic's process status for policy and monitoring code.
type ExitError struct{ Code int }

func (err *ExitError) Error() string { return fmt.Sprintf("restic exited with status %d", err.Code) }

func (err *ExitError) ExitCode() int { return err.Code }

type BackupSummary struct {
	FilesNew            uint64 `json:"files_new"`
	FilesChanged        uint64 `json:"files_changed"`
	FilesUnmodified     uint64 `json:"files_unmodified"`
	DirsNew             uint64 `json:"dirs_new"`
	DirsChanged         uint64 `json:"dirs_changed"`
	DirsUnmodified      uint64 `json:"dirs_unmodified"`
	DataBlobs           uint64 `json:"data_blobs"`
	TreeBlobs           uint64 `json:"tree_blobs"`
	DataAddedBytes      uint64 `json:"data_added"`
	TotalFilesProcessed uint64 `json:"total_files_processed"`
	TotalBytesProcessed uint64 `json:"total_bytes_processed"`
}

type Result struct{ Summary *BackupSummary }

const maximumJSONLine = 1 << 20

type summaryCapture struct {
	line    []byte
	discard bool
	summary *BackupSummary
}

func (capture *summaryCapture) Write(data []byte) (int, error) {
	for _, character := range data {
		if character == '\n' {
			capture.consume()
			continue
		}
		if !capture.discard {
			if len(capture.line) < maximumJSONLine {
				capture.line = append(capture.line, character)
			} else {
				capture.line = capture.line[:0]
				capture.discard = true
			}
		}
	}
	return len(data), nil
}

func (capture *summaryCapture) consume() {
	if !capture.discard && len(capture.line) > 0 {
		var message struct {
			MessageType string `json:"message_type"`
			BackupSummary
		}
		if json.Unmarshal(capture.line, &message) == nil && message.MessageType == "summary" {
			summary := message.BackupSummary
			capture.summary = &summary
		}
	}
	capture.line = capture.line[:0]
	capture.discard = false
}

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
	config Config,
	arguments []string,
	cwd string,
) (runErr error) {
	_, runErr = client.run(ctx, config, arguments, cwd, nil)
	return runErr
}

// RunWithResult captures Restic's newline-delimited JSON summary while still
// streaming it to the configured output.
func (client *Client) RunWithResult(ctx context.Context, config Config, arguments []string, cwd string) (Result, error) {
	var capture summaryCapture
	return client.run(ctx, config, arguments, cwd, &capture)
}

func (client *Client) run(ctx context.Context, config Config, arguments []string, cwd string, capture *summaryCapture) (result Result, runErr error) {
	passwordFile, temporary, err := preparePasswordFile(ctx, config)
	if err != nil {
		return Result{}, err
	}
	if temporary {
		defer func() {
			if err := os.Remove(passwordFile); err != nil && !errors.Is(err, os.ErrNotExist) {
				runErr = errors.Join(runErr, fmt.Errorf("cannot remove temporary password file %s: %w", passwordFile, err))
			}
		}()
	}

	commandArgs := append([]string{}, client.prefixArguments...)
	commandArgs = append(commandArgs, config.Arguments...)
	commandArgs = append(commandArgs, "--repo", config.Repository, "--password-file", passwordFile)
	commandArgs = append(commandArgs, arguments...)
	command := exec.Command(client.executable, commandArgs...)
	command.Dir = cwd
	command.Env = mergeEnvironment(os.Environ(), config.Environment)
	command.Stdin = client.stdin
	command.Stdout = client.stdout
	if capture != nil {
		command.Stdout = io.MultiWriter(client.stdout, capture)
	}
	command.Stderr = client.stderr
	commandErr := process.Run(ctx, command)
	if capture != nil {
		capture.consume()
		result.Summary = capture.summary
	}
	if commandErr != nil {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		var exitError *exec.ExitError
		if errors.As(commandErr, &exitError) {
			return result, &ExitError{Code: exitError.ExitCode()}
		}
		return result, fmt.Errorf("cannot execute restic: %w", commandErr)
	}
	return result, nil
}
