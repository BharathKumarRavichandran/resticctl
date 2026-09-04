package schedule

import (
	"bytes"
	"context"
	"crypto/sha256"
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
	BackendSystemd = "systemd"
	BackendWindows = "windows"
	ActionBackup   = "backup"
	ActionForget   = "forget"
	ActionCheck    = "check"
	ActionPrune    = "prune"
	ActionCopy     = "copy"
)

var ErrDrift = errors.New("installed schedule differs from recorded state")

var errDefinitionDrift = errors.New("installed schedule definition is missing or invalid")

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
	executor         Executor
	goos             string
	uid              int
	environmentPath  string
	launchAgentsDir  string
	systemdUserDir   string
	systemdSystemDir string
	now              func() time.Time
}

// Option customizes a Manager constructed by NewManager.
type Option func(*Manager)

// WithExecutor replaces the operating-system command executor.
func WithExecutor(executor Executor) Option {
	return func(manager *Manager) {
		if executor != nil {
			manager.executor = executor
		}
	}
}

// WithPlatform overrides the operating system and user ID used by scheduler backends.
func WithPlatform(goos string, uid int) Option {
	return func(manager *Manager) { manager.goos, manager.uid = goos, uid }
}

// WithEnvironmentPath sets the PATH recorded in installed jobs.
func WithEnvironmentPath(path string) Option {
	return func(manager *Manager) { manager.environmentPath = path }
}

// WithLaunchAgentsDir sets the macOS LaunchAgents directory.
func WithLaunchAgentsDir(path string) Option {
	return func(manager *Manager) { manager.launchAgentsDir = path }
}

// WithClock replaces the clock used for installation timestamps.
func WithClock(now func() time.Time) Option {
	return func(manager *Manager) {
		if now != nil {
			manager.now = now
		}
	}
}

// NewManager constructs a scheduler manager for the current process.
func NewManager(options ...Option) Manager {
	home, _ := os.UserHomeDir()
	manager := Manager{
		executor: OSExecutor{}, goos: runtime.GOOS, uid: platformUID(),
		environmentPath: os.Getenv("PATH"), launchAgentsDir: filepath.Join(home, "Library", "LaunchAgents"),
		systemdUserDir: filepath.Join(home, ".config", "systemd", "user"), systemdSystemDir: "/etc/systemd/system",
		now: time.Now,
	}
	for _, option := range options {
		option(&manager)
	}
	return manager
}

func (manager Manager) Install(ctx context.Context, configDir, name, expression, backend, executable string, catchUp bool) (State, error) {
	return manager.InstallAction(ctx, configDir, name, ActionBackup, expression, backend, executable, catchUp, false)
}

func (manager Manager) InstallAction(ctx context.Context, configDir, name, action, expression, backend, executable string, catchUp, prune bool) (State, error) {
	return manager.InstallSpec(ctx, Spec{Name: name, Action: action, Expressions: []string{expression}, Backend: backend, Executable: executable, ConfigDir: configDir, CatchUp: catchUp, Prune: prune, Permission: PermissionUser, Enabled: true, Start: true})
}

// InstallSpec validates, renders, and installs one independently managed action.
func (manager Manager) InstallSpec(ctx context.Context, spec Spec) (State, error) {
	var err error
	name, action, backend, executable, configDir := spec.Name, spec.Action, spec.Backend, spec.Executable, spec.ConfigDir
	catchUp, prune := spec.CatchUp, spec.Prune
	if err := spec.validatePolicy(); err != nil {
		return State{}, err
	}
	if err := profile.ValidateName(name); err != nil {
		return State{}, err
	}
	if err := validateAction(action); err != nil {
		return State{}, err
	}
	if action != ActionForget && prune {
		return State{}, errors.New("prune is only valid for a forget schedule")
	}
	if len(spec.Expressions) == 0 {
		return State{}, errors.New("at least one calendar expression is required")
	}
	normalizedExpressions := make([]string, len(spec.Expressions))
	for i, expression := range spec.Expressions {
		normalizedExpressions[i], err = cronexpr.Normalize(expression)
		if err != nil {
			return State{}, fmt.Errorf("calendar expression %d: %w", i+1, err)
		}
	}
	normalized := normalizedExpressions[0]
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
	if !spec.DryRun {
		if installed, loadErr := LoadAction(configDir, name, action); loadErr == nil {
			existing = &installed
		} else if loadErr != nil && !errors.Is(loadErr, ErrNotInstalled) {
			return State{}, loadErr
		}
	}
	state := State{
		Profile: name, Backend: backend, Expression: normalized,
		Installed: manager.now().UTC(), CatchUp: catchUp, Action: action, Prune: prune,
		Expressions: normalizedExpressions, Permission: defaultString(spec.Permission, PermissionUser), CronFile: spec.CronFile,
		User: spec.User, Priority: spec.Priority, Log: spec.Log, LockMode: spec.LockMode, LockWait: spec.LockWait,
		Enabled: spec.Enabled, Start: spec.Start, Network: spec.Network, ACPower: spec.ACPower,
	}
	if backend == BackendLaunchd {
		state.JobFile, err = manager.launchdJobPath(name, action)
		if err != nil {
			return State{}, err
		}
	} else if backend == BackendSystemd {
		state.JobFile = filepath.Join(manager.systemdDir(state), nativeID(state)+".timer")
	} else if backend == BackendWindows {
		state.JobFile = filepath.Join(configDir, "schedules", nativeID(state)+".xml")
	}
	if spec.DryRun {
		definition, renderErr := manager.render(configDir, state, executable)
		state.Rendered = string(definition)
		return state, renderErr
	}
	switchedBackend := existing != nil && existing.Backend != state.Backend
	if switchedBackend {
		if err := manager.removeApplied(ctx, configDir, *existing); err != nil {
			return State{}, fmt.Errorf("cannot remove previous %s schedule: %w", existing.Backend, err)
		}
	}
	rollbackCtx := context.WithoutCancel(ctx)
	rollback := func(cause error) error {
		if existing == nil {
			return errors.Join(cause, manager.removeApplied(rollbackCtx, configDir, state), removeState(configDir, name, action))
		}
		if switchedBackend {
			cause = errors.Join(cause, manager.removeApplied(rollbackCtx, configDir, state))
		}
		return errors.Join(cause, manager.restore(rollbackCtx, configDir, *existing, executable), writeState(configDir, *existing))
	}
	if err := writeState(configDir, state); err != nil {
		if switchedBackend {
			err = errors.Join(err, manager.restore(rollbackCtx, configDir, *existing, executable))
		}
		return State{}, err
	}
	err = manager.apply(ctx, configDir, &state, executable)
	if err != nil {
		return State{}, rollback(err)
	}
	definition, err := manager.installedDefinition(ctx, state)
	if err != nil {
		return State{}, rollback(err)
	}
	state.DefinitionHash = definitionHash(definition)
	if err := writeState(configDir, state); err != nil {
		return State{}, rollback(err)
	}
	return state, nil
}

func (manager Manager) apply(ctx context.Context, configDir string, state *State, executable string) error {
	backend, err := manager.backend(state.Backend)
	if err != nil {
		return err
	}
	return backend.install(ctx, configDir, state, executable)
}

func (manager Manager) restore(ctx context.Context, configDir string, state State, executable string) error {
	if err := manager.apply(ctx, configDir, &state, executable); err != nil {
		return fmt.Errorf("cannot restore previous schedule: %w", err)
	}
	return nil
}

func (manager Manager) removeApplied(ctx context.Context, configDir string, state State) error {
	backend, err := manager.backend(state.Backend)
	if err != nil {
		return err
	}
	return backend.remove(ctx, configDir, &state)
}

func (manager Manager) Remove(ctx context.Context, configDir, name string) error {
	return manager.RemoveAction(ctx, configDir, name, ActionBackup)
}

func (manager Manager) RemoveAction(ctx context.Context, configDir, name, action string) error {
	state, err := LoadAction(configDir, name, action)
	if err != nil {
		return err
	}
	backend, err := manager.backend(state.Backend)
	if err != nil {
		return err
	}
	err = backend.remove(ctx, configDir, &state)
	if err != nil {
		return err
	}
	if err := removeState(configDir, name, action); err != nil {
		return err
	}
	return nil
}

// Verify checks that the scheduler still contains the job described by state.
func (manager Manager) Verify(ctx context.Context, state State) error {
	definition, err := manager.installedDefinition(ctx, state)
	if err != nil {
		if errors.Is(err, errDefinitionDrift) {
			return fmt.Errorf("%w: %v", ErrDrift, err)
		}
		return err
	}
	if state.DefinitionHash != "" && definitionHash(definition) != state.DefinitionHash {
		return fmt.Errorf("%w: scheduler job content changed", ErrDrift)
	}
	backend, err := manager.backend(state.Backend)
	if err != nil {
		return err
	}
	return backend.verify(ctx, state)
}

func (manager Manager) installedDefinition(ctx context.Context, state State) ([]byte, error) {
	backend, err := manager.backend(state.Backend)
	if err != nil {
		return nil, err
	}
	return backend.definition(ctx, state)
}

func definitionHash(definition []byte) string {
	return fmt.Sprintf("%x", sha256.Sum256(definition))
}

func (manager Manager) resolveBackend(backend string) (string, error) {
	if backend == "" || backend == BackendAuto {
		if manager.goos == "darwin" {
			return BackendLaunchd, nil
		}
		if manager.goos == "windows" {
			return BackendWindows, nil
		}
		return BackendCron, nil
	}
	if !validBackend(backend) {
		return "", fmt.Errorf("unsupported schedule backend %q", backend)
	}
	if backend == BackendLaunchd && manager.goos != "darwin" {
		return "", errors.New("launchd scheduling is only available on macOS")
	}
	if backend == BackendCron && manager.goos == "windows" {
		return "", errors.New("cron scheduling is not available on Windows")
	}
	if backend == BackendSystemd && manager.goos != "linux" {
		return "", errors.New("systemd scheduling is only available on Linux")
	}
	if backend == BackendWindows && manager.goos != "windows" {
		return "", errors.New("Windows Task Scheduler is only available on Windows")
	}
	return backend, nil
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func (manager Manager) jobArguments(executable, configDir string, state State) []string {
	arguments := scheduledArguments(executable, configDir, state)
	if manager.environmentPath != "" {
		arguments = append([]string{"/usr/bin/env", "PATH=" + manager.environmentPath}, arguments...)
	}
	return arguments
}

func scheduledArguments(executable, configDir string, state State) []string {
	return []string{executable, "--config-dir", configDir, "schedule", "run", state.Profile, "--action", state.Action}
}

func validateAction(action string) error {
	if action != ActionBackup && action != ActionForget && action != ActionCheck && action != ActionPrune && action != ActionCopy {
		return fmt.Errorf("unsupported schedule action %q", action)
	}
	return nil
}

func validBackend(value string) bool {
	return value == BackendCron || value == BackendLaunchd || value == BackendSystemd || value == BackendWindows
}
func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
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
