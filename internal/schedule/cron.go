package schedule

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

func (manager Manager) installCron(ctx context.Context, state State, executable, configDir string) error {
	current, err := manager.crontab(ctx)
	if err != nil {
		return err
	}
	current, err = withoutCronJob(current, state.Profile, state.Action)
	if err != nil {
		return err
	}
	arguments := manager.jobArguments(executable, configDir, state)
	quoted := make([]string, len(arguments))
	for index, argument := range arguments {
		if strings.ContainsAny(argument, "\r\n\x00") {
			return errors.New("scheduled command path contains an invalid character")
		}
		quoted[index] = strings.ReplaceAll(shellQuote(argument), "%", "\\%")
	}
	begin, end := cronMarkers(state.Profile, state.Action)
	command := strings.Join(quoted, " ")
	entry := begin + "\n" + state.Expression + " " + command + "\n"
	if state.CatchUp {
		entry += "@reboot " + command + "\n"
	}
	entry += end + "\n"
	if current != "" && !strings.HasSuffix(current, "\n") {
		current += "\n"
	}
	output, err := manager.Executor.Run(ctx, []byte(current+entry), "crontab", "-")
	if err != nil {
		return commandError("install crontab", output, err)
	}
	return nil
}

func (manager Manager) removeCron(ctx context.Context, name, action string) error {
	current, err := manager.crontab(ctx)
	if err != nil {
		return err
	}
	updated, err := withoutCronJob(current, name, action)
	if err != nil {
		return err
	}
	output, err := manager.Executor.Run(ctx, []byte(updated), "crontab", "-")
	if err != nil {
		return commandError("update crontab", output, err)
	}
	return nil
}

func (manager Manager) crontab(ctx context.Context) (string, error) {
	output, err := manager.Executor.Run(ctx, nil, "crontab", "-l")
	if err == nil {
		return string(output), nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return "", nil
	}
	return "", commandError("read crontab", output, err)
}

func cronMarkers(name, action string) (string, string) {
	id := name
	if action != ActionBackup {
		id += ":" + action
	}
	return "# resticctl:" + id + ":begin", "# resticctl:" + id + ":end"
}

func withoutCronJob(content, name, action string) (string, error) {
	begin, end := cronMarkers(name, action)
	lines := strings.SplitAfter(content, "\n")
	var output strings.Builder
	inJob := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == begin:
			if inJob {
				return "", errors.New("invalid nested resticctl crontab marker")
			}
			inJob = true
		case trimmed == end:
			if !inJob {
				return "", errors.New("resticctl crontab end marker has no matching start")
			}
			inJob = false
		default:
			if !inJob {
				output.WriteString(line)
			}
		}
	}
	if inJob {
		return "", errors.New("resticctl crontab start marker has no matching end")
	}
	return output.String(), nil
}

func commandError(action string, output []byte, err error) error {
	message := strings.TrimSpace(string(output))
	if message == "" {
		return fmt.Errorf("cannot %s: %w", action, err)
	}
	return fmt.Errorf("cannot %s: %w: %s", action, err, message)
}
