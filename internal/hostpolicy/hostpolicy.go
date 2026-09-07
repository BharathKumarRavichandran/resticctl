package hostpolicy

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"resticctl/internal/process"
	"resticctl/internal/profile"
)

var ErrLocked = errors.New("runtime lock is held")

type Probe interface {
	AvailableMemory() (uint64, error)
	OnACPower() (bool, error)
	NetworkOnline() (bool, error)
	ProcessActive(int) (bool, error)
	PreventSleep(context.Context) (func() error, error)
}

type Runner struct {
	Probe Probe
	Now   func() time.Time
}

func New() Runner { return Runner{Probe: systemProbe{}, Now: time.Now} }

func (runner Runner) Run(ctx context.Context, policy profile.Runtime, scheduled, remote bool, operation func(context.Context) error) (runErr error) {
	if runner.Probe == nil {
		return errors.New("host policy probe is not configured")
	}
	if runner.Now == nil {
		runner.Now = time.Now
	}
	if policy.MinimumAvailableMemory > 0 {
		available, err := runner.Probe.AvailableMemory()
		if err != nil {
			return fmt.Errorf("check available memory: %w", err)
		}
		if available < policy.MinimumAvailableMemory {
			return fmt.Errorf("available memory %d bytes is below required %d bytes", available, policy.MinimumAvailableMemory)
		}
	}
	if scheduled && policy.RequireACPower {
		onAC, err := runner.Probe.OnACPower()
		if err != nil {
			return fmt.Errorf("check AC power: %w", err)
		}
		if !onAC {
			return errors.New("scheduled job requires AC power")
		}
	}
	if remote && policy.RequireNetwork {
		online, err := runner.Probe.NetworkOnline()
		if err != nil {
			return fmt.Errorf("check network: %w", err)
		}
		if !online {
			return errors.New("remote repository requires an online network")
		}
	}

	release, err := runner.acquire(ctx, policy.Lock)
	if err != nil {
		return err
	}
	if release != nil {
		defer func() { runErr = errors.Join(runErr, release()) }()
	}
	if policy.PreventSleep {
		restore, err := runner.Probe.PreventSleep(ctx)
		if err != nil {
			return fmt.Errorf("prevent idle sleep: %w", err)
		}
		defer func() { runErr = errors.Join(runErr, restore()) }()
	}
	priority := process.Priority{Portable: policy.Priority, Nice: policy.Nice, Windows: policy.WindowsPriority}
	if policy.IOPriority != nil {
		priority.IO = &process.IOPriority{Class: policy.IOPriority.Class, Level: policy.IOPriority.Level}
	}
	ctx = process.WithPriority(ctx, priority)
	return operation(ctx)
}

type lockRecord struct {
	PID      int       `json:"pid"`
	Hostname string    `json:"hostname"`
	Created  time.Time `json:"created_at"`
	Token    string    `json:"token"`
}

func (runner Runner) acquire(ctx context.Context, lock *profile.RuntimeLock) (func() error, error) {
	if lock == nil || lock.Mode == "ignore" {
		return nil, nil
	}
	wait := time.Duration(0)
	if lock.Mode == "wait" {
		wait, _ = time.ParseDuration(lock.Wait)
	}
	deadline := runner.Now().Add(wait)
	for {
		release, err := runner.tryAcquire(lock)
		if err == nil || !errors.Is(err, ErrLocked) || lock.Mode != "wait" {
			return release, err
		}
		remaining := deadline.Sub(runner.Now())
		if remaining <= 0 {
			return nil, fmt.Errorf("runtime lock wait expired after %s: %w", wait, ErrLocked)
		}
		timer := time.NewTimer(min(remaining, 100*time.Millisecond))
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

func (runner Runner) tryAcquire(lock *profile.RuntimeLock) (func() error, error) {
	if err := os.MkdirAll(filepath.Dir(lock.Path), 0o700); err != nil {
		return nil, fmt.Errorf("create runtime lock directory: %w", err)
	}
	record, err := runner.newLock(lock.Path)
	if err == nil {
		return record, nil
	}
	if !errors.Is(err, os.ErrExist) {
		return nil, fmt.Errorf("create runtime lock: %w", err)
	}
	stale, staleErr := runner.stale(lock)
	if staleErr != nil {
		return nil, staleErr
	}
	if !stale || lock.Stale != "clear" {
		return nil, ErrLocked
	}
	if err := os.Remove(lock.Path); err != nil {
		return nil, fmt.Errorf("clear stale runtime lock: %w", err)
	}
	return runner.newLock(lock.Path)
}

func (runner Runner) newLock(path string) (func() error, error) {
	host, err := os.Hostname()
	if err != nil {
		return nil, err
	}
	tokenBytes := make([]byte, 16)
	if _, err := rand.Read(tokenBytes); err != nil {
		return nil, err
	}
	token := fmt.Sprintf("%x", tokenBytes)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, err
	}
	data, encodeErr := json.Marshal(lockRecord{PID: os.Getpid(), Hostname: host, Created: runner.Now().UTC(), Token: token})
	if encodeErr == nil {
		_, encodeErr = file.Write(append(data, '\n'))
	}
	closeErr := file.Close()
	if encodeErr != nil || closeErr != nil {
		_ = os.Remove(path)
		return nil, errors.Join(encodeErr, closeErr)
	}
	return func() error {
		data, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("inspect runtime lock during release: %w", err)
		}
		var current lockRecord
		if json.Unmarshal(data, &current) != nil || current.Token != token {
			return errors.New("runtime lock changed ownership; refusing to remove it")
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("release runtime lock: %w", err)
		}
		return nil
	}, nil
}

func (runner Runner) stale(lock *profile.RuntimeLock) (bool, error) {
	if lock.StaleAfter == "" {
		return false, nil
	}
	data, err := os.ReadFile(lock.Path)
	if err != nil {
		return false, fmt.Errorf("read runtime lock: %w", err)
	}
	var record lockRecord
	if err := json.Unmarshal(data, &record); err != nil || record.PID <= 0 || record.Hostname == "" || record.Created.IsZero() {
		return false, errors.New("runtime lock metadata is invalid; refusing to clear it")
	}
	age, _ := time.ParseDuration(lock.StaleAfter)
	if runner.Now().Sub(record.Created) < age {
		return false, nil
	}
	host, err := os.Hostname()
	if err != nil {
		return false, err
	}
	if record.Hostname != host {
		return false, errors.New("runtime lock belongs to another host; refusing to clear it")
	}
	active, err := runner.Probe.ProcessActive(record.PID)
	if err != nil {
		return false, fmt.Errorf("check runtime lock owner: %w", err)
	}
	return !active, nil
}
