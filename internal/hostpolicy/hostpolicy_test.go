package hostpolicy

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"resticctl/internal/profile"
)

type fakeProbe struct {
	memory   uint64
	ac       bool
	network  bool
	active   bool
	restored bool
}

func (probe *fakeProbe) AvailableMemory() (uint64, error) { return probe.memory, nil }
func (probe *fakeProbe) OnACPower() (bool, error)         { return probe.ac, nil }
func (probe *fakeProbe) NetworkOnline() (bool, error)     { return probe.network, nil }
func (probe *fakeProbe) ProcessActive(int) (bool, error)  { return probe.active, nil }
func (probe *fakeProbe) PreventSleep(context.Context) (func() error, error) {
	return func() error { probe.restored = true; return nil }, nil
}

func TestRunChecksHostAndRestoresState(t *testing.T) {
	probe := &fakeProbe{memory: 1024, ac: true, network: true}
	runner := Runner{Probe: probe, Now: time.Now}
	err := runner.Run(context.Background(), profile.Runtime{
		MinimumAvailableMemory: 512, RequireACPower: true, RequireNetwork: true, PreventSleep: true,
	}, true, true, func(context.Context) error { return errors.New("job failed") })
	if err == nil || err.Error() != "job failed" {
		t.Fatalf("Run error = %v", err)
	}
	if !probe.restored {
		t.Fatal("sleep state was not restored")
	}
}

func TestRunRejectsFailedPreflight(t *testing.T) {
	probe := &fakeProbe{memory: 10, ac: false, network: false}
	runner := Runner{Probe: probe, Now: time.Now}
	called := false
	err := runner.Run(context.Background(), profile.Runtime{MinimumAvailableMemory: 20}, false, false, func(context.Context) error { called = true; return nil })
	if err == nil || called {
		t.Fatalf("Run = %v, called = %t", err, called)
	}
}

func TestStaleLockRequiresPolicyAndInactiveOwner(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "job.lock")
	now := time.Now()
	host, _ := os.Hostname()
	data, _ := json.Marshal(lockRecord{PID: 999999, Hostname: host, Created: now.Add(-time.Hour), Token: "old"})
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	probe := &fakeProbe{active: false}
	runner := Runner{Probe: probe, Now: func() time.Time { return now }}
	lock := &profile.RuntimeLock{Path: path, Mode: "fail", StaleAfter: "10m", Stale: "fail"}
	if release, err := runner.acquire(context.Background(), lock); !errors.Is(err, ErrLocked) || release != nil {
		t.Fatalf("acquire without clear returned release=%t, error=%v", release != nil, err)
	}
	lock.Stale = "clear"
	release, err := runner.acquire(context.Background(), lock)
	if err != nil {
		t.Fatal(err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("lock remains: %v", err)
	}
}

func TestReleaseDoesNotRemoveReplacementLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "job.lock")
	runner := Runner{Probe: &fakeProbe{}, Now: time.Now}
	release, err := runner.newLock(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"token":"replacement"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := release(); err == nil {
		t.Fatal("release removed or accepted a replacement lock")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("replacement lock was removed: %v", err)
	}
}

type blockingOwnerProbe struct {
	fakeProbe
	entered chan struct{}
	proceed chan struct{}
}

func (probe *blockingOwnerProbe) ProcessActive(int) (bool, error) {
	close(probe.entered)
	<-probe.proceed
	return false, nil
}

func TestStaleLockRecoveryExcludesOtherContenders(t *testing.T) {
	path := filepath.Join(t.TempDir(), "job.lock")
	now := time.Now()
	host, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(lockRecord{PID: 999999, Hostname: host, Created: now.Add(-time.Hour), Token: "old"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	probe := &blockingOwnerProbe{entered: make(chan struct{}), proceed: make(chan struct{})}
	runner := Runner{Probe: probe, Now: func() time.Time { return now }}
	lock := &profile.RuntimeLock{Path: path, Mode: "fail", StaleAfter: "1m", Stale: "clear"}
	type result struct {
		release func() error
		err     error
	}
	done := make(chan result, 1)
	go func() { release, err := runner.tryAcquire(lock); done <- result{release, err} }()
	<-probe.entered
	other := Runner{Probe: &fakeProbe{}, Now: func() time.Time { return now }}
	release, contentionErr := other.tryAcquire(lock)
	close(probe.proceed)
	first := <-done
	if first.err != nil {
		t.Fatal(first.err)
	}
	defer func() {
		if err := first.release(); err != nil {
			t.Error(err)
		}
	}()
	if release != nil || !errors.Is(contentionErr, ErrLocked) {
		t.Fatalf("second acquisition: release=%t, err=%v", release != nil, contentionErr)
	}
	if release, err := other.tryAcquire(lock); release != nil || !errors.Is(err, ErrLocked) {
		t.Fatalf("live lock acquisition: release=%t, err=%v", release != nil, err)
	}
}

func TestCancelledPolicyDoesNotAcquireLockOrRun(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	path := filepath.Join(t.TempDir(), "job.lock")
	runner := Runner{Probe: &fakeProbe{}, Now: time.Now}
	called := false
	err := runner.Run(ctx, profile.Runtime{Lock: &profile.RuntimeLock{Path: path, Mode: "fail"}}, false, false, func(context.Context) error { called = true; return nil })
	if !errors.Is(err, context.Canceled) || called {
		t.Fatalf("called=%t, err=%v", called, err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cancelled policy created lock: %v", err)
	}
}
