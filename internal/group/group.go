package group

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"resticctl/internal/profile"
)

// Group is an ordered collection of profiles that are run sequentially.
type Group struct {
	Name            string   `json:"name"`
	Profiles        []string `json:"profiles"`
	ContinueOnError bool     `json:"continue_on_error"`
}

func Load(configDir, name string) (Group, error) {
	if err := validateName(name); err != nil {
		return Group{}, err
	}
	groupPath := filepath.Join(configDir, "groups", name+".json")
	file, err := os.Open(groupPath)
	if errors.Is(err, os.ErrNotExist) {
		return Group{}, fmt.Errorf("group file not found: %s", groupPath)
	}
	if err != nil {
		return Group{}, fmt.Errorf("cannot open group file %s: %w", groupPath, err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return Group{}, fmt.Errorf("cannot inspect group file %s: %w", groupPath, err)
	}
	if !info.Mode().IsRegular() {
		return Group{}, fmt.Errorf("group file is not a regular file: %s", groupPath)
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return Group{}, fmt.Errorf("cannot load group file %s: %w", groupPath, err)
	}
	if err := rejectDuplicateFields(data); err != nil {
		return Group{}, fmt.Errorf("cannot load group %s: %w", groupPath, err)
	}
	var configured Group
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&configured); err != nil {
		return Group{}, fmt.Errorf("cannot load group %s: %w", groupPath, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("unexpected trailing JSON value")
		}
		return Group{}, fmt.Errorf("cannot load group %s: %w", groupPath, err)
	}
	if configured.Name == "" {
		configured.Name = name
	}
	if configured.Name != name {
		return Group{}, fmt.Errorf("group name %q does not match file name %q", configured.Name, name)
	}
	if len(configured.Profiles) == 0 {
		return Group{}, errors.New("group profiles must contain at least one profile")
	}
	seen := make(map[string]struct{}, len(configured.Profiles))
	for _, member := range configured.Profiles {
		if err := profile.ValidateName(member); err != nil {
			return Group{}, fmt.Errorf("invalid group member %q: %w", member, err)
		}
		normalized := strings.ToLower(member)
		if _, exists := seen[normalized]; exists {
			return Group{}, fmt.Errorf("duplicate profile in group: %s", member)
		}
		seen[normalized] = struct{}{}
	}
	return configured, nil
}

func rejectDuplicateFields(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if token != json.Delim('{') {
		return errors.New("group must be a JSON object")
	}
	seen := make(map[string]struct{})
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		key, ok := token.(string)
		if !ok {
			return errors.New("group field name is not a string")
		}
		normalized := strings.ToLower(key)
		if _, exists := seen[normalized]; exists {
			return fmt.Errorf("duplicate JSON field %q", key)
		}
		seen[normalized] = struct{}{}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return err
		}
	}
	return nil
}

func List(configDir string) ([]string, error) {
	directory := filepath.Join(configDir, "groups")
	entries, err := os.ReadDir(directory)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("cannot list groups in %s: %w", directory, err)
	}
	var groups []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.Type().IsRegular() && strings.HasSuffix(name, ".json") {
			groupName := strings.TrimSuffix(name, ".json")
			if validateName(groupName) == nil {
				groups = append(groups, groupName)
			}
		}
	}
	sort.Strings(groups)
	return groups, nil
}

func validateName(name string) error {
	if err := profile.ValidateName(name); err != nil {
		return fmt.Errorf("invalid group name: %s", name)
	}
	return nil
}
