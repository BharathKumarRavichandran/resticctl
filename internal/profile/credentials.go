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
	if err := validateEnvironment("database_environment", credentials.DatabaseEnvironment); err != nil {
		return Credentials{}, err
	}
	databaseNames := make(map[string]struct{}, len(credentials.DatabaseEnvironments))
	for name, environment := range credentials.DatabaseEnvironments {
		if !isPortableName(name) {
			return Credentials{}, fmt.Errorf("invalid database_environments name: %s", name)
		}
		normalized := strings.ToLower(name)
		if _, exists := databaseNames[normalized]; exists {
			return Credentials{}, fmt.Errorf("duplicate database_environments name: %s", name)
		}
		databaseNames[normalized] = struct{}{}
		if err := validateEnvironment("database_environments."+name, environment); err != nil {
			return Credentials{}, err
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

func validateEnvironment(field string, environment map[string]string) error {
	for key, value := range environment {
		if key == "" || strings.ContainsAny(key, "=\x00") || strings.ContainsRune(value, 0) {
			return fmt.Errorf("invalid %s entry in credentials: %q", field, key)
		}
	}
	return nil
}

func validateDatabaseEnvironmentNames(backupProfile Profile) error {
	configured := make(map[string]struct{}, len(backupProfile.PostgreSQLDatabases)+len(backupProfile.MongoDBDatabases)+len(backupProfile.MySQLDatabases))
	for _, database := range backupProfile.PostgreSQLDatabases {
		configured[strings.ToLower(database.Name)] = struct{}{}
	}
	for _, database := range backupProfile.MongoDBDatabases {
		configured[strings.ToLower(database.Name)] = struct{}{}
	}
	for _, database := range backupProfile.MySQLDatabases {
		configured[strings.ToLower(database.Name)] = struct{}{}
	}
	for name := range backupProfile.Credentials.DatabaseEnvironments {
		if _, ok := configured[strings.ToLower(name)]; !ok {
			return fmt.Errorf("database_environments references unknown database %s", name)
		}
	}
	return nil
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
