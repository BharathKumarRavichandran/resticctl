package profile

import (
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"time"
)

func validateRuntime(value *Profile, base string) error {
	policy := &value.Runtime
	if policy.Priority != "" && policy.Priority != "normal" && policy.Priority != "background" {
		return fmt.Errorf("runtime.priority must be normal or background")
	}
	if policy.Nice != nil && (*policy.Nice < -20 || *policy.Nice > 19) {
		return errors.New("runtime.nice must be between -20 and 19")
	}
	if policy.IOPriority != nil {
		class := policy.IOPriority.Class
		if class != "idle" && class != "best-effort" && class != "realtime" {
			return errors.New("runtime.ionice.class must be idle, best-effort, or realtime")
		}
		if policy.IOPriority.Level < 0 || policy.IOPriority.Level > 7 {
			return errors.New("runtime.ionice.level must be between 0 and 7")
		}
		if runtime.GOOS != "linux" {
			return errors.New("runtime.ionice is supported only on Linux")
		}
	}
	if policy.WindowsPriority != "" {
		switch policy.WindowsPriority {
		case "idle", "below-normal", "normal", "above-normal", "high":
		default:
			return errors.New("runtime.windows_priority is invalid")
		}
		if runtime.GOOS != "windows" {
			return errors.New("runtime.windows_priority is supported only on Windows")
		}
	}
	if lock := policy.Lock; lock != nil {
		if lock.Path == "" {
			return errors.New("runtime.lock.path must be set")
		}
		expanded, err := expandPath(lock.Path, base)
		if err != nil {
			return fmt.Errorf("invalid runtime.lock.path: %w", err)
		}
		lock.Path = filepath.Clean(expanded)
		if lock.Mode == "" {
			lock.Mode = "fail"
		}
		if lock.Mode != "fail" && lock.Mode != "wait" && lock.Mode != "ignore" {
			return errors.New("runtime.lock.mode must be fail, wait, or ignore")
		}
		if err := validatePositiveDuration("runtime.lock.wait", lock.Wait, lock.Mode == "wait"); err != nil {
			return err
		}
		if lock.Stale == "" {
			lock.Stale = "fail"
		}
		if lock.Stale != "fail" && lock.Stale != "clear" {
			return errors.New("runtime.lock.stale must be fail or clear")
		}
		if err := validatePositiveDuration("runtime.lock.stale_after", lock.StaleAfter, false); err != nil {
			return err
		}
		if lock.Stale == "clear" && lock.StaleAfter == "" {
			return errors.New("runtime.lock.stale_after must be set when stale is clear")
		}
	}
	if recovery := policy.RepositoryLockRecovery; recovery != nil {
		if !recovery.Enabled && recovery.DryRun {
			return errors.New("runtime.repository_lock_recovery.dry_run requires enabled")
		}
		if recovery.Enabled {
			if err := validatePositiveDuration("runtime.repository_lock_recovery.min_age", recovery.MinAge, true); err != nil {
				return err
			}
		}
	}
	return nil
}

func validatePositiveDuration(name, value string, required bool) error {
	if value == "" {
		if required {
			return fmt.Errorf("%s must be set", name)
		}
		return nil
	}
	duration, err := time.ParseDuration(value)
	if err != nil || duration <= 0 {
		return fmt.Errorf("%s must be a positive duration", name)
	}
	return nil
}
