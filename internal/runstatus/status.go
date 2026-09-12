package runstatus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"resticctl/internal/profile"
	"resticctl/internal/securefile"
)

var ErrNotRecorded = errors.New("run status has not been recorded")
var ErrLocked = errors.New("another action is already running for this profile")

func validateTargetName(name string) error {
	if strings.HasPrefix(name, "group+") {
		return profile.ValidateName(strings.TrimPrefix(name, "group+"))
	}
	if parts := strings.Split(name, "+copy+"); len(parts) == 2 {
		if err := profile.ValidateName(parts[0]); err != nil {
			return err
		}
		return profile.ValidateName(parts[1])
	}
	return profile.ValidateName(name)
}

type Status struct {
	Profile       string      `json:"profile"`
	TargetType    string      `json:"target_type,omitempty"`
	TargetName    string      `json:"target_name,omitempty"`
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
	release, path, err := acquireAction(context.Background(), configDir, name, action, 0)
	if err != nil {
		return nil, err
	}
	lastSuccess, err := loadLastSuccess(configDir, name, action)
	if err != nil {
		_ = release()
		return nil, err
	}
	return beginActionLocked(path, name, action, now, lastSuccess, release)
}

func BeginGroupAction(configDir, name, action string, now time.Time) (*Recorder, error) {
	key := "group+" + name
	recorder, err := BeginAction(configDir, key, action, now)
	if err == nil {
		recorder.status.TargetType = "group"
		recorder.status.TargetName = name
		if writeErr := write(recorder.path, recorder.status); writeErr != nil {
			_ = recorder.release()
			return nil, writeErr
		}
	}
	return recorder, err
}

func BeginCopyTarget(configDir, profileName, target string, now time.Time) (*Recorder, error) {
	return beginCopyTarget(configDir, profileName, target, now, false)
}

// BeginCopyTargetUnderProfileLock records a copy target when the caller
// already holds the profile-wide action lock, as ScheduledRun does.
func BeginCopyTargetUnderProfileLock(configDir, profileName, target string, now time.Time) (*Recorder, error) {
	return beginCopyTarget(configDir, profileName, target, now, true)
}

func beginCopyTarget(configDir, profileName, target string, now time.Time, profileLocked bool) (*Recorder, error) {
	key := profileName + "+copy+" + target
	var release func() error
	var path string
	if profileLocked {
		if err := validateTargetName(key); err != nil {
			return nil, err
		}
		directory := filepath.Join(configDir, "status")
		path = filepath.Join(directory, statusKey(key, "copy")+".json")
		release = func() error { return nil }
	} else {
		var err error
		release, path, err = acquireAction(context.Background(), configDir, profileName, "copy", 0)
		if err != nil {
			return nil, err
		}
		path = filepath.Join(filepath.Dir(path), statusKey(key, "copy")+".json")
	}
	lastSuccess, err := loadLastSuccess(configDir, key, "copy")
	if err != nil {
		_ = release()
		return nil, err
	}
	recorder := &Recorder{
		path: path, started: now, release: release,
		status: Status{Profile: profileName, TargetType: "copy", TargetName: target, Action: "copy", Command: "copy", State: "running", StartedAt: now.UTC(), LastSuccessAt: lastSuccess},
	}
	if err := write(recorder.path, recorder.status); err != nil {
		_ = recorder.release()
		return nil, err
	}
	return recorder, nil
}

func LoadCopyTarget(configDir, profileName, target string) (Status, error) {
	return LoadAction(configDir, profileName+"+copy+"+target, "copy")
}

func BeginGroupActionIf(ctx context.Context, configDir, name, action string, wait time.Duration, now func() time.Time, shouldRun func(*time.Time) (bool, error)) (*Recorder, bool, error) {
	recorder, due, err := BeginActionIf(ctx, configDir, "group+"+name, action, wait, now, shouldRun)
	if err == nil && due {
		recorder.status.TargetType = "group"
		recorder.status.TargetName = name
		if writeErr := write(recorder.path, recorder.status); writeErr != nil {
			_ = recorder.release()
			return nil, false, writeErr
		}
	}
	return recorder, due, err
}

// BeginActionIf acquires the action lock and evaluates shouldRun against the
// latest successful run while holding it. A zero wait fails immediately on
// contention; a positive wait retries for the bounded duration.
func BeginActionIf(ctx context.Context, configDir, name, action string, wait time.Duration, now func() time.Time, shouldRun func(*time.Time) (bool, error)) (*Recorder, bool, error) {
	release, path, err := acquireAction(ctx, configDir, name, action, wait)
	if err != nil {
		return nil, false, err
	}
	lastSuccess, err := loadLastSuccess(configDir, name, action)
	if err != nil {
		_ = release()
		return nil, false, err
	}
	due, err := shouldRun(lastSuccess)
	if err != nil || !due {
		releaseErr := release()
		return nil, due, errors.Join(err, releaseErr)
	}
	recorder, err := beginActionLocked(path, name, action, now(), lastSuccess, release)
	if err != nil {
		return nil, false, err
	}
	return recorder, true, nil
}

func acquireAction(ctx context.Context, configDir, name, action string, wait time.Duration) (func() error, string, error) {
	if err := validateTargetName(name); err != nil {
		return nil, "", err
	}
	if err := validateAction(action); err != nil {
		return nil, "", err
	}
	directory := filepath.Join(configDir, "status")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, "", fmt.Errorf("cannot create status directory: %w", err)
	}
	if err := securefile.Protect(directory); err != nil {
		return nil, "", fmt.Errorf("cannot protect status directory: %w", err)
	}
	lockPath := filepath.Join(directory, name+".lock")
	release, err := acquire(lockPath)
	if !errors.Is(err, ErrLocked) || wait <= 0 {
		return release, filepath.Join(directory, statusKey(name, action)+".json"), err
	}
	deadline := time.NewTimer(wait)
	defer deadline.Stop()
	retry := time.NewTicker(100 * time.Millisecond)
	defer retry.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil, "", ctx.Err()
		case <-deadline.C:
			return nil, "", fmt.Errorf("schedule lock wait expired after %s: %w", wait, ErrLocked)
		case <-retry.C:
			release, err := acquire(lockPath)
			if !errors.Is(err, ErrLocked) {
				return release, filepath.Join(directory, statusKey(name, action)+".json"), err
			}
		}
	}
}

func loadLastSuccess(configDir, name, action string) (*time.Time, error) {
	var lastSuccess *time.Time
	previous, loadErr := LoadAction(configDir, name, action)
	if loadErr == nil {
		lastSuccess = previous.LastSuccessAt
		if lastSuccess == nil && previous.State == "succeeded" && previous.FinishedAt != nil {
			lastSuccess = previous.FinishedAt
		}
	} else if !errors.Is(loadErr, ErrNotRecorded) {
		return nil, loadErr
	}
	return lastSuccess, nil
}

func beginActionLocked(path, name, action string, now time.Time, lastSuccess *time.Time, release func() error) (*Recorder, error) {
	recorder := &Recorder{
		path:    path,
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
	if err := validateTargetName(name); err != nil {
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
	if err := validateStatus(path, name, action, &status); err != nil {
		return Status{}, err
	}
	return status, nil
}

func validateStatus(path, name, action string, status *Status) error {
	expectedProfile := name
	if parts := strings.Split(name, "+copy+"); len(parts) == 2 {
		expectedProfile = parts[0]
		if status.TargetType != "copy" || status.TargetName != parts[1] {
			return fmt.Errorf("run status %s has inconsistent copy target identity", path)
		}
	}
	if status.Profile != expectedProfile {
		return fmt.Errorf("run status %s has profile %q, expected %q", path, status.Profile, expectedProfile)
	}
	if status.Action == "" {
		status.Action = "backup"
	}
	if status.Command == "" {
		status.Command = status.Action
	}
	if status.Action != action {
		return fmt.Errorf("run status %s has action %q, expected %q", path, status.Action, action)
	}
	if status.State != "running" && status.State != "succeeded" && status.State != "warning" && status.State != "failed" && status.State != "cancelled" {
		return fmt.Errorf("run status %s has invalid state %q", path, status.State)
	}
	if status.StartedAt.IsZero() {
		return fmt.Errorf("run status %s has no start time", path)
	}
	if status.State == "running" {
		if status.FinishedAt != nil {
			return fmt.Errorf("run status %s is running but has a finish time", path)
		}
	} else if status.FinishedAt == nil {
		return fmt.Errorf("run status %s is completed but has no finish time", path)
	} else if status.FinishedAt.Before(status.StartedAt) {
		return fmt.Errorf("run status %s finishes before it starts", path)
	}
	return nil
}

func LoadGroupAction(configDir, name, action string) (Status, error) {
	status, err := LoadAction(configDir, "group+"+name, action)
	if err != nil {
		return Status{}, err
	}
	if status.TargetType != "group" || status.TargetName != name {
		return Status{}, fmt.Errorf("group run status has inconsistent target identity")
	}
	return status, nil
}

// LoadHistory returns completed records newest first.
func LoadHistory(configDir, name, action string) ([]Status, error) {
	if err := validateTargetName(name); err != nil {
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
	for index := range statuses {
		if err := validateStatus(path, name, action, &statuses[index]); err != nil {
			return nil, fmt.Errorf("history record %d: %w", index+1, err)
		}
		if statuses[index].State == "running" {
			return nil, fmt.Errorf("history record %d in %s is not completed", index+1, path)
		}
	}
	return statuses, nil
}

func LoadCopyTargetHistory(configDir, profileName, target string) ([]Status, error) {
	statuses, err := LoadHistory(configDir, profileName+"+copy+"+target, "copy")
	if err != nil {
		return nil, err
	}
	for i := range statuses {
		if statuses[i].Profile != profileName || statuses[i].TargetType != "copy" || statuses[i].TargetName != target || statuses[i].Action != "copy" {
			return nil, errors.New("copy run history has inconsistent target identity")
		}
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
	if err := securefile.Protect(directory); err != nil {
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
