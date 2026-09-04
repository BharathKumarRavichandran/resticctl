package schedule

import (
	"context"
	"fmt"
)

// backend isolates scheduler-specific rendering and lifecycle operations from
// the portable Manager API.
type backend interface {
	render(configDir string, state State, executable string) ([]byte, error)
	install(ctx context.Context, configDir string, state *State, executable string) error
	remove(ctx context.Context, configDir string, state *State) error
	definition(ctx context.Context, state State) ([]byte, error)
	verify(ctx context.Context, state State) error
}

func (manager Manager) backend(name string) (backend, error) {
	switch name {
	case BackendCron:
		return cronBackend{manager}, nil
	case BackendLaunchd:
		return launchdBackend{manager}, nil
	case BackendSystemd:
		return systemdBackend{manager}, nil
	case BackendWindows:
		return windowsBackend{manager}, nil
	default:
		return nil, fmt.Errorf("unsupported schedule backend %q", name)
	}
}

type cronBackend struct{ Manager }

func (b cronBackend) render(configDir string, state State, executable string) ([]byte, error) {
	return b.renderCron(state, executable, configDir)
}
func (b cronBackend) install(ctx context.Context, configDir string, state *State, executable string) error {
	return b.installCron(ctx, *state, executable, configDir)
}
func (b cronBackend) remove(ctx context.Context, _ string, state *State) error {
	if state.CronFile != "" {
		return removeCronFile(state.CronFile, state.Profile, state.Action)
	}
	return b.removeCron(ctx, state.Profile, state.Action)
}
func (b cronBackend) definition(ctx context.Context, state State) ([]byte, error) {
	return b.cronDefinition(ctx, state)
}
func (cronBackend) verify(context.Context, State) error { return nil }

type launchdBackend struct{ Manager }

func (b launchdBackend) render(configDir string, state State, executable string) ([]byte, error) {
	return b.renderLaunchd(configDir, state, executable)
}
func (b launchdBackend) install(ctx context.Context, configDir string, state *State, executable string) error {
	jobFile, err := b.installLaunchd(ctx, configDir, *state, executable)
	state.JobFile = jobFile
	return err
}
func (b launchdBackend) remove(ctx context.Context, configDir string, state *State) error {
	return b.removeLaunchd(ctx, configDir, *state)
}
func (b launchdBackend) definition(_ context.Context, state State) ([]byte, error) {
	return b.launchdDefinition(state)
}
func (b launchdBackend) verify(ctx context.Context, state State) error {
	return b.verifyLaunchd(ctx, state)
}

type systemdBackend struct{ Manager }

func (b systemdBackend) render(configDir string, state State, executable string) ([]byte, error) {
	service, timer, err := b.renderSystemd(configDir, state, executable)
	return append(service, timer...), err
}
func (b systemdBackend) install(ctx context.Context, configDir string, state *State, executable string) error {
	return b.installSystemd(ctx, configDir, state, executable)
}
func (b systemdBackend) remove(ctx context.Context, _ string, state *State) error {
	return b.removeSystemd(ctx, state)
}
func (b systemdBackend) definition(_ context.Context, state State) ([]byte, error) {
	return b.nativeDefinition(state)
}
func (b systemdBackend) verify(ctx context.Context, state State) error {
	return b.verifyNative(ctx, state)
}

type windowsBackend struct{ Manager }

func (b windowsBackend) render(configDir string, state State, executable string) ([]byte, error) {
	return b.renderWindows(configDir, state, executable)
}
func (b windowsBackend) install(ctx context.Context, configDir string, state *State, executable string) error {
	return b.installWindows(ctx, configDir, state, executable)
}
func (b windowsBackend) remove(ctx context.Context, _ string, state *State) error {
	return b.removeWindows(ctx, state)
}
func (b windowsBackend) definition(_ context.Context, state State) ([]byte, error) {
	return b.nativeDefinition(state)
}
func (b windowsBackend) verify(ctx context.Context, state State) error {
	return b.verifyNative(ctx, state)
}
