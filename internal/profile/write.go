package profile

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"resticctl/internal/configlock"
	"resticctl/internal/cronexpr"
	"resticctl/internal/securefile"
)

func WriteBackupSchedule(configDir, name string, scheduled Schedule) (string, error) {
	return writeSchedule(configDir, name, "schedule", scheduled, scheduled.Cron, scheduled.Backend)
}

func WriteForgetSchedule(configDir, name string, scheduled ForgetSchedule) (string, error) {
	return writeSchedule(configDir, name, "forget", scheduled, scheduled.Cron, scheduled.Backend)
}

func writeSchedule(configDir, name, field string, scheduled any, expression, backend string) (string, error) {
	if err := ValidateName(name); err != nil {
		return "", err
	}
	if err := validateWritableSchedule(expression, backend); err != nil {
		return "", err
	}
	profilePath := filepath.Join(configDir, name+".json")
	err := configlock.With(profilePath+".lock", func() error {
		document, err := writableDocument(profilePath)
		if err != nil {
			return err
		}
		value, err := json.Marshal(scheduled)
		if err != nil {
			return fmt.Errorf("cannot encode %s: %w", field, err)
		}
		document[field] = value
		return writeDocument(profilePath, document)
	})
	if err != nil {
		return "", err
	}
	return profilePath, nil
}

func validateWritableSchedule(expression, backend string) error {
	if _, err := cronexpr.Normalize(expression); err != nil {
		return fmt.Errorf("invalid schedule: %w", err)
	}
	if !validScheduleBackend(backend) {
		return fmt.Errorf("schedule backend is unsupported: %s", backend)
	}
	return nil
}

func writableDocument(profilePath string) (map[string]json.RawMessage, error) {
	info, err := os.Lstat(profilePath)
	if err != nil {
		return nil, fmt.Errorf("cannot inspect profile file %s: %w", profilePath, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("profile file is not a regular file: %s", profilePath)
	}
	file, err := os.OpenFile(profilePath, os.O_WRONLY, 0)
	if err != nil {
		return nil, fmt.Errorf("profile file is not writable: %s: %w", profilePath, err)
	}
	if err := file.Close(); err != nil {
		return nil, fmt.Errorf("cannot close profile file %s: %w", profilePath, err)
	}
	data, err := os.ReadFile(profilePath)
	if err != nil {
		return nil, fmt.Errorf("cannot read profile file %s: %w", profilePath, err)
	}
	var configured profileConfig
	if err := decodeStrictJSON(data, &configured); err != nil {
		return nil, fmt.Errorf("cannot update profile %s: %w", profilePath, err)
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(data, &document); err != nil {
		return nil, fmt.Errorf("cannot update profile %s: %w", profilePath, err)
	}
	return document, nil
}

func writeDocument(path string, document map[string]json.RawMessage) error {
	data, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return fmt.Errorf("cannot encode profile file %s: %w", path, err)
	}
	data = append(data, '\n')
	if err := securefile.WriteAtomic(path, data); err != nil {
		if errors.Is(err, os.ErrPermission) {
			return fmt.Errorf("profile file is not writable: %s: %w", path, err)
		}
		return fmt.Errorf("cannot update profile file %s: %w", path, err)
	}
	return nil
}
