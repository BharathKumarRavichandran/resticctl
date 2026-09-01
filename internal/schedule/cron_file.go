package schedule

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"resticctl/internal/securefile"
)

func (manager Manager) renderCron(state State, executable, configDir string) ([]byte, error) {
	args := manager.jobArguments(executable, configDir, state)
	quoted := make([]string, len(args))
	for i, arg := range args {
		if strings.ContainsAny(arg, "\r\n\x00") {
			return nil, errors.New("scheduled command path contains an invalid character")
		}
		quoted[i] = strings.ReplaceAll(shellQuote(arg), "%", "\\%")
	}
	command := strings.Join(quoted, " ")
	if state.Priority == PriorityBackground {
		command = "nice -n 10 " + command
	}
	if state.Log != "" {
		command += " >> " + shellQuote(state.Log) + " 2>&1"
	}
	user := ""
	if state.CronFile != "" && state.Permission == PermissionSystem {
		if state.User == "" {
			return nil, errors.New("system crontab requires --user")
		}
		user = " " + state.User
	}
	begin, end := cronMarkers(state.Profile, state.Action)
	var b strings.Builder
	b.WriteString(begin + "\n")
	for _, expression := range state.Expressions {
		fmt.Fprintf(&b, "%s%s %s\n", expression, user, command)
	}
	if state.CatchUp {
		fmt.Fprintf(&b, "@reboot%s %s\n", user, command)
	}
	b.WriteString(end + "\n")
	return []byte(b.String()), nil
}

func (manager Manager) installCronFile(state State, executable, configDir string) error {
	if !filepath.IsAbs(state.CronFile) {
		return errors.New("explicit crontab path must be absolute")
	}
	current, err := os.ReadFile(state.CronFile)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("cannot read crontab file: %w", err)
	}
	updated, err := withoutCronJob(string(current), state.Profile, state.Action)
	if err != nil {
		return err
	}
	definition, err := manager.renderCron(state, executable, configDir)
	if err != nil {
		return err
	}
	if updated != "" && !strings.HasSuffix(updated, "\n") {
		updated += "\n"
	}
	if err := os.MkdirAll(filepath.Dir(state.CronFile), 0o700); err != nil {
		return err
	}
	return securefile.WriteAtomic(state.CronFile, append([]byte(updated), definition...))
}

func removeCronFile(path, name, action string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	updated, err := withoutCronJob(string(data), name, action)
	if err != nil {
		return err
	}
	return securefile.WriteAtomic(path, []byte(updated))
}
