package process

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
)

// Executor runs lifecycle hooks and database clients without invoking a shell.
type Executor struct {
	stdin              io.Reader
	stdout             io.Writer
	stderr             io.Writer
	blockedEnvironment func(string) bool
}

// ExitError preserves subprocess status without retaining potentially
// sensitive command output.
type ExitError struct {
	Label string
	Code  int
}

func (err *ExitError) Error() string {
	return fmt.Sprintf("%s exited with status %d", err.Label, err.Code)
}
func (err *ExitError) ExitCode() int { return err.Code }

// NewExecutor constructs an executor that omits environment keys rejected by
// blockedEnvironment when running database clients.
func NewExecutor(stdin io.Reader, stdout, stderr io.Writer, blockedEnvironment func(string) bool) *Executor {
	return &Executor{stdin: stdin, stdout: stdout, stderr: stderr, blockedEnvironment: blockedEnvironment}
}

func (executor *Executor) RunHook(ctx context.Context, arguments []string) error {
	return executor.run(ctx, "hook", arguments, nil, "", nil)
}

func (executor *Executor) RunDatabase(ctx context.Context, arguments []string, environment map[string]string, cwd string) error {
	return executor.run(ctx, "database client", arguments, environment, cwd, executor.blockedEnvironment)
}

// RunProducer writes a command's stdout to output without invoking a shell.
func (executor *Executor) RunProducer(ctx context.Context, arguments []string, output io.Writer) error {
	if len(arguments) == 0 {
		return errors.New("cannot execute stream producer: command is empty")
	}
	command := exec.Command(arguments[0], arguments[1:]...)
	command.Env = mergeEnvironment(os.Environ(), nil, executor.blockedEnvironment)
	command.Stdin = executor.stdin
	command.Stdout = output
	command.Stderr = executor.stderr
	if err := Run(ctx, command); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			return &ExitError{Label: "stream producer", Code: exitError.ExitCode()}
		}
		return fmt.Errorf("cannot execute stream producer: %w", err)
	}
	return nil
}

// Pipe streams producer output into consumer and preserves errors from both
// sides. Closing the reader unblocks a producer when the consumer exits early.
func Pipe(producer func(io.Writer) error, consumer func(io.Reader) error) error {
	reader, writer := io.Pipe()
	producerResult := make(chan error, 1)
	go func() {
		err := producer(writer)
		_ = writer.CloseWithError(err)
		producerResult <- err
	}()
	consumerErr := consumer(reader)
	_ = reader.Close()
	return errors.Join(consumerErr, <-producerResult)
}

func (executor *Executor) run(ctx context.Context, label string, arguments []string, environment map[string]string, cwd string, blockedEnvironment func(string) bool) error {
	if len(arguments) == 0 {
		return fmt.Errorf("cannot execute %s: command is empty", label)
	}
	command := exec.Command(arguments[0], arguments[1:]...)
	command.Dir = cwd
	if environment != nil || blockedEnvironment != nil {
		command.Env = mergeEnvironment(os.Environ(), environment, blockedEnvironment)
	}
	command.Stdin = executor.stdin
	command.Stdout = executor.stdout
	command.Stderr = executor.stderr
	if err := Run(ctx, command); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			return &ExitError{Label: label, Code: exitError.ExitCode()}
		}
		return fmt.Errorf("cannot execute %s: %w", label, err)
	}
	return nil
}

func mergeEnvironment(base []string, overrides map[string]string, blocked func(string) bool) []string {
	type variable struct{ key, value string }
	values := make(map[string]variable, len(base)+len(overrides))
	for _, entry := range base {
		if index := strings.IndexByte(entry, '='); index >= 0 {
			key := entry[:index]
			if blocked == nil || !blocked(key) {
				values[normalizeEnvKey(key)] = variable{key, entry[index+1:]}
			}
		}
	}
	for key, value := range overrides {
		if blocked == nil || !blocked(key) {
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
