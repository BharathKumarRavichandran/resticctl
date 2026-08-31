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

// NewExecutor constructs an executor that omits environment keys rejected by
// blockedEnvironment when running database clients.
func NewExecutor(stdin io.Reader, stdout, stderr io.Writer, blockedEnvironment func(string) bool) *Executor {
	return &Executor{stdin: stdin, stdout: stdout, stderr: stderr, blockedEnvironment: blockedEnvironment}
}

func (executor *Executor) RunHook(ctx context.Context, arguments []string) error {
	return executor.run(ctx, "hook", arguments, nil, "")
}

func (executor *Executor) RunDatabase(ctx context.Context, arguments []string, environment map[string]string, cwd string) error {
	return executor.run(ctx, "database client", arguments, environment, cwd)
}

func (executor *Executor) run(ctx context.Context, label string, arguments []string, environment map[string]string, cwd string) error {
	if len(arguments) == 0 {
		return fmt.Errorf("cannot execute %s: command is empty", label)
	}
	command := exec.CommandContext(ctx, arguments[0], arguments[1:]...)
	command.Dir = cwd
	if environment != nil {
		command.Env = mergeEnvironment(os.Environ(), environment, executor.blockedEnvironment)
	}
	command.Stdin = executor.stdin
	command.Stdout = executor.stdout
	command.Stderr = executor.stderr
	if err := command.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			return fmt.Errorf("%s exited with status %d", label, exitError.ExitCode())
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
