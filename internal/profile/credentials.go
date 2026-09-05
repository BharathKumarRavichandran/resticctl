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
	var credentials Credentials
	if err := decodePrivateStrict(path, "credentials", &credentials); err != nil {
		return Credentials{}, err
	}
	base := filepath.Dir(path)
	if err := validateRepositoryCredentials(&credentials, base, "credentials"); err != nil {
		return Credentials{}, err
	}
	if err := validateEnvironment("database_environment", credentials.DatabaseEnvironment); err != nil {
		return Credentials{}, err
	}
	if credentials.DatabaseCredentials != nil && (credentials.DatabaseEnvironment != nil || credentials.DatabaseEnvironments != nil) {
		return Credentials{}, errors.New("credentials.databases must not be combined with deprecated database_environment or database_environments")
	}
	credentialNames := make(map[string]struct{}, len(credentials.DatabaseCredentials))
	for name, credential := range credentials.DatabaseCredentials {
		if !isPortableName(name) {
			return Credentials{}, fmt.Errorf("invalid databases credential name: %s", name)
		}
		normalized := strings.ToLower(name)
		if _, exists := credentialNames[normalized]; exists {
			return Credentials{}, fmt.Errorf("duplicate databases credential name: %s", name)
		}
		credentialNames[normalized] = struct{}{}
		password := credential.Password
		if err := validatePasswordSource("databases."+name+".password", &password, base, false); err != nil {
			return Credentials{}, err
		}
		credential.Password = password
		credentials.DatabaseCredentials[name] = credential
		if err := validateEnvironment("databases."+name+".environment", credential.Environment); err != nil {
			return Credentials{}, err
		}
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

	return credentials, nil
}

func validateRepositoryCredentials(credentials *Credentials, base, label string) error {
	return validateRepositoryCredentialFields(credentials, base, label, true)
}

func validateRepositoryCredentialFields(credentials *Credentials, base, label string, passwordRequired bool) error {
	for key := range credentials.Environment {
		if IsReservedEnvironment(key) {
			return fmt.Errorf("%s.environment must not set %s", label, key)
		}
	}
	if err := validateEnvironment(label+".environment", credentials.Environment); err != nil {
		return err
	}
	return validatePasswordSource("repository password", &credentials.Password, base, passwordRequired)
}

func validateEnvironment(field string, environment map[string]string) error {
	seen := make(map[string]struct{}, len(environment))
	for key, value := range environment {
		if key == "" || strings.ContainsAny(key, "=\x00") || strings.ContainsRune(value, 0) {
			return fmt.Errorf("invalid %s entry in credentials: %q", field, key)
		}
		if strings.EqualFold(key, "RESTICCTL_DATABASE_PASSWORD") {
			return fmt.Errorf("%s must not set reserved environment key %s", field, key)
		}
		normalized := strings.ToUpper(key)
		if _, exists := seen[normalized]; exists {
			return fmt.Errorf("duplicate %s environment key: %s", field, key)
		}
		seen[normalized] = struct{}{}
	}
	return nil
}

func validateDatabaseEnvironmentNames(backupProfile *Profile) error {
	providers := make(map[string]string, len(backupProfile.PostgreSQLDatabases)+len(backupProfile.MongoDBDatabases)+len(backupProfile.MySQLDatabases)+len(backupProfile.SQLServerDatabases))
	for _, database := range backupProfile.PostgreSQLDatabases {
		providers[strings.ToLower(database.Name)] = "postgresql"
	}
	for _, database := range backupProfile.MongoDBDatabases {
		providers[strings.ToLower(database.Name)] = "mongodb"
	}
	for _, database := range backupProfile.MySQLDatabases {
		providers[strings.ToLower(database.Name)] = "mysql"
	}
	for _, database := range backupProfile.SQLServerDatabases {
		providers[strings.ToLower(database.Name)] = "sqlserver"
	}
	for name := range backupProfile.Credentials.DatabaseEnvironments {
		if _, ok := providers[strings.ToLower(name)]; !ok {
			return fmt.Errorf("database_environments references unknown database %s", name)
		}
	}
	if backupProfile.Credentials.DatabaseEnvironments == nil {
		backupProfile.Credentials.DatabaseEnvironments = make(map[string]map[string]string)
	}
	for name, credential := range backupProfile.Credentials.DatabaseCredentials {
		provider, ok := providers[strings.ToLower(name)]
		if !ok {
			return fmt.Errorf("credentials.databases references unknown database %s", name)
		}
		hasPassword := credential.Password.Configured()
		if hasPassword && provider == "mongodb" {
			return fmt.Errorf("credentials.databases.%s.password is not supported for MongoDB; use config_file", name)
		}
		environmentKey, inherited := namedEnvironment(backupProfile.Credentials.DatabaseEnvironments, name)
		environment := make(map[string]string, len(inherited)+len(credential.Environment))
		for key, value := range inherited {
			environment[key] = value
		}
		for key, value := range credential.Environment {
			setEnvironmentValue(environment, key, value)
		}
		if hasPassword {
			passwordKey := map[string]string{"postgresql": "PGPASSWORD", "mysql": "MYSQL_PASSWORD", "sqlserver": "SQLSERVER_PASSWORD"}[provider]
			for key := range environment {
				if strings.EqualFold(key, passwordKey) {
					return fmt.Errorf("credentials.databases.%s must not set both password and environment.%s", name, key)
				}
			}
		}
		if environmentKey == "" {
			environmentKey = name
		}
		backupProfile.Credentials.DatabaseEnvironments[environmentKey] = environment
	}
	return nil
}

func namedEnvironment(environments map[string]map[string]string, name string) (string, map[string]string) {
	for configuredName, environment := range environments {
		if strings.EqualFold(configuredName, name) {
			return configuredName, environment
		}
	}
	return "", nil
}

func decodeStrict(path, label string, destination any) error {
	data, _, err := readStrictJSONFile(path, label)
	if err != nil {
		return err
	}
	if err := decodeStrictJSON(data, destination); err != nil {
		return fmt.Errorf("cannot load %s %s: %w", label, path, err)
	}
	return nil
}

func decodePrivateStrict(path, label string, destination any) error {
	data, info, err := readStrictJSONFile(path, label)
	if err != nil {
		return err
	}
	if err := ensureFileSecurity(info, path, label); err != nil {
		return err
	}
	if err := decodeStrictJSON(data, destination); err != nil {
		return fmt.Errorf("cannot load %s %s: %w", label, path, err)
	}
	return nil
}

func readStrictJSONFile(path, label string) ([]byte, os.FileInfo, error) {
	data, info, err := readRegularFile(path, label)
	if err != nil {
		return nil, nil, err
	}
	if err := rejectDuplicateJSONKeys(data); err != nil {
		return nil, nil, fmt.Errorf("cannot load %s %s: %w", label, path, err)
	}
	return data, info, nil
}

func decodeStrictJSON(data []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func rejectDuplicateJSONKeys(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	var walk func() error
	walk = func() error {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		delimiter, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delimiter {
		case '{':
			seen := make(map[string]struct{})
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return err
				}
				key, ok := keyToken.(string)
				if !ok {
					return errors.New("JSON object key is not a string")
				}
				normalized := strings.ToLower(key)
				if _, exists := seen[normalized]; exists {
					return fmt.Errorf("duplicate JSON field %q", key)
				}
				seen[normalized] = struct{}{}
				if err := walk(); err != nil {
					return err
				}
			}
		case '[':
			for decoder.More() {
				if err := walk(); err != nil {
					return err
				}
			}
		default:
			return fmt.Errorf("unexpected JSON delimiter %q", delimiter)
		}
		_, err = decoder.Token()
		return err
	}
	if err := walk(); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}
