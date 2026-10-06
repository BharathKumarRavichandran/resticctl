package schedule

import (
	"context"
	"os"
	"path/filepath"

	"resticctl/internal/configlock"
	"resticctl/internal/securefile"
)

// Scheduler mutations include state writes, verification, and rollback. The real
// executor shares one user lock across configuration directories and profiles.
func (manager Manager) withMutationLock(ctx context.Context, configDir string, update func() error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	directory, err := filepath.Abs(configDir)
	if err != nil {
		return err
	}
	if _, real := manager.executor.(OSExecutor); real {
		cache, err := os.UserCacheDir()
		if err != nil {
			return err
		}
		directory = filepath.Join(cache, "resticctl")
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		return err
	}
	// Do not change permissions on a user-supplied configuration directory.
	if _, real := manager.executor.(OSExecutor); real {
		if err := securefile.Protect(directory); err != nil {
			return err
		}
	}
	return configlock.With(filepath.Join(directory, ".scheduler.lock"), func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		return update()
	})
}
