package app

import (
	"embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"resticctl/internal/profile"
)

//go:embed templates/*.json
var templateFiles embed.FS

func CreateProfile(configDir, name string) (profilePath, credentialsPath string, err error) {
	if err := profile.ValidateName(name); err != nil {
		return "", "", err
	}
	profilePath = filepath.Join(configDir, name+".json")
	credentialsPath = filepath.Join(configDir, name+".credentials.json")
	for _, path := range []string{profilePath, credentialsPath} {
		if _, statErr := os.Lstat(path); statErr == nil {
			return "", "", fmt.Errorf("refusing to overwrite existing file: %s", path)
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return "", "", fmt.Errorf("cannot inspect profile path %s: %w", path, statErr)
		}
	}
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		return "", "", fmt.Errorf("cannot create profile directory: %w", err)
	}

	profileTemplate, err := templateFiles.ReadFile("templates/profile.json")
	if err != nil {
		return "", "", fmt.Errorf("cannot read profile template: %w", err)
	}
	credentialsTemplate, err := templateFiles.ReadFile("templates/credentials.json")
	if err != nil {
		return "", "", fmt.Errorf("cannot read credentials template: %w", err)
	}
	created := make([]string, 0, 2)
	defer func() {
		if err != nil {
			for _, path := range created {
				if removeErr := os.Remove(path); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
					err = errors.Join(err, fmt.Errorf("cannot remove incomplete profile file %s: %w", path, removeErr))
				}
			}
		}
	}()
	if err = createPrivateFile(profilePath, strings.ReplaceAll(string(profileTemplate), "<profile>", name)); err != nil {
		return "", "", fmt.Errorf("cannot create profile: %w", err)
	}
	created = append(created, profilePath)
	if err = createPrivateFile(credentialsPath, strings.ReplaceAll(string(credentialsTemplate), "<profile>", name)); err != nil {
		return "", "", fmt.Errorf("cannot create profile: %w", err)
	}
	return profilePath, credentialsPath, nil
}

func createPrivateFile(path, content string) (err error) {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		if !ok {
			if removeErr := os.Remove(path); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
				err = errors.Join(err, fmt.Errorf("cannot remove incomplete file %s: %w", path, removeErr))
			}
		}
	}()
	_, writeErr := file.WriteString(content)
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return err
	}
	ok = true
	return nil
}
