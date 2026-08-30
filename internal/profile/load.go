package profile

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func Load(configDir, name string) (Profile, error) {
	if err := ValidateName(name); err != nil {
		return Profile{}, err
	}
	profilePath := filepath.Join(configDir, name+".json")
	if err := ensurePrivateFile(profilePath, "profile"); err != nil {
		return Profile{}, err
	}

	var backupProfile Profile
	if err := decodeStrict(profilePath, "profile", &backupProfile); err != nil {
		return Profile{}, err
	}
	backupProfile.Name = name
	if backupProfile.Repository == "" {
		return Profile{}, errors.New("repository must be a non-empty string")
	}
	if strings.ContainsRune(backupProfile.Repository, 0) {
		return Profile{}, errors.New("repository must not contain NUL bytes")
	}
	if backupProfile.CredentialsFile == "" {
		return Profile{}, errors.New("credentials_file must be a non-empty string")
	}

	base := filepath.Dir(profilePath)
	credentialsPath, err := expandPath(backupProfile.CredentialsFile, base)
	if err != nil {
		return Profile{}, fmt.Errorf("invalid credentials_file: %w", err)
	}
	backupProfile.CredentialsFile = credentialsPath
	credentials, err := loadCredentials(credentialsPath)
	if err != nil {
		return Profile{}, err
	}
	backupProfile.Credentials = credentials

	for index, path := range backupProfile.BackupPaths {
		if path == "" {
			return Profile{}, errors.New("backup_paths must not contain empty strings")
		}
		expanded, expandErr := expandPath(path, base)
		if expandErr != nil {
			return Profile{}, fmt.Errorf("invalid backup_paths entry: %w", expandErr)
		}
		backupProfile.BackupPaths[index] = expanded
	}

	names := make(map[string]struct{})
	for index := range backupProfile.SQLiteDatabases {
		database := &backupProfile.SQLiteDatabases[index]
		if !isPortableName(database.Name) {
			return Profile{}, fmt.Errorf("invalid SQLite backup name: %s", database.Name)
		}
		normalized := strings.ToLower(database.Name)
		if _, exists := names[normalized]; exists {
			return Profile{}, fmt.Errorf("duplicate SQLite backup name: %s", database.Name)
		}
		names[normalized] = struct{}{}
		if database.Path == "" {
			return Profile{}, fmt.Errorf("SQLite database path is missing: %s", database.Name)
		}
		database.Path, err = expandPath(database.Path, base)
		if err != nil {
			return Profile{}, fmt.Errorf("invalid SQLite database path: %w", err)
		}
	}

	argumentLists := []struct {
		name   string
		values []string
	}{
		{"restic_args", backupProfile.ResticArgs},
		{"backup_args", backupProfile.BackupArgs},
		{"tags", backupProfile.Tags},
		{"forget_args", backupProfile.ForgetArgs},
		{"check_args", backupProfile.CheckArgs},
	}
	for _, list := range argumentLists {
		for _, value := range list.values {
			if value == "" || strings.ContainsRune(value, 0) {
				return Profile{}, fmt.Errorf("%s must not contain empty strings or NUL bytes", list.name)
			}
			if isReservedOption(value) {
				return Profile{}, fmt.Errorf("%s must not override repository or password options: %s", list.name, value)
			}
		}
	}
	return backupProfile, nil
}

func loadCredentials(path string) (Credentials, error) {
	if err := ensurePrivateFile(path, "credentials"); err != nil {
		return Credentials{}, err
	}
	var credentials Credentials
	if err := decodeStrict(path, "credentials", &credentials); err != nil {
		return Credentials{}, err
	}
	for key, value := range credentials.Environment {
		if key == "" || strings.ContainsAny(key, "=\x00") || strings.ContainsRune(value, 0) {
			return Credentials{}, fmt.Errorf("invalid environment entry in credentials: %q", key)
		}
		if IsReservedEnvironment(key) {
			return Credentials{}, fmt.Errorf("credentials.environment must not set %s", key)
		}
	}

	hasCommand := credentials.Password.Command != nil
	hasFile := credentials.Password.File != ""
	if hasCommand == hasFile {
		return Credentials{}, errors.New("set exactly one of password.command or password.file")
	}
	if hasCommand {
		if len(credentials.Password.Command) == 0 {
			return Credentials{}, errors.New("password.command must contain non-empty arguments")
		}
		for _, part := range credentials.Password.Command {
			if part == "" || strings.ContainsRune(part, 0) {
				return Credentials{}, errors.New("password.command must contain non-empty arguments without NUL bytes")
			}
		}
	} else {
		expanded, err := expandPath(credentials.Password.File, filepath.Dir(path))
		if err != nil {
			return Credentials{}, fmt.Errorf("invalid password.file: %w", err)
		}
		credentials.Password.File = expanded
		if err := ensurePrivateFile(expanded, "password"); err != nil {
			return Credentials{}, err
		}
	}
	return credentials, nil
}

func decodeStrict(path, label string, destination any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("cannot load %s %s: %w", label, path, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("cannot load %s %s: %w", label, path, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("multiple JSON values")
		}
		return fmt.Errorf("cannot load %s %s: %w", label, path, err)
	}
	return nil
}
