package schedule

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"resticctl/internal/cronexpr"
	"resticctl/internal/profile"
)

const (
	BackendAuto    = "auto"
	BackendCron    = "cron"
	BackendLaunchd = "launchd"
	ActionBackup   = "backup"
	ActionForget   = "forget"
)

var ErrNotInstalled = errors.New("schedule is not installed")

type State struct {
	Profile    string    `json:"profile"`
	Backend    string    `json:"backend"`
	Expression string    `json:"expression"`
	Installed  time.Time `json:"installed_at"`
	JobFile    string    `json:"job_file,omitempty"`
	CatchUp    bool      `json:"catch_up"`
	Action     string    `json:"action,omitempty"`
	Prune      bool      `json:"prune,omitempty"`
}

type Executor interface {
	Run(context.Context, []byte, string, ...string) ([]byte, error)
}

type OSExecutor struct{}

func (OSExecutor) Run(ctx context.Context, input []byte, name string, arguments ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, name, arguments...)
	command.Stdin = bytes.NewReader(input)
	return command.CombinedOutput()
}

type Manager struct {
	Executor        Executor
	GOOS            string
	UID             int
	EnvironmentPath string
	LaunchAgentsDir string
	Now             func() time.Time
}

func NewManager() Manager {
	home, _ := os.UserHomeDir()
	return Manager{
		Executor: OSExecutor{}, GOOS: runtime.GOOS, UID: platformUID(),
		EnvironmentPath: os.Getenv("PATH"), LaunchAgentsDir: filepath.Join(home, "Library", "LaunchAgents"),
		Now: time.Now,
	}
}

func (manager Manager) Install(ctx context.Context, configDir, name, expression, backend, executable string, catchUp bool) (State, error) {
	return manager.InstallAction(ctx, configDir, name, ActionBackup, expression, backend, executable, catchUp, false)
}

func (manager Manager) InstallAction(ctx context.Context, configDir, name, action, expression, backend, executable string, catchUp, prune bool) (State, error) {
	if err := profile.ValidateName(name); err != nil {
		return State{}, err
	}
	if err := validateAction(action); err != nil {
		return State{}, err
	}
	if action != ActionForget && prune {
		return State{}, errors.New("prune is only valid for a forget schedule")
	}
	normalized, err := cronexpr.Normalize(expression)
	if err != nil {
		return State{}, err
	}
	fields := strings.Fields(normalized)
	backend, err = manager.resolveBackend(backend)
	if err != nil {
		return State{}, err
	}
	executable, err = filepath.Abs(executable)
	if err != nil {
		return State{}, fmt.Errorf("cannot resolve resticctl executable: %w", err)
	}
	configDir, err = filepath.Abs(configDir)
	if err != nil {
		return State{}, fmt.Errorf("cannot resolve configuration directory: %w", err)
	}
	var existing *State
	if installed, loadErr := LoadAction(configDir, name, action); loadErr == nil && installed.Backend != backend {
		return State{}, fmt.Errorf("profile already has a %s schedule; remove it before switching backends", installed.Backend)
	} else if loadErr == nil {
		existing = &installed
	} else if loadErr != nil && !errors.Is(loadErr, ErrNotInstalled) {
		return State{}, loadErr
	}
	state := State{
		Profile: name, Backend: backend, Expression: normalized,
		Installed: manager.Now().UTC(), CatchUp: catchUp, Action: action, Prune: prune,
	}
	if backend == BackendLaunchd {
		state.JobFile, err = manager.launchdJobPath(name, action)
		if err != nil {
			return State{}, err
		}
	}
	if err := writeState(configDir, state); err != nil {
		return State{}, err
	}
	err = manager.apply(ctx, configDir, &state, fields, executable)
	if err != nil {
		if existing != nil {
			err = errors.Join(err, manager.restore(ctx, configDir, *existing, executable), writeState(configDir, *existing))
		} else {
			err = errors.Join(err, removeState(configDir, name, action))
		}
		return State{}, err
	}
	return state, nil
}

func (manager Manager) apply(ctx context.Context, configDir string, state *State, fields []string, executable string) error {
	if state.Backend == BackendCron {
		return manager.installCron(ctx, *state, executable, configDir)
	}
	jobFile, err := manager.installLaunchd(ctx, configDir, *state, fields, executable)
	state.JobFile = jobFile
	return err
}

func (manager Manager) restore(ctx context.Context, configDir string, state State, executable string) error {
	fields, err := cronexpr.Fields(state.Expression)
	if err != nil {
		return fmt.Errorf("cannot restore previous schedule: %w", err)
	}
	if err := manager.apply(ctx, configDir, &state, fields, executable); err != nil {
		return fmt.Errorf("cannot restore previous schedule: %w", err)
	}
	return nil
}

func (manager Manager) Remove(ctx context.Context, configDir, name string) error {
	return manager.RemoveAction(ctx, configDir, name, ActionBackup)
}

func (manager Manager) RemoveAction(ctx context.Context, configDir, name, action string) error {
	state, err := LoadAction(configDir, name, action)
	if err != nil {
		return err
	}
	switch state.Backend {
	case BackendCron:
		err = manager.removeCron(ctx, name, action)
	case BackendLaunchd:
		err = manager.removeLaunchd(ctx, configDir, state)
	default:
		err = fmt.Errorf("unsupported recorded schedule backend %q", state.Backend)
	}
	if err != nil {
		return err
	}
	if err := removeState(configDir, name, action); err != nil {
		return err
	}
	return nil
}

func Load(configDir, name string) (State, error) {
	return LoadAction(configDir, name, ActionBackup)
}

func LoadAction(configDir, name, action string) (State, error) {
	if err := profile.ValidateName(name); err != nil {
		return State{}, err
	}
	if err := validateAction(action); err != nil {
		return State{}, err
	}
	path := statePath(configDir, name, action)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return State{}, fmt.Errorf("%w for profile %s", ErrNotInstalled, name)
	}
	if err != nil {
		return State{}, fmt.Errorf("cannot read schedule state %s: %w", path, err)
	}
	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return State{}, fmt.Errorf("cannot decode schedule state %s: %w", path, err)
	}
	if state.Profile != name {
		return State{}, fmt.Errorf("schedule state %s has profile %q, expected %q", path, state.Profile, name)
	}
	if state.Action == "" {
		state.Action = ActionBackup
	}
	if state.Action != action {
		return State{}, fmt.Errorf("schedule state %s has action %q, expected %q", path, state.Action, action)
	}
	if state.Backend != BackendCron && state.Backend != BackendLaunchd {
		return State{}, fmt.Errorf("schedule state %s has unsupported backend %q", path, state.Backend)
	}
	if _, err := cronexpr.Normalize(state.Expression); err != nil {
		return State{}, fmt.Errorf("schedule state %s has invalid expression: %w", path, err)
	}
	if state.Installed.IsZero() {
		return State{}, fmt.Errorf("schedule state %s has no installation time", path)
	}
	if state.Backend == BackendCron {
		state.JobFile = ""
	}
	return state, nil
}

func (manager Manager) resolveBackend(backend string) (string, error) {
	if backend == "" || backend == BackendAuto {
		if manager.GOOS == "darwin" {
			return BackendLaunchd, nil
		}
		if manager.GOOS == "windows" {
			return "", errors.New("automatic scheduling is not supported on Windows")
		}
		return BackendCron, nil
	}
	if backend != BackendCron && backend != BackendLaunchd {
		return "", fmt.Errorf("unsupported schedule backend %q", backend)
	}
	if backend == BackendLaunchd && manager.GOOS != "darwin" {
		return "", errors.New("launchd scheduling is only available on macOS")
	}
	if backend == BackendCron && manager.GOOS == "windows" {
		return "", errors.New("cron scheduling is not available on Windows")
	}
	return backend, nil
}

func statePath(configDir, name, action string) string {
	filename := name + ".json"
	if action != ActionBackup {
		filename = name + "." + action + ".json"
	}
	return filepath.Join(configDir, "schedules", filename)
}

func removeState(configDir, name, action string) error {
	if err := os.Remove(statePath(configDir, name, action)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("cannot remove schedule state: %w", err)
	}
	return nil
}

func writeState(configDir string, state State) error {
	directory := filepath.Join(configDir, "schedules")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("cannot create schedule directory: %w", err)
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return fmt.Errorf("cannot protect schedule directory: %w", err)
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("cannot encode schedule state: %w", err)
	}
	data = append(data, '\n')
	path := statePath(configDir, state.Profile, state.Action)
	return writePrivateAtomic(path, data)
}

func writePrivateAtomic(path string, data []byte) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".resticctl-*")
	if err != nil {
		return fmt.Errorf("cannot create temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	ok := false
	defer func() {
		_ = temporary.Close()
		if !ok {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return fmt.Errorf("cannot protect temporary file: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		return fmt.Errorf("cannot write temporary file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("cannot close temporary file: %w", err)
	}
	if err := replaceFile(temporaryPath, path); err != nil {
		return fmt.Errorf("cannot replace %s: %w", path, err)
	}
	ok = true
	return nil
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func (manager Manager) jobArguments(executable, configDir string, state State) []string {
	arguments := scheduledArguments(executable, configDir, state)
	if manager.EnvironmentPath != "" {
		arguments = append([]string{"/usr/bin/env", "PATH=" + manager.EnvironmentPath}, arguments...)
	}
	return arguments
}

func scheduledArguments(executable, configDir string, state State) []string {
	arguments := []string{executable, "--config-dir", configDir}
	if state.CatchUp {
		arguments = append(arguments, "schedule", "run", state.Profile, "--action", state.Action)
	} else if state.Action == ActionForget {
		arguments = append(arguments, "forget", state.Profile)
		if state.Prune {
			arguments = append(arguments, "--prune")
		}
	} else {
		arguments = append(arguments, "backup", state.Profile)
	}
	return arguments
}

func validateAction(action string) error {
	if action != ActionBackup && action != ActionForget {
		return fmt.Errorf("unsupported schedule action %q", action)
	}
	return nil
}

func launchdCalendar(fields []string) (string, error) {
	keys := []string{"Minute", "Hour", "Day", "Month", "Weekday"}
	minimums := []int{0, 0, 1, 1, 0}
	maximums := []int{59, 23, 31, 12, 7}
	var output strings.Builder
	output.WriteString("<dict>")
	for index, field := range fields {
		if field == "*" {
			continue
		}
		value, err := strconv.Atoi(field)
		if err != nil {
			return "", fmt.Errorf("launchd supports only a number or * in cron field %q", field)
		}
		if value < minimums[index] || value > maximums[index] {
			return "", fmt.Errorf("launchd cron field %q is out of range", field)
		}
		fmt.Fprintf(&output, "<key>%s</key><integer>%d</integer>", keys[index], value)
	}
	output.WriteString("</dict>")
	return output.String(), nil
}

func xmlText(value string) string {
	var output bytes.Buffer
	_ = xml.EscapeText(&output, []byte(value))
	return output.String()
}
