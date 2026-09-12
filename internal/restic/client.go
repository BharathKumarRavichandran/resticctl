package restic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

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

type CopyConfig struct {
	Source      Config
	Destination Config
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
const maximumDiagnostic = 64 << 10

type boundedBuffer struct {
	data      []byte
	truncated bool
}

func (buffer *boundedBuffer) Write(data []byte) (int, error) {
	remaining := maximumDiagnostic - len(buffer.data)
	if remaining > 0 {
		buffer.data = append(buffer.data, data[:min(remaining, len(data))]...)
	}
	if len(data) > remaining {
		buffer.truncated = true
	}
	return len(data), nil
}

func (buffer *boundedBuffer) String() string { return string(buffer.data) }

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

// writerOnly prevents os/exec from attaching an *os.File directly to the
// child. Some restic listing commands use terminal control sequences when
// stdout is a TTY, which can erase otherwise successful output.
type writerOnly struct{ io.Writer }

var lockIDPattern = regexp.MustCompile(`^[0-9a-f]+$`)
var isProcessActive = processActive

type repositoryLock struct {
	Time     time.Time `json:"time"`
	PID      int       `json:"pid"`
	Hostname string    `json:"hostname"`
}

func (client *Client) RecoverRepositoryLocks(ctx context.Context, config Config, minimumAge time.Duration, dryRun bool, now time.Time) error {
	var listed boundedBuffer
	if _, err := client.runInput(ctx, config, []string{"list", "locks"}, "", nil, client.stdin, &listed, io.Discard); err != nil {
		return err
	}
	ids := strings.Fields(listed.String())
	if len(ids) == 0 {
		return nil
	}
	host, err := os.Hostname()
	if err != nil {
		return err
	}
	foundStale := false
	for _, id := range ids {
		if !lockIDPattern.MatchString(id) {
			return errors.New("restic returned an invalid lock ID")
		}
		var encoded boundedBuffer
		if _, err := client.runInput(ctx, config, []string{"cat", "lock", id}, "", nil, client.stdin, &encoded, io.Discard); err != nil {
			return err
		}
		var lock repositoryLock
		if err := json.Unmarshal(encoded.data, &lock); err != nil {
			return fmt.Errorf("decode repository lock %s: %w", id, err)
		}
		if lock.Time.IsZero() || lock.PID <= 0 || lock.Hostname != host || now.Sub(lock.Time) < minimumAge {
			continue
		}
		active, err := isProcessActive(lock.PID)
		if err != nil {
			return err
		}
		if active {
			continue
		}
		foundStale = true
	}
	if !foundStale || dryRun {
		return nil
	}
	_, err = client.runInput(ctx, config, []string{"unlock"}, "", nil, client.stdin, client.stdout, client.stderr)
	return err
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
	_, runErr = client.runInput(ctx, config, arguments, cwd, nil, client.stdin, writerOnly{client.stdout}, client.stderr)
	return runErr
}

func (client *Client) Copy(ctx context.Context, config CopyConfig, arguments []string) error {
	if err := validateCopyEnvironment(config); err != nil {
		return err
	}
	return client.runWithSource(ctx, config.Destination, config.Source, "copy", arguments)
}

// InitCopyDestination initializes the destination, optionally reusing the
// source repository's chunker parameters.
func (client *Client) InitCopyDestination(ctx context.Context, config CopyConfig, copyChunkerParams bool) error {
	if err := validateCopyEnvironment(config); err != nil {
		return err
	}
	if copyChunkerParams {
		return client.runWithSource(ctx, config.Destination, config.Source, "init", []string{"--copy-chunker-params"})
	}
	return client.Run(ctx, config.Destination, []string{"init"}, "")
}

func validateCopyEnvironment(config CopyConfig) error {
	source := make(map[string]string, len(config.Source.Environment))
	for key, value := range config.Source.Environment {
		source[normalizeEnvKey(key)] = value
	}
	for key, value := range config.Destination.Environment {
		if sourceValue, ok := source[normalizeEnvKey(key)]; ok && sourceValue != value {
			return fmt.Errorf("source and destination credentials conflict on environment key %s", key)
		}
	}
	return nil
}

func (client *Client) runWithSource(ctx context.Context, destination, source Config, commandName string, arguments []string) (runErr error) {
	destinationPassword, destinationTemporary, err := preparePasswordFile(ctx, destination)
	if err != nil {
		return err
	}
	if destinationTemporary {
		defer func() { runErr = errors.Join(runErr, removePasswordFile(destinationPassword)) }()
	}
	sourcePassword, sourceTemporary, err := preparePasswordFile(ctx, source)
	if err != nil {
		return err
	}
	if sourceTemporary {
		defer func() { runErr = errors.Join(runErr, removePasswordFile(sourcePassword)) }()
	}

	commandArgs := append([]string{}, client.prefixArguments...)
	commandArgs = append(commandArgs, destination.Arguments...)
	commandArgs = append(commandArgs, "--repo", destination.Repository, "--password-file", destinationPassword)
	commandArgs = append(commandArgs, commandName, "--from-repo", source.Repository, "--from-password-file", sourcePassword)
	commandArgs = append(commandArgs, arguments...)
	command := exec.Command(client.executable, commandArgs...)
	environment := mergeEnvironment(os.Environ(), source.Environment)
	command.Env = mergeEnvironment(environment, destination.Environment)
	command.Stdin, command.Stdout, command.Stderr = client.stdin, writerOnly{client.stdout}, client.stderr
	if err := process.Run(ctx, command); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			return &ExitError{Code: exitError.ExitCode()}
		}
		return fmt.Errorf("cannot execute restic: %w", err)
	}
	return nil
}

func removePasswordFile(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("cannot remove temporary password file %s: %w", path, err)
	}
	return nil
}

// RunWithResult captures Restic's newline-delimited JSON summary while still
// streaming it to the configured output.
func (client *Client) RunWithResult(ctx context.Context, config Config, arguments []string, cwd string) (Result, error) {
	var capture summaryCapture
	return client.run(ctx, config, arguments, cwd, &capture)
}

// RunWithInput executes Restic with input as stdin and captures any JSON backup summary.
func (client *Client) RunWithInput(ctx context.Context, config Config, arguments []string, cwd string, input io.Reader) (Result, error) {
	var capture summaryCapture
	return client.runInput(ctx, config, arguments, cwd, &capture, input, client.stdout, client.stderr)
}

// RepositoryExists returns false only for Restic's explicit missing-repository
// diagnostic. Authentication, permission, and transport failures remain errors.
func (client *Client) RepositoryExists(ctx context.Context, config Config) (bool, error) {
	var diagnostic boundedBuffer
	_, err := client.runInput(ctx, config, []string{"cat", "config"}, "", nil, client.stdin, io.Discard, &diagnostic)
	if err == nil {
		return true, nil
	}
	message := strings.ToLower(diagnostic.String())
	if strings.Contains(message, "repository does not exist") {
		return false, nil
	}
	_, _ = io.WriteString(client.stderr, diagnostic.String())
	if diagnostic.truncated {
		_, _ = io.WriteString(client.stderr, "\n[restic diagnostic truncated]\n")
	}
	return false, err
}

func (client *Client) run(ctx context.Context, config Config, arguments []string, cwd string, capture *summaryCapture) (result Result, runErr error) {
	return client.runInput(ctx, config, arguments, cwd, capture, client.stdin, client.stdout, client.stderr)
}

func (client *Client) runInput(ctx context.Context, config Config, arguments []string, cwd string, capture *summaryCapture, input io.Reader, stdout, stderr io.Writer) (result Result, runErr error) {
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
	command.Stdin = input
	command.Stdout = stdout
	if capture != nil {
		command.Stdout = io.MultiWriter(stdout, capture)
	}
	command.Stderr = stderr
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
