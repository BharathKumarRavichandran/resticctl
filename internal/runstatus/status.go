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

var ErrNotRecorded = errors.New("run status has not been recorded")
var ErrLocked = errors.New("another action is already running for this profile")

type Status struct {
	Profile       string      `json:"profile"`
	Action        string      `json:"action,omitempty"`
	Command       string      `json:"command,omitempty"`
	State         string      `json:"state"`
	StartedAt     time.Time   `json:"started_at"`
	FinishedAt    *time.Time  `json:"finished_at,omitempty"`
	LastSuccessAt *time.Time  `json:"last_success_at,omitempty"`
	DurationMS    int64       `json:"duration_ms,omitempty"`
	ExitCode      *int        `json:"exit_code,omitempty"`
	ErrorCategory string      `json:"error_category,omitempty"`
	Warning       bool        `json:"restic_warning,omitempty"`
	Statistics    *Statistics `json:"backup_statistics,omitempty"`
}

// Statistics is the deliberately small, non-sensitive subset of Restic's
// backup summary suitable for status and metrics export.
type Statistics struct {
	FilesNew            uint64 `json:"files_new,omitempty"`
	FilesChanged        uint64 `json:"files_changed,omitempty"`
	FilesUnmodified     uint64 `json:"files_unmodified,omitempty"`
	DirsNew             uint64 `json:"dirs_new,omitempty"`
	DirsChanged         uint64 `json:"dirs_changed,omitempty"`
	DirsUnmodified      uint64 `json:"dirs_unmodified,omitempty"`
	DataBlobs           uint64 `json:"data_blobs,omitempty"`
	TreeBlobs           uint64 `json:"tree_blobs,omitempty"`
	DataAddedBytes      uint64 `json:"data_added_bytes,omitempty"`
	TotalFilesProcessed uint64 `json:"total_files_processed,omitempty"`
	TotalBytesProcessed uint64 `json:"total_bytes_processed,omitempty"`
}

type Outcome struct {
	Err          error
	ExitCode     *int
	Warning      bool
	WarningState bool
	Statistics   *Statistics
	HistoryLimit int
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
		status:  Status{Profile: name, Action: action, Command: action, State: "running", StartedAt: now.UTC(), LastSuccessAt: lastSuccess},
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
	return recorder.FinishOutcome(Outcome{Err: runErr, HistoryLimit: 100}, now)
}

func (recorder *Recorder) FinishOutcome(outcome Outcome, now time.Time) error {
	finished := now.UTC()
	recorder.status.FinishedAt = &finished
	recorder.status.DurationMS = now.Sub(recorder.started).Milliseconds()
	recorder.status.Warning = outcome.Warning
	recorder.status.Statistics = outcome.Statistics
	recorder.status.ErrorCategory, recorder.status.ExitCode = classify(outcome.Err)
	if outcome.Err == nil && outcome.ExitCode != nil {
		recorder.status.ExitCode = outcome.ExitCode
	}
	if outcome.Err == nil {
		if outcome.WarningState {
			recorder.status.State = "warning"
		} else {
			recorder.status.State = "succeeded"
		}
		recorder.status.LastSuccessAt = &finished
	} else if errors.Is(outcome.Err, context.Canceled) || errors.Is(outcome.Err, context.DeadlineExceeded) {
		recorder.status.State = "cancelled"
	} else {
		recorder.status.State = "failed"
	}
	writeErr := write(recorder.path, recorder.status)
	historyErr := appendHistory(recorder.path, recorder.status, outcome.HistoryLimit)
	releaseErr := recorder.release()
	return errors.Join(writeErr, historyErr, releaseErr)
}

func (recorder *Recorder) Status() Status { return recorder.status }

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
		return Status{}, fmt.Errorf("cannot read run status %s: %w", path, err)
	}
	var status Status
	if err := json.Unmarshal(data, &status); err != nil {
		return Status{}, fmt.Errorf("cannot decode run status %s: %w", path, err)
	}
	if status.Profile != name {
		return Status{}, fmt.Errorf("run status %s has profile %q, expected %q", path, status.Profile, name)
	}
	if status.Action == "" {
		status.Action = "backup"
	}
	if status.Command == "" {
		status.Command = status.Action
	}
	if status.Action != action {
		return Status{}, fmt.Errorf("run status %s has action %q, expected %q", path, status.Action, action)
	}
	if status.State != "running" && status.State != "succeeded" && status.State != "warning" && status.State != "failed" && status.State != "cancelled" {
		return Status{}, fmt.Errorf("run status %s has invalid state %q", path, status.State)
	}
	if status.StartedAt.IsZero() {
		return Status{}, fmt.Errorf("run status %s has no start time", path)
	}
	return status, nil
}

// LoadHistory returns completed records newest first.
func LoadHistory(configDir, name, action string) ([]Status, error) {
	if err := profile.ValidateName(name); err != nil {
		return nil, err
	}
	if err := validateAction(action); err != nil {
		return nil, err
	}
	path := filepath.Join(configDir, "status", "history", statusKey(name, action)+".json")
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%w for profile %s", ErrNotRecorded, name)
	}
	if err != nil {
		return nil, fmt.Errorf("cannot read status history %s: %w", path, err)
	}
	var statuses []Status
	if err := json.Unmarshal(data, &statuses); err != nil {
		return nil, fmt.Errorf("cannot decode status history %s: %w", path, err)
	}
	return statuses, nil
}

type exitCoder interface{ ExitCode() int }

func classify(err error) (string, *int) {
	if err == nil {
		code := 0
		return "", &code
	}
	category := "execution"
	if errors.Is(err, context.Canceled) {
		category = "cancelled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		category = "timeout"
	}
	var coded exitCoder
	if errors.As(err, &coded) {
		code := coded.ExitCode()
		return "command_exit", &code
	}
	return category, nil
}

func appendHistory(latestPath string, status Status, limit int) error {
	if limit <= 0 {
		return nil
	}
	directory := filepath.Join(filepath.Dir(latestPath), "history")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("cannot create status history directory: %w", err)
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return fmt.Errorf("cannot protect status history directory: %w", err)
	}
	path := filepath.Join(directory, filepath.Base(latestPath))
	var history []Status
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, &history); err != nil {
			return fmt.Errorf("cannot decode status history %s: %w", path, err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("cannot read status history %s: %w", path, err)
	}
	history = append([]Status{status}, history...)
	if len(history) > limit {
		history = history[:limit]
	}
	data, err := json.MarshalIndent(history, "", "  ")
	if err != nil {
		return fmt.Errorf("cannot encode status history: %w", err)
	}
	data = append(data, '\n')
	if err := securefile.WriteAtomic(path, data); err != nil {
		return fmt.Errorf("cannot write status history %s: %w", path, err)
	}
	return nil
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
		return fmt.Errorf("cannot encode run status: %w", err)
	}
	data = append(data, '\n')
	if err := securefile.WriteAtomic(path, data); err != nil {
		return fmt.Errorf("cannot write run status %s: %w", path, err)
	}
	return nil
}
