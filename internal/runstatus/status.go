package runstatus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"resticctl/internal/profile"
	"resticctl/internal/securefile"
)

var ErrNotRecorded = errors.New("backup status has not been recorded")
var ErrLocked = errors.New("backup is already running for this profile")

type Status struct {
	Profile       string     `json:"profile"`
	Action        string     `json:"action,omitempty"`
	State         string     `json:"state"`
	StartedAt     time.Time  `json:"started_at"`
	FinishedAt    *time.Time `json:"finished_at,omitempty"`
	LastSuccessAt *time.Time `json:"last_success_at,omitempty"`
	DurationMS    int64      `json:"duration_ms,omitempty"`
}

type Recorder struct {
	path    string
	status  Status
	started time.Time
	release func() error
}

func Begin(configDir, name string, now time.Time) (*Recorder, error) {
	return BeginAction(configDir, name, "backup", now)
}

func BeginAction(configDir, name, action string, now time.Time) (*Recorder, error) {
	return beginAction(configDir, name, action, now, acquire)
}

// BeginActionWait retries lock contention until the bounded wait expires.
func BeginActionWait(ctx context.Context, configDir, name, action string, now time.Time, wait time.Duration) (*Recorder, error) {
	deadline := time.NewTimer(wait)
	defer deadline.Stop()
	retry := time.NewTicker(100 * time.Millisecond)
	defer retry.Stop()
	for {
		recorder, err := beginAction(configDir, name, action, now, acquire)
		if !errors.Is(err, ErrLocked) {
			return recorder, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-deadline.C:
			return nil, fmt.Errorf("schedule lock wait expired after %s: %w", wait, ErrLocked)
		case <-retry.C:
		}
	}
}

func beginAction(configDir, name, action string, now time.Time, lock func(string) (func() error, error)) (*Recorder, error) {
	if err := profile.ValidateName(name); err != nil {
		return nil, err
	}
	if err := validateAction(action); err != nil {
		return nil, err
	}
	directory := filepath.Join(configDir, "status")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, fmt.Errorf("cannot create status directory: %w", err)
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return nil, fmt.Errorf("cannot protect status directory: %w", err)
	}
	key := statusKey(name, action)
	release, err := lock(filepath.Join(directory, name+".lock"))
	if err != nil {
		return nil, err
	}
	var lastSuccess *time.Time
	previous, loadErr := LoadAction(configDir, name, action)
	if loadErr == nil {
		lastSuccess = previous.LastSuccessAt
		if lastSuccess == nil && previous.State == "succeeded" && previous.FinishedAt != nil {
			lastSuccess = previous.FinishedAt
		}
	} else if !errors.Is(loadErr, ErrNotRecorded) {
		_ = release()
		return nil, loadErr
	}
	recorder := &Recorder{
		path:    filepath.Join(directory, key+".json"),
		status:  Status{Profile: name, Action: action, State: "running", StartedAt: now.UTC(), LastSuccessAt: lastSuccess},
		started: now,
		release: release,
	}
	if err := write(recorder.path, recorder.status); err != nil {
		_ = release()
		return nil, err
	}
	return recorder, nil
}

func (recorder *Recorder) Finish(runErr error, now time.Time) error {
	finished := now.UTC()
	recorder.status.FinishedAt = &finished
	recorder.status.DurationMS = now.Sub(recorder.started).Milliseconds()
	if runErr == nil {
		recorder.status.State = "succeeded"
		recorder.status.LastSuccessAt = &finished
	} else if errors.Is(runErr, context.Canceled) || errors.Is(runErr, context.DeadlineExceeded) {
		recorder.status.State = "cancelled"
	} else {
		recorder.status.State = "failed"
	}
	writeErr := write(recorder.path, recorder.status)
	releaseErr := recorder.release()
	return errors.Join(writeErr, releaseErr)
}

func Load(configDir, name string) (Status, error) {
	return LoadAction(configDir, name, "backup")
}

func LoadAction(configDir, name, action string) (Status, error) {
	if err := profile.ValidateName(name); err != nil {
		return Status{}, err
	}
	if err := validateAction(action); err != nil {
		return Status{}, err
	}
	path := filepath.Join(configDir, "status", statusKey(name, action)+".json")
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Status{}, fmt.Errorf("%w for profile %s", ErrNotRecorded, name)
	}
	if err != nil {
		return Status{}, fmt.Errorf("cannot read backup status %s: %w", path, err)
	}
	var status Status
	if err := json.Unmarshal(data, &status); err != nil {
		return Status{}, fmt.Errorf("cannot decode backup status %s: %w", path, err)
	}
	if status.Profile != name {
		return Status{}, fmt.Errorf("backup status %s has profile %q, expected %q", path, status.Profile, name)
	}
	if status.Action == "" {
		status.Action = "backup"
	}
	if status.Action != action {
		return Status{}, fmt.Errorf("backup status %s has action %q, expected %q", path, status.Action, action)
	}
	if status.State != "running" && status.State != "succeeded" && status.State != "failed" && status.State != "cancelled" {
		return Status{}, fmt.Errorf("backup status %s has invalid state %q", path, status.State)
	}
	if status.StartedAt.IsZero() {
		return Status{}, fmt.Errorf("backup status %s has no start time", path)
	}
	return status, nil
}

func statusKey(name, action string) string {
	if action == "backup" {
		return name
	}
	return name + "." + action
}

func validateAction(action string) error {
	if action != "backup" && action != "forget" && action != "check" && action != "prune" && action != "copy" {
		return fmt.Errorf("unsupported status action %q", action)
	}
	return nil
}

func write(path string, status Status) error {
	data, err := json.MarshalIndent(status, "", "  ")
	if err != nil {
		return fmt.Errorf("cannot encode backup status: %w", err)
	}
	data = append(data, '\n')
	if err := securefile.WriteAtomic(path, data); err != nil {
		return fmt.Errorf("cannot write backup status %s: %w", path, err)
	}
	return nil
}
