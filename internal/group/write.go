package group

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"resticctl/internal/configlock"
	"resticctl/internal/securefile"
)

func WriteSchedule(configDir, name, action string, scheduled Schedule) (string, error) {
	if err := validateName(name); err != nil {
		return "", err
	}
	path := filepath.Join(configDir, "groups", name+".json")
	err := configlock.With(path+".lock", func() error {
		return writeSchedule(path, configDir, name, action, scheduled)
	})
	if err != nil {
		return "", err
	}
	return path, nil
}

func writeSchedule(path, configDir, name, action string, scheduled Schedule) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("cannot inspect group file %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("group file is not a regular file: %s", path)
	}
	file, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return fmt.Errorf("group file is not writable: %s: %w", path, err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("cannot close group file %s: %w", path, err)
	}
	configured, err := Load(configDir, name)
	if err != nil {
		return err
	}
	if configured.Schedules == nil {
		configured.Schedules = make(map[string]Schedule)
	}
	configured.Schedules[action] = scheduled
	if err := validate(configured, name); err != nil {
		return err
	}
	data, err := json.MarshalIndent(configured, "", "  ")
	if err != nil {
		return fmt.Errorf("cannot encode group %s: %w", name, err)
	}
	if err := securefile.WriteAtomic(path, append(data, '\n')); err != nil {
		return fmt.Errorf("cannot update group file %s: %w", path, err)
	}
	return nil
}
