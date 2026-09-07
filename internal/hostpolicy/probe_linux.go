//go:build linux

package hostpolicy

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

type systemProbe struct{}

func (systemProbe) AvailableMemory() (uint64, error) {
	file, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) >= 2 && fields[0] == "MemAvailable:" {
			kb, err := strconv.ParseUint(fields[1], 10, 64)
			return kb * 1024, err
		}
	}
	return 0, errors.New("MemAvailable is missing from /proc/meminfo")
}

func (systemProbe) OnACPower() (bool, error) {
	matches, err := filepathGlob("/sys/class/power_supply/*/online")
	if err != nil {
		return false, err
	}
	if len(matches) == 0 {
		return false, errors.New("no AC power probe is available")
	}
	for _, path := range matches {
		data, err := os.ReadFile(path)
		if err != nil {
			return false, err
		}
		if strings.TrimSpace(string(data)) == "1" {
			return true, nil
		}
	}
	return false, nil
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
	return startInhibitor(ctx, "systemd-inhibit", "--what=idle:sleep", "--mode=block", "--who=resticctl", "--why=backup job is running", "sleep", "infinity")
}

var filepathGlob = func(pattern string) ([]string, error) { return filepath.Glob(pattern) }

func startInhibitor(ctx context.Context, name string, args ...string) (func() error, error) {
	command := exec.CommandContext(ctx, name, args...)
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
		if err != nil && !errors.Is(err, os.ErrProcessDone) {
			var exit *exec.ExitError
			if !errors.As(err, &exit) {
				return fmt.Errorf("stop sleep inhibitor: %w", err)
			}
		}
		return nil
	}, nil
}
