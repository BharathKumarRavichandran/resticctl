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
	"time"

	"resticctl/internal/cronexpr"
)

func Load(configDir, name string) (Profile, error) {
	if err := ValidateName(name); err != nil {
		return Profile{}, err
	}
	configured, err := resolve(configDir, name, nil)
	if err != nil {
		return Profile{}, err
	}
	backupProfile := configured.profile(name)
	profilePath := filepath.Join(configDir, name+".json")
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
	if (backupProfile.PruneBefore || backupProfile.PruneAfter) && len(backupProfile.ForgetArgs) == 0 {
		return Profile{}, errors.New("backup pruning requires non-empty forget_args")
	}
	for _, hooks := range []struct {
		name   string
		values []Hook
	}{
		{"run_before", backupProfile.RunBefore},
		{"run_after", backupProfile.RunAfter},
		{"run_after_fail", backupProfile.RunAfterFail},
		{"run_finally", backupProfile.RunFinally},
	} {
		for index, hook := range hooks.values {
			if len(hook.Command) == 0 {
				return Profile{}, fmt.Errorf("%s[%d].command must contain at least one argument", hooks.name, index)
			}
			for _, part := range hook.Command {
				if part == "" || strings.ContainsRune(part, 0) {
					return Profile{}, fmt.Errorf("%s[%d].command must not contain empty arguments or NUL bytes", hooks.name, index)
				}
			}
			if hook.Timeout != "" {
				timeout, err := time.ParseDuration(hook.Timeout)
				if err != nil || timeout <= 0 {
					return Profile{}, fmt.Errorf("%s[%d].timeout must be a positive duration", hooks.name, index)
				}
			}
		}
	}
	if backupProfile.Schedule != nil {
		schedule := backupProfile.Schedule
		if schedule.Backend == "" {
			schedule.Backend = "auto"
		}
		if schedule.Backend != "auto" && schedule.Backend != "cron" && schedule.Backend != "launchd" {
			return Profile{}, fmt.Errorf("schedule.backend must be auto, cron, or launchd: %s", schedule.Backend)
		}
		normalized, err := cronexpr.Normalize(schedule.Cron)
		if err != nil {
			return Profile{}, fmt.Errorf("invalid schedule.cron: %w", err)
		}
		schedule.Cron = normalized
	}
	if backupProfile.Forget != nil {
		forget := backupProfile.Forget
		if len(backupProfile.ForgetArgs) == 0 {
			return Profile{}, errors.New("forget schedule requires non-empty forget_args")
		}
		if forget.Backend == "" {
			forget.Backend = "auto"
		}
		if forget.Backend != "auto" && forget.Backend != "cron" && forget.Backend != "launchd" {
			return Profile{}, fmt.Errorf("forget.backend must be auto, cron, or launchd: %s", forget.Backend)
		}
		normalized, err := cronexpr.Normalize(forget.Schedule)
		if err != nil {
			return Profile{}, fmt.Errorf("invalid forget.schedule: %w", err)
		}
		forget.Schedule = normalized
	}
	return backupProfile, nil
}

// profileConfig uses pointers for scalars so inheritance can distinguish an
// omitted value from an explicit false or empty value.
type profileConfig struct {
	Parent          string           `json:"parent,omitempty"`
	Repository      *string          `json:"repository"`
	CredentialsFile *string          `json:"credentials_file"`
	BackupPaths     []string         `json:"backup_paths"`
	SQLiteDatabases []SQLiteDatabase `json:"sqlite_databases"`
	ResticArgs      []string         `json:"restic_args"`
	BackupArgs      []string         `json:"backup_args"`
	Tags            []string         `json:"tags"`
	ForgetArgs      []string         `json:"forget_args"`
	CheckArgs       []string         `json:"check_args"`
	CheckBefore     *bool            `json:"check_before"`
	CheckAfter      *bool            `json:"check_after"`
	PruneBefore     *bool            `json:"prune_before"`
	PruneAfter      *bool            `json:"prune_after"`
	RunBefore       []Hook           `json:"run_before"`
	RunAfter        []Hook           `json:"run_after"`
	RunAfterFail    []Hook           `json:"run_after_fail"`
	RunFinally      []Hook           `json:"run_finally"`
	Schedule        *Schedule        `json:"schedule,omitempty"`
	Forget          *ForgetSchedule  `json:"forget,omitempty"`
}

func resolve(configDir, name string, chain []string) (profileConfig, error) {
	if err := ValidateName(name); err != nil {
		return profileConfig{}, fmt.Errorf("invalid parent profile %q: %w", name, err)
	}
	for _, ancestor := range chain {
		if strings.EqualFold(ancestor, name) {
			return profileConfig{}, fmt.Errorf("profile inheritance cycle: %s", strings.Join(append(chain, name), " -> "))
		}
	}
	path := filepath.Join(configDir, name+".json")
	if err := ensurePrivateFile(path, "profile"); err != nil {
		if len(chain) > 0 {
			return profileConfig{}, fmt.Errorf("cannot load parent profile %q: %w", name, err)
		}
		return profileConfig{}, err
	}
	var child profileConfig
	if err := decodeStrict(path, "profile", &child); err != nil {
		return profileConfig{}, err
	}
	if err := validateConfiguredDatabases(child.SQLiteDatabases); err != nil {
		return profileConfig{}, fmt.Errorf("invalid profile %s: %w", name, err)
	}
	if child.Parent == "" {
		return child, nil
	}
	parent, err := resolve(configDir, child.Parent, append(chain, name))
	if err != nil {
		return profileConfig{}, err
	}
	return merge(parent, child), nil
}

func validateConfiguredDatabases(databases []SQLiteDatabase) error {
	names := make(map[string]struct{}, len(databases))
	for _, database := range databases {
		normalized := strings.ToLower(database.Name)
		if _, exists := names[normalized]; exists {
			return fmt.Errorf("duplicate SQLite backup name: %s", database.Name)
		}
		names[normalized] = struct{}{}
	}
	return nil
}

func merge(parent, child profileConfig) profileConfig {
	result := parent
	result.Parent = child.Parent
	if child.Repository != nil {
		result.Repository = child.Repository
	}
	// Credentials deliberately belong to the requested profile and are never inherited.
	result.CredentialsFile = child.CredentialsFile
	result.BackupPaths = appendCopy(parent.BackupPaths, child.BackupPaths)
	result.SQLiteDatabases = mergeDatabases(parent.SQLiteDatabases, child.SQLiteDatabases)
	result.ResticArgs = appendCopy(parent.ResticArgs, child.ResticArgs)
	result.BackupArgs = appendCopy(parent.BackupArgs, child.BackupArgs)
	result.Tags = appendCopy(parent.Tags, child.Tags)
	result.ForgetArgs = appendCopy(parent.ForgetArgs, child.ForgetArgs)
	result.CheckArgs = appendCopy(parent.CheckArgs, child.CheckArgs)
	if child.CheckBefore != nil {
		result.CheckBefore = child.CheckBefore
	}
	if child.CheckAfter != nil {
		result.CheckAfter = child.CheckAfter
	}
	if child.PruneBefore != nil {
		result.PruneBefore = child.PruneBefore
	}
	if child.PruneAfter != nil {
		result.PruneAfter = child.PruneAfter
	}
	result.RunBefore = appendCopy(parent.RunBefore, child.RunBefore)
	result.RunAfter = appendCopy(parent.RunAfter, child.RunAfter)
	result.RunAfterFail = appendCopy(parent.RunAfterFail, child.RunAfterFail)
	result.RunFinally = appendCopy(parent.RunFinally, child.RunFinally)
	if child.Schedule != nil {
		result.Schedule = child.Schedule
	}
	if child.Forget != nil {
		result.Forget = child.Forget
	}
	return result
}

func appendCopy[T any](parent, child []T) []T {
	return append(append([]T(nil), parent...), child...)
}

func mergeDatabases(parent, child []SQLiteDatabase) []SQLiteDatabase {
	result := append([]SQLiteDatabase(nil), parent...)
	positions := make(map[string]int, len(result))
	for index, database := range result {
		positions[strings.ToLower(database.Name)] = index
	}
	for _, database := range child {
		if index, ok := positions[strings.ToLower(database.Name)]; ok {
			result[index] = database
		} else {
			positions[strings.ToLower(database.Name)] = len(result)
			result = append(result, database)
		}
	}
	return result
}

func (configured profileConfig) profile(name string) Profile {
	value := Profile{Name: name, Parent: configured.Parent, BackupPaths: configured.BackupPaths,
		SQLiteDatabases: configured.SQLiteDatabases, ResticArgs: configured.ResticArgs,
		BackupArgs: configured.BackupArgs, Tags: configured.Tags, ForgetArgs: configured.ForgetArgs,
		CheckArgs: configured.CheckArgs, RunBefore: configured.RunBefore, RunAfter: configured.RunAfter,
		RunAfterFail: configured.RunAfterFail, RunFinally: configured.RunFinally,
		Schedule: configured.Schedule, Forget: configured.Forget}
	if configured.Repository != nil {
		value.Repository = *configured.Repository
	}
	if configured.CredentialsFile != nil {
		value.CredentialsFile = *configured.CredentialsFile
	}
	if configured.CheckBefore != nil {
		value.CheckBefore = *configured.CheckBefore
	}
	if configured.CheckAfter != nil {
		value.CheckAfter = *configured.CheckAfter
	}
	if configured.PruneBefore != nil {
		value.PruneBefore = *configured.PruneBefore
	}
	if configured.PruneAfter != nil {
		value.PruneAfter = *configured.PruneAfter
	}
	return value
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
