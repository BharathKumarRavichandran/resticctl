//go:build darwin

package hostpolicy

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

type systemProbe struct{}

func (systemProbe) AvailableMemory() (uint64, error) {
	output, err := exec.Command("vm_stat").Output()
	if err != nil {
		return 0, err
	}
	var pageSize uint64 = 4096
	var pages uint64
	for _, line := range strings.Split(string(output), "\n") {
		if strings.Contains(line, "page size of") {
			fields := strings.Fields(line)
			for i, field := range fields {
				if field == "of" && i+1 < len(fields) {
					pageSize, _ = strconv.ParseUint(fields[i+1], 10, 64)
				}
			}
		}
		if strings.HasPrefix(line, "Pages free:") || strings.HasPrefix(line, "Pages inactive:") || strings.HasPrefix(line, "Pages speculative:") {
			value := strings.TrimSuffix(strings.TrimSpace(strings.SplitN(line, ":", 2)[1]), ".")
			count, parseErr := strconv.ParseUint(value, 10, 64)
			if parseErr != nil {
				return 0, parseErr
			}
			pages += count
		}
	}
	if pages == 0 {
		return 0, errors.New("vm_stat did not report available pages")
	}
	return pages * pageSize, nil
}

func (systemProbe) OnACPower() (bool, error) {
	output, err := exec.Command("pmset", "-g", "batt").Output()
	if err != nil {
		return false, err
	}
	text := string(output)
	if strings.Contains(text, "AC Power") {
		return true, nil
	}
	if strings.Contains(text, "Battery Power") {
		return false, nil
	}
	return false, errors.New("pmset returned an unknown power source")
}

func (systemProbe) NetworkOnline() (bool, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return false, err
	}
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp != 0 && iface.Flags&net.FlagLoopback == 0 {
			return true, nil
		}
	}
	return false, nil
}

func (systemProbe) PreventSleep(ctx context.Context) (func() error, error) {
	command := exec.CommandContext(ctx, "caffeinate", "-dimsu")
	if err := command.Start(); err != nil {
		return nil, err
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	return func() error {
		if command.Process != nil {
			_ = command.Process.Kill()
		}
		err := <-done
		var exit *exec.ExitError
		if err != nil && !errors.As(err, &exit) && !errors.Is(err, os.ErrProcessDone) {
			return fmt.Errorf("stop sleep inhibitor: %w", err)
		}
		return nil
	}, nil
}
