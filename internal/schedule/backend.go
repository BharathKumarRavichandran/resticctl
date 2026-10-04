package schedule

import (
	"context"
	"fmt"
)

func (manager Manager) render(configDir string, state State, executable string) ([]byte, error) {
	switch state.Backend {
	case BackendCron:
		return manager.renderCron(state, executable, configDir)
	case BackendLaunchd:
		return manager.renderLaunchd(configDir, state, executable)
	case BackendSystemd:
		service, timer, err := manager.renderSystemd(configDir, state, executable)
		return append(service, timer...), err
	case BackendWindows:
		return manager.renderWindows(configDir, state, executable)
	default:
		return nil, unsupportedBackend(state.Backend)
	}
}

func (manager Manager) apply(ctx context.Context, configDir string, state *State, executable string) error {
	switch state.Backend {
	case BackendCron:
		return manager.installCron(ctx, *state, executable, configDir)
	case BackendLaunchd:
		jobFile, err := manager.installLaunchd(ctx, configDir, *state, executable)
		state.JobFile = jobFile
		return err
	case BackendSystemd:
		return manager.installSystemd(ctx, configDir, state, executable)
	case BackendWindows:
		return manager.installWindows(ctx, configDir, state, executable)
	default:
		return unsupportedBackend(state.Backend)
	}
}

func (manager Manager) removeApplied(ctx context.Context, configDir string, state State) error {
	switch state.Backend {
	case BackendCron:
		if state.CronFile != "" {
			return removeCronFile(state.CronFile, targetIdentity(state), state.Action)
		}
		return manager.removeCron(ctx, targetIdentity(state), state.Action)
	case BackendLaunchd:
		return manager.removeLaunchd(ctx, configDir, state)
	case BackendSystemd:
		return manager.removeSystemd(ctx, &state)
	case BackendWindows:
		return manager.removeWindows(ctx, &state)
	default:
		return unsupportedBackend(state.Backend)
	}
}

func (manager Manager) installedDefinition(ctx context.Context, state State) ([]byte, error) {
	switch state.Backend {
	case BackendCron:
		return manager.cronDefinition(ctx, state)
	case BackendLaunchd:
		return manager.launchdDefinition(state)
	case BackendSystemd, BackendWindows:
		return manager.nativeDefinition(state)
	default:
		return nil, unsupportedBackend(state.Backend)
	}
}

func (manager Manager) verifyBackend(ctx context.Context, state State) error {
	switch state.Backend {
	case BackendCron:
		return nil
	case BackendLaunchd:
		return manager.verifyLaunchd(ctx, state)
	case BackendSystemd, BackendWindows:
		return manager.verifyNative(ctx, state)
	default:
		return unsupportedBackend(state.Backend)
	}
}

func unsupportedBackend(name string) error {
	return fmt.Errorf("unsupported schedule backend %q", name)
}
