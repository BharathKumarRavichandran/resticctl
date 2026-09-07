//go:build !windows

package process

import (
	"fmt"
	"os/exec"
	"runtime"
	"strconv"
)

func applyPriority(command *exec.Cmd, configured any) error {
	priority, ok := configured.(Priority)
	if !ok {
		return nil
	}
	nice := priority.Nice
	if nice == nil && priority.Portable == "background" {
		value := 10
		nice = &value
	}
	if nice != nil {
		path, err := exec.LookPath("nice")
		if err != nil {
			return fmt.Errorf("process priority requires nice: %w", err)
		}
		wrap(command, path, "-n", strconv.Itoa(*nice))
	}
	ioPriority := priority.IO
	if ioPriority == nil {
		return nil
	}
	if runtime.GOOS != "linux" {
		return fmt.Errorf("I/O priority is unsupported on %s", runtime.GOOS)
	}
	path, err := exec.LookPath("ionice")
	if err != nil {
		return fmt.Errorf("I/O priority requires ionice: %w", err)
	}
	class := map[string]string{"realtime": "1", "best-effort": "2", "idle": "3"}[ioPriority.Class]
	arguments := []string{"-c", class}
	if ioPriority.Class != "idle" {
		arguments = append(arguments, "-n", strconv.Itoa(ioPriority.Level))
	}
	wrap(command, path, arguments...)
	return nil
}

func wrap(command *exec.Cmd, executable string, arguments ...string) {
	original := append([]string{command.Path}, command.Args[1:]...)
	command.Path = executable
	command.Args = append([]string{executable}, append(arguments, original...)...)
}
