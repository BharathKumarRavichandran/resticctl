package profile

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"

	"resticctl/internal/configlock"
)

// ForCopyTarget returns a profile configured to address a copy destination as
// its primary repository.
func ForCopyTarget(value Profile, targetName string) (Profile, error) {
	target, ok := value.Copies[targetName]
	if !ok {
		return Profile{}, fmt.Errorf("profile %s has no copy target %q", value.Name, targetName)
	}
	value.Repository = target.Repository
	value.Credentials = Credentials{
		Environment: target.Credentials.Environment,
		Password:    target.Credentials.Password,
	}
	value.CredentialsFile = target.CredentialsFile
	value.PrivateFile = ""
	return value, nil
}

// Cutover promotes a copy target and retains the former repository as a
// rollback copy target. It only supports dedicated credential files so the
// rollback target can refer to the old credentials without copying secrets.
func Cutover(configDir, name, targetName string) (string, error) {
	if err := ValidateName(name); err != nil {
		return "", err
	}
	rollbackName := "rollback-" + targetName
	profilePath := filepath.Join(configDir, name+".json")
	err := configlock.With(profilePath+".lock", func() error {
		value, err := Load(configDir, name)
		if err != nil {
			return err
		}
		target, ok := value.Copies[targetName]
		if !ok {
			return fmt.Errorf("profile %s has no copy target %q", name, targetName)
		}
		if value.PrivateFile != "" || value.CredentialsFile == "" {
			return errors.New("migration cutover requires the source profile to use credentials_file")
		}
		if _, exists := value.Copies[rollbackName]; exists {
			return fmt.Errorf("rollback copy target %q already exists", rollbackName)
		}
		for configuredName, configured := range value.Copies {
			if configuredName != targetName && sameRepository(configured.Repository, target.Repository) {
				return fmt.Errorf("copy target %q already uses the migration destination", configuredName)
			}
		}

		document, err := writableDocument(profilePath)
		if err != nil {
			return err
		}
		original := cloneDocument(document)
		if document[matchingJSONKey(document, "credentials_file")] == nil {
			return errors.New("migration cutover requires credentials_file to be declared by the selected profile")
		}
		var copies map[string]json.RawMessage
		if raw := document[matchingJSONKey(document, "copies")]; raw != nil {
			if err := json.Unmarshal(raw, &copies); err != nil {
				return fmt.Errorf("cannot decode copies: %w", err)
			}
		}
		if copies == nil {
			copies = make(map[string]json.RawMessage)
		}
		if _, declared := copies[targetName]; !declared {
			return fmt.Errorf("migration cutover requires copy target %q to be declared by the selected profile", targetName)
		}
		delete(copies, targetName)
		rollback := CopyTarget{
			Repository:      value.Repository,
			CredentialsFile: relativeConfigPath(configDir, value.CredentialsFile),
		}
		encodedRollback, err := json.Marshal(rollback)
		if err != nil {
			return err
		}
		copies[rollbackName] = encodedRollback
		encodedCopies, err := json.Marshal(copies)
		if err != nil {
			return err
		}
		document[matchingJSONKey(document, "repository")] = mustMarshal(target.Repository)
		document[matchingJSONKey(document, "credentials_file")] = mustMarshal(relativeConfigPath(configDir, target.CredentialsFile))
		document[matchingJSONKey(document, "copies")] = encodedCopies
		if err := writeDocument(profilePath, document); err != nil {
			return err
		}
		if _, err := Load(configDir, name); err != nil {
			restoreErr := writeDocument(profilePath, original)
			return errors.Join(fmt.Errorf("cutover produced an invalid profile: %w", err), restoreErr)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return rollbackName, nil
}

func cloneDocument(document map[string]json.RawMessage) map[string]json.RawMessage {
	result := make(map[string]json.RawMessage, len(document))
	for key, value := range document {
		result[key] = append(json.RawMessage(nil), value...)
	}
	return result
}

func relativeConfigPath(configDir, path string) string {
	relative, err := filepath.Rel(configDir, path)
	if err == nil && relative != ".." && !filepath.IsAbs(relative) {
		return relative
	}
	return path
}

func mustMarshal(value string) json.RawMessage {
	encoded, _ := json.Marshal(value)
	return encoded
}
