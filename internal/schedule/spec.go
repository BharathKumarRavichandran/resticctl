package schedule

import (
	"errors"
	"strings"
	"time"
)

const (
	PermissionUser     = "user"
	PermissionLoggedOn = "logged-on-user"
	PermissionSystem   = "system"
	PriorityNormal     = "normal"
	PriorityBackground = "background"
	LockFail           = "fail"
	LockWait           = "wait"
)

// Spec describes a portable scheduled action. Expressions use normalized
// five-field cron syntax; backends may reject constructs they cannot preserve.
type Spec struct {
	Name        string
	Action      string
	Backend     string
	Executable  string
	ConfigDir   string
	Expressions []string
	CatchUp     bool
	Prune       bool
	Enabled     bool
	Start       bool
	Permission  string
	CronFile    string
	User        string
	Priority    string
	Log         string
	LockMode    string
	LockWait    string
	Network     bool
	ACPower     bool
	DryRun      bool
}

func (spec Spec) validatePolicy() error {
	for _, value := range []string{spec.CronFile, spec.User, spec.Log} {
		if strings.ContainsAny(value, "\r\n\x00") {
			return errors.New("schedule path or user contains an invalid character")
		}
	}
	if spec.Permission != "" && spec.Permission != PermissionUser && spec.Permission != PermissionLoggedOn && spec.Permission != PermissionSystem {
		return valueError("permission", spec.Permission)
	}
	if spec.Priority != "" && spec.Priority != PriorityNormal && spec.Priority != PriorityBackground {
		return valueError("priority", spec.Priority)
	}
	if spec.LockMode != "" && spec.LockMode != LockFail && spec.LockMode != LockWait {
		return valueError("lock mode", spec.LockMode)
	}
	if spec.LockWait != "" {
		d, err := time.ParseDuration(spec.LockWait)
		if err != nil || d <= 0 {
			return valueError("lock wait", spec.LockWait)
		}
	}
	if spec.LockMode != LockWait && spec.LockWait != "" {
		return errors.New("lock wait requires lock mode wait")
	}
	return nil
}

type scheduleValueError struct{ field, value string }

func (e scheduleValueError) Error() string {
	return "unsupported schedule " + e.field + " " + quote(e.value)
}
func valueError(field, value string) error {
	return scheduleValueError{field: field, value: value}
}

func quote(value string) string {
	return `"` + value + `"`
}

// WithSystemdDirs overrides unit directories, primarily for fake installations.
func WithSystemdDirs(user, system string) Option {
	return func(manager *Manager) {
		manager.systemdUserDir = user
		manager.systemdSystemDir = system
	}
}
