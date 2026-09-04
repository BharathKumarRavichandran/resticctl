//go:build !windows

package process

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

type processTree struct{ pid int }

func prepareTree(command *exec.Cmd) error {
	attributes := syscall.SysProcAttr{}
	if command.SysProcAttr != nil {
		attributes = *command.SysProcAttr
	}
	attributes.Setpgid = true
	command.SysProcAttr = &attributes
	return nil
}

func superviseTree(process *os.Process) (processTree, error) {
	return processTree{pid: process.Pid}, nil
}

func (tree processTree) kill() error {
	err := syscall.Kill(-tree.pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}

func (processTree) close() error { return nil }
