package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var validName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

const maxNameLength = 128

type SQLiteDatabase struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

type PasswordSource struct {
	Command []string `json:"command"`
	File    string   `json:"file"`
}

type Credentials struct {
	Environment map[string]string `json:"environment"`
	Password    PasswordSource    `json:"password"`
}

type Profile struct {
	Name            string           `json:"-"`
	Repository      string           `json:"repository"`
	CredentialsFile string           `json:"credentials_file"`
	BackupPaths     []string         `json:"backup_paths"`
	SQLiteDatabases []SQLiteDatabase `json:"sqlite_databases"`
	ResticArgs      []string         `json:"restic_args"`
	BackupArgs      []string         `json:"backup_args"`
	Tags            []string         `json:"tags"`
	ForgetArgs      []string         `json:"forget_args"`
	CheckArgs       []string         `json:"check_args"`
	Credentials     Credentials      `json:"-"`
}

func DefaultConfigDir() (string, error) {
	if value := os.Getenv("RESTICCTL_CONFIG_DIR"); value != "" {
		return expandPath(value, "")
	}
	if value := platformConfigDir(); value != "" {
		return expandPath(value, "")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home directory: %w", err)
	}
	return filepath.Join(home, ".config", "resticctl"), nil
}

func ValidateProfileName(name string) error {
	if !isPortableName(name) {
		return fmt.Errorf("invalid profile name: %s", name)
	}
	return nil
}

func LoadProfile(configDir, name string) (Profile, error) {
	if err := ValidateProfileName(name); err != nil {
		return Profile{}, err
	}
	profilePath := filepath.Join(configDir, name+".json")
	if err := ensurePrivateFile(profilePath, "profile"); err != nil {
		return Profile{}, err
	}

	var profile Profile
	if err := decodeStrict(profilePath, "profile", &profile); err != nil {
		return Profile{}, err
	}
	profile.Name = name
	if profile.Repository == "" {
		return Profile{}, errors.New("repository must be a non-empty string")
	}
	if strings.ContainsRune(profile.Repository, 0) {
		return Profile{}, errors.New("repository must not contain NUL bytes")
	}
	if profile.CredentialsFile == "" {
		return Profile{}, errors.New("credentials_file must be a non-empty string")
	}

	base := filepath.Dir(profilePath)
	credentialsPath, err := expandPath(profile.CredentialsFile, base)
	if err != nil {
		return Profile{}, fmt.Errorf("invalid credentials_file: %w", err)
	}
	profile.CredentialsFile = credentialsPath
	credentials, err := loadCredentials(credentialsPath)
	if err != nil {
		return Profile{}, err
	}
	profile.Credentials = credentials

	for index, path := range profile.BackupPaths {
		if path == "" {
			return Profile{}, errors.New("backup_paths must not contain empty strings")
		}
		expanded, expandErr := expandPath(path, base)
		if expandErr != nil {
			return Profile{}, fmt.Errorf("invalid backup_paths entry: %w", expandErr)
		}
		profile.BackupPaths[index] = expanded
	}

	names := make(map[string]struct{})
	for index := range profile.SQLiteDatabases {
		database := &profile.SQLiteDatabases[index]
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
		{"restic_args", profile.ResticArgs},
		{"backup_args", profile.BackupArgs},
		{"tags", profile.Tags},
		{"forget_args", profile.ForgetArgs},
		{"check_args", profile.CheckArgs},
	}
	for _, list := range argumentLists {
		for _, value := range list.values {
			if value == "" || strings.ContainsRune(value, 0) {
				return Profile{}, fmt.Errorf("%s must not contain empty strings or NUL bytes", list.name)
			}
			if isManagedResticOption(value) {
				return Profile{}, fmt.Errorf("%s must not override repository or password options: %s", list.name, value)
			}
		}
	}
	return profile, nil
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
		if isManagedResticEnvironment(key) {
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

func expandPath(value, base string) (string, error) {
	var missing string
	value = os.Expand(value, func(name string) string {
		expanded, ok := os.LookupEnv(name)
		if !ok && missing == "" {
			missing = name
		}
		return expanded
	})
	if missing != "" {
		return "", fmt.Errorf("environment variable %s is not set", missing)
	}
	if value == "~" || strings.HasPrefix(value, "~/") || strings.HasPrefix(value, `~\`) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		value = filepath.Join(home, strings.TrimLeft(value[1:], `/\`))
	} else if strings.HasPrefix(value, "~") {
		return "", errors.New("~user paths are not supported")
	}
	if base != "" && !filepath.IsAbs(value) {
		value = filepath.Join(base, value)
	}
	return filepath.Clean(value), nil
}

func ListProfiles(configDir string) ([]string, error) {
	entries, err := os.ReadDir(configDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("cannot list profiles in %s: %w", configDir, err)
	}
	var profiles []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.Type().IsRegular() && strings.HasSuffix(name, ".json") && !strings.HasSuffix(name, ".credentials.json") {
			profileName := strings.TrimSuffix(name, ".json")
			if isPortableName(profileName) {
				profiles = append(profiles, profileName)
			}
		}
	}
	sort.Strings(profiles)
	return profiles, nil
}

func isPortableName(name string) bool {
	if len(name) == 0 || len(name) > maxNameLength || !validName.MatchString(name) || strings.HasSuffix(name, ".") {
		return false
	}
	base := strings.ToUpper(strings.SplitN(name, ".", 2)[0])
	if base == "CON" || base == "PRN" || base == "AUX" || base == "NUL" {
		return false
	}
	if len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && base[3] >= '1' && base[3] <= '9' {
		return false
	}
	return true
}

func isManagedResticOption(argument string) bool {
	if argument == "--" || argument == "-r" || strings.HasPrefix(argument, "-r=") {
		return true
	}
	for _, option := range []string{"--repo", "--repository-file", "--password-file", "--password-command"} {
		if argument == option || strings.HasPrefix(argument, option+"=") {
			return true
		}
	}
	return false
}

func isManagedResticEnvironment(key string) bool {
	switch strings.ToUpper(key) {
	case "RESTIC_REPOSITORY", "RESTIC_REPOSITORY_FILE", "RESTIC_PASSWORD", "RESTIC_PASSWORD_FILE", "RESTIC_PASSWORD_COMMAND":
		return true
	default:
		return false
	}
}
