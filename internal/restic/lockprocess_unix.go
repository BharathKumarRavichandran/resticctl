//go:build !windows

package restic

import (
	"errors"
	"os"
	"syscall"
)

func processActive(pid int) (bool, error) {
	owner, err := os.FindProcess(pid)
	if err != nil {
		return false, err
	}
	err = owner.Signal(syscall.Signal(0))
	if err == nil || errors.Is(err, syscall.EPERM) {
		return true, nil
	}
	if errors.Is(err, syscall.ESRCH) {
		return false, nil
	}
	return false, err
}
