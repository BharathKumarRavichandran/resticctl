package schedule

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	managedaction "resticctl/internal/action"
	"resticctl/internal/cronexpr"
	"resticctl/internal/profile"
	"resticctl/internal/securefile"
)

var ErrNotInstalled = errors.New("schedule is not installed")

type State struct {
	Profile         string    `json:"profile"`
	TargetType      string    `json:"target_type,omitempty"`
	TargetName      string    `json:"target_name,omitempty"`
	Backend         string    `json:"backend"`
	Expression      string    `json:"expression"`
	Installed       time.Time `json:"installed_at"`
	JobFile         string    `json:"job_file,omitempty"`
	CatchUp         bool      `json:"catch_up"`
	Action          string    `json:"action,omitempty"`
	Prune           bool      `json:"prune,omitempty"`
	RegisteredHash  string    `json:"registered_definition_hash,omitempty"`
	DefinitionHash  string    `json:"definition_hash,omitempty"`
	Executable      string    `json:"executable,omitempty"`
	EnvironmentPath string    `json:"environment_path,omitempty"`
	Expressions     []string  `json:"expressions,omitempty"`
	Permission      string    `json:"permission,omitempty"`
	CronFile        string    `json:"cron_file,omitempty"`
	User            string    `json:"user,omitempty"`
	Priority        string    `json:"priority,omitempty"`
	Log             string    `json:"log,omitempty"`
	LockMode        string    `json:"lock_mode,omitempty"`
	LockWait        string    `json:"lock_wait,omitempty"`
	Enabled         bool      `json:"enabled"`
	Start           bool      `json:"start"`
	Network         bool      `json:"network,omitempty"`
	ACPower         bool      `json:"ac_power,omitempty"`
	Rendered        string    `json:"-"`
}

func Load(configDir, name string) (State, error) {
	return LoadAction(configDir, name, ActionBackup)
}

func LoadAction(configDir, name, action string) (State, error) {
	return LoadTargetAction(configDir, TargetProfile, name, action)
}

func LoadTargetAction(configDir, targetType, name, action string) (State, error) {
	if err := profile.ValidateName(name); err != nil {
		return State{}, err
	}
	if err := validateAction(action); err != nil {
		return State{}, err
	}
	key, err := targetKey(targetType, name)
	if err != nil {
		return State{}, err
	}
	path := statePath(configDir, key, action)
	state, err := readState(path)
	if errors.Is(err, os.ErrNotExist) {
		return State{}, fmt.Errorf("%w for profile %s", ErrNotInstalled, name)
	}
	if err != nil {
		return State{}, err
	}
	if state.Profile != name || state.TargetType != targetType {
		return State{}, fmt.Errorf("schedule state %s has a different target, expected %s %s", path, targetType, name)
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
	if state.TargetType == "" {
		state.TargetType = TargetProfile
		state.TargetName = state.Profile
	}
	if state.TargetType != TargetProfile && state.TargetType != TargetGroup {
		return State{}, fmt.Errorf("schedule state %s has invalid target type %q", path, state.TargetType)
	}
	if state.TargetName == "" {
		return State{}, fmt.Errorf("schedule state %s has no target name", path)
	}
	_, err = targetKey(state.TargetType, state.TargetName)
	if err != nil || state.TargetName != state.Profile {
		return State{}, fmt.Errorf("schedule state %s has inconsistent target identity", path)
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
	if !managedaction.Action(state.Action).Capabilities().Prune && state.Prune {
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
	if state.Executable != "" && !filepath.IsAbs(state.Executable) {
		return State{}, fmt.Errorf("schedule state %s has a relative executable", path)
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
	var states []State
	err := filepath.WalkDir(directory, func(path string, entry fs.DirEntry, walkErr error) error {
		if errors.Is(walkErr, os.ErrNotExist) && path == directory {
			return nil
		}
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(directory, path)
		if err != nil {
			return err
		}
		parts := strings.Split(relative, string(filepath.Separator))
		if entry.IsDir() {
			if path == directory {
				return nil
			}
			if (parts[0] != "profiles" && parts[0] != "groups") || len(parts) > 2 {
				return filepath.SkipDir
			}
			return nil
		}
		if len(parts) != 3 || !entry.Type().IsRegular() || filepath.Ext(path) != ".json" {
			return nil
		}
		state, err := readState(path)
		if err != nil {
			return err
		}
		if profileName != "" && state.Profile != profileName {
			return nil
		}
		action := managedaction.Action(state.Action)
		identity := targetIdentity(state)
		key := action.StateKey(identity)
		if relative != key+".json" {
			return fmt.Errorf("schedule state filename %s does not match its profile and action", relative)
		}
		states = append(states, state)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("cannot list schedule state in %s: %w", directory, err)
	}
	sort.Slice(states, func(i, j int) bool {
		if states[i].Profile == states[j].Profile {
			if states[i].Action == states[j].Action {
				return states[i].TargetType < states[j].TargetType
			}
			return states[i].Action < states[j].Action
		}
		return states[i].Profile < states[j].Profile
	})
	return states, nil
}

func statePath(configDir, name, action string) string {
	return filepath.Join(configDir, "schedules", managedaction.Action(action).StateKey(name)+".json")
}

func targetKey(targetType, name string) (string, error) {
	if targetType == "" || targetType == TargetProfile {
		return name, nil
	}
	if targetType == TargetGroup {
		return "group+" + name, nil
	}
	return "", fmt.Errorf("unsupported schedule target type %q", targetType)
}

func removeState(configDir, name, action string) error {
	if err := os.Remove(statePath(configDir, name, action)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("cannot remove schedule state: %w", err)
	}
	return nil
}

func writeState(configDir string, state State) error {
	directory := filepath.Dir(statePath(configDir, targetIdentity(state), state.Action))
	if err := securefile.MakePrivateDir(filepath.Join(configDir, "schedules"), directory); err != nil {
		return fmt.Errorf("cannot create private schedule directory: %w", err)
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("cannot encode schedule state: %w", err)
	}
	data = append(data, '\n')
	return securefile.WriteAtomic(statePath(configDir, targetIdentity(state), state.Action), data)
}

func targetIdentity(state State) string {
	targetName := state.TargetName
	if targetName == "" {
		targetName = state.Profile
	}
	key, _ := targetKey(state.TargetType, targetName)
	return key
}
