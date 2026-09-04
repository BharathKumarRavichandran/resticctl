package schedule

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
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
	state, err := readState(path)
	if errors.Is(err, os.ErrNotExist) {
		return State{}, fmt.Errorf("%w for profile %s", ErrNotInstalled, name)
	}
	if err != nil {
		return State{}, err
	}
	if state.Profile != name {
		return State{}, fmt.Errorf("schedule state %s has profile %q, expected %q", path, state.Profile, name)
	}
	if state.Action != action {
		return State{}, fmt.Errorf("schedule state %s has action %q, expected %q", path, state.Action, action)
	}
	return state, nil
}

func readState(path string) (State, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return State{}, fmt.Errorf("cannot read schedule state %s: %w", path, err)
	}
	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return State{}, fmt.Errorf("cannot decode schedule state %s: %w", path, err)
	}
	var optional struct {
		Enabled *bool `json:"enabled"`
		Start   *bool `json:"start"`
	}
	if err := json.Unmarshal(data, &optional); err != nil {
		return State{}, fmt.Errorf("cannot decode schedule state %s: %w", path, err)
	}
	if optional.Enabled == nil {
		state.Enabled = true
	}
	if optional.Start == nil {
		state.Start = true
	}
	if state.Action == "" {
		state.Action = ActionBackup
	}
	if err := profile.ValidateName(state.Profile); err != nil {
		return State{}, fmt.Errorf("schedule state %s: %w", path, err)
	}
	if err := validateAction(state.Action); err != nil {
		return State{}, fmt.Errorf("schedule state %s: %w", path, err)
	}
	if !validBackend(state.Backend) {
		return State{}, fmt.Errorf("schedule state %s has unsupported backend %q", path, state.Backend)
	}
	if len(state.Expressions) == 0 {
		state.Expressions = []string{state.Expression}
	}
	for i, expression := range state.Expressions {
		normalized, err := cronexpr.Normalize(expression)
		if err != nil {
			return State{}, fmt.Errorf("schedule state %s has invalid expression %d: %w", path, i+1, err)
		}
		state.Expressions[i] = normalized
	}
	if normalized, err := cronexpr.Normalize(state.Expression); err != nil {
		return State{}, fmt.Errorf("schedule state %s has invalid expression: %w", path, err)
	} else if normalized != state.Expressions[0] {
		return State{}, fmt.Errorf("schedule state %s has inconsistent primary expression", path)
	} else {
		state.Expression = normalized
	}
	if state.Action != ActionForget && state.Prune {
		return State{}, fmt.Errorf("schedule state %s enables prune for a %s action", path, state.Action)
	}
	policy := Spec{
		Permission: state.Permission, CronFile: state.CronFile, User: state.User,
		Priority: state.Priority, Log: state.Log, LockMode: state.LockMode, LockWait: state.LockWait,
	}
	if err := policy.validatePolicy(); err != nil {
		return State{}, fmt.Errorf("schedule state %s: %w", path, err)
	}
	if state.Backend == BackendCron {
		if state.CronFile != "" && !filepath.IsAbs(state.CronFile) {
			return State{}, fmt.Errorf("schedule state %s has a relative crontab path", path)
		}
		state.JobFile = ""
	} else if (state.Backend == BackendSystemd || state.Backend == BackendWindows) && !filepath.IsAbs(state.JobFile) {
		return State{}, fmt.Errorf("schedule state %s has an invalid job file", path)
	}
	if state.Installed.IsZero() {
		return State{}, fmt.Errorf("schedule state %s has no installation time", path)
	}
	return state, nil
}

// List returns recorded schedules, optionally restricted to one profile.
func List(configDir, profileName string) ([]State, error) {
	if profileName != "" {
		if err := profile.ValidateName(profileName); err != nil {
			return nil, err
		}
	}
	directory := filepath.Join(configDir, "schedules")
	entries, err := os.ReadDir(directory)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("cannot list schedule state in %s: %w", directory, err)
	}
	var states []State
	for _, entry := range entries {
		if !entry.Type().IsRegular() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		state, err := readState(filepath.Join(directory, entry.Name()))
		if err != nil {
			return nil, err
		}
		if profileName != "" && state.Profile != profileName {
			continue
		}
		if filepath.Base(statePath(configDir, state.Profile, state.Action)) != entry.Name() {
			return nil, fmt.Errorf("schedule state filename %s does not match its profile and action", entry.Name())
		}
		states = append(states, state)
	}
	sort.Slice(states, func(i, j int) bool {
		if states[i].Profile == states[j].Profile {
			return states[i].Action < states[j].Action
		}
		return states[i].Profile < states[j].Profile
	})
	return states, nil
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
	if err := securefile.Protect(directory); err != nil {
		return fmt.Errorf("cannot protect schedule directory: %w", err)
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("cannot encode schedule state: %w", err)
	}
	data = append(data, '\n')
	return securefile.WriteAtomic(statePath(configDir, state.Profile, state.Action), data)
}
