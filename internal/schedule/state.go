package schedule

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"resticctl/internal/cronexpr"
	"resticctl/internal/profile"
	"resticctl/internal/securefile"
)

var ErrNotInstalled = errors.New("schedule is not installed")

type State struct {
	Profile        string    `json:"profile"`
	Backend        string    `json:"backend"`
	Expression     string    `json:"expression"`
	Installed      time.Time `json:"installed_at"`
	JobFile        string    `json:"job_file,omitempty"`
	CatchUp        bool      `json:"catch_up"`
	Action         string    `json:"action,omitempty"`
	Prune          bool      `json:"prune,omitempty"`
	DefinitionHash string    `json:"definition_hash,omitempty"`
	Expressions    []string  `json:"expressions,omitempty"`
	Permission     string    `json:"permission,omitempty"`
	CronFile       string    `json:"cron_file,omitempty"`
	User           string    `json:"user,omitempty"`
	Priority       string    `json:"priority,omitempty"`
	Log            string    `json:"log,omitempty"`
	LockMode       string    `json:"lock_mode,omitempty"`
	LockWait       string    `json:"lock_wait,omitempty"`
	Enabled        bool      `json:"enabled"`
	Start          bool      `json:"start"`
	Network        bool      `json:"network,omitempty"`
	ACPower        bool      `json:"ac_power,omitempty"`
	Rendered       string    `json:"-"`
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
	if !validBackend(state.Backend) {
		return State{}, fmt.Errorf("schedule state %s has unsupported backend %q", path, state.Backend)
	}
	if _, err := cronexpr.Normalize(state.Expression); err != nil {
		return State{}, fmt.Errorf("schedule state %s has invalid expression: %w", path, err)
	}
	if len(state.Expressions) == 0 {
		state.Expressions = []string{state.Expression}
	}
	if state.Installed.IsZero() {
		return State{}, fmt.Errorf("schedule state %s has no installation time", path)
	}
	if state.Backend == BackendCron {
		state.JobFile = ""
	}
	return state, nil
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
	return securefile.WriteAtomic(statePath(configDir, state.Profile, state.Action), data)
}
