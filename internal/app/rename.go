package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"resticctl/internal/configlock"
	"resticctl/internal/profile"
	"resticctl/internal/runstatus"
	"resticctl/internal/securefile"
)

// RenameProfile renames a profile and updates local configuration references.
// Repository snapshots are intentionally not modified.
func RenameProfile(ctx context.Context, configDir, oldName, newName string, dryRun bool) ([]string, error) {
	if err := validateRenameNames(oldName, newName); err != nil {
		return nil, err
	}
	var err error
	configDir, err = filepath.Abs(configDir)
	if err != nil {
		return nil, err
	}

	if dryRun {
		updates, err := planProfileRename(configDir, oldName, newName)
		return describeRenameUpdates(updates), err
	}
	var changes []string
	err = withRenameActionLocks(ctx, configDir, oldName, newName, func() error {
		var err error
		changes, err = renameProfileLocked(configDir, oldName, newName)
		return err
	})
	return changes, err
}

// RenameProfileUnderLocks performs the local rename while the caller holds
// action locks for both profile identities.
func RenameProfileUnderLocks(ctx context.Context, configDir, oldName, newName string) ([]string, error) {
	if err := validateRenameNames(oldName, newName); err != nil {
		return nil, err
	}
	var err error
	configDir, err = filepath.Abs(configDir)
	if err != nil {
		return nil, err
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return renameProfileLocked(configDir, oldName, newName)
}

func validateRenameNames(oldName, newName string) error {
	if err := profile.ValidateName(oldName); err != nil {
		return err
	}
	if err := profile.ValidateName(newName); err != nil {
		return err
	}
	if oldName == newName {
		return errors.New("old and new profile names are identical")
	}
	return nil
}

func renameProfileLocked(configDir, oldName, newName string) ([]string, error) {
	var changes []string
	err := configlock.With(filepath.Join(configDir, ".resticctl.lock"), func() error {
		profileLock := filepath.Join(profile.Dir(configDir), oldName+".json.lock")
		return configlock.With(profileLock, func() error {
			updates, err := planProfileRename(configDir, oldName, newName)
			if err != nil {
				return err
			}
			changes = describeRenameUpdates(updates)
			return applyRenameUpdates(configDir, updates)
		})
	})
	return changes, err
}

func describeRenameUpdates(updates []renameUpdate) []string {
	changes := make([]string, len(updates))
	for index := range updates {
		changes[index] = updates[index].description
	}
	return changes
}

func withRenameActionLocks(ctx context.Context, configDir, oldName, newName string, run func() error) error {
	first, second := oldName, newName
	if second < first {
		first, second = second, first
	}
	return runstatus.WithProfileLock(ctx, configDir, first, func() error {
		return runstatus.WithProfileLock(ctx, configDir, second, func() error {
			return run()
		})
	})
}

type renameUpdate struct {
	source, destination string
	content             []byte
	description         string
	moveOnly            bool
}

func planProfileRename(configDir, oldName, newName string) ([]renameUpdate, error) {
	profilesDir := profile.Dir(configDir)
	oldPath := filepath.Join(profilesDir, oldName+".json")
	newPath := filepath.Join(profilesDir, newName+".json")
	loaded, err := profile.Load(profilesDir, oldName)
	if err != nil {
		return nil, err
	}
	data, err := readRenameFile(oldPath)
	if err != nil {
		return nil, fmt.Errorf("cannot rename profile %s: %w", oldName, err)
	}
	if _, err := os.Lstat(newPath); err == nil {
		return nil, fmt.Errorf("refusing to overwrite existing profile: %s", newPath)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("cannot inspect destination profile %s: %w", newPath, err)
	}

	document, err := decodeRenameDocument(oldPath, data)
	if err != nil {
		return nil, err
	}
	updates := make([]renameUpdate, 0, 4)
	var value string
	privateKey := renameJSONKey(document, "private_file")
	if raw, ok := document[privateKey]; ok && json.Unmarshal(raw, &value) == nil && value == oldName+".private.json" {
		source := filepath.Join(profilesDir, value)
		destination := filepath.Join(profilesDir, newName+".private.json")
		companion, readErr := readRenameFile(source)
		if readErr != nil {
			return nil, fmt.Errorf("cannot rename private_file: %w", readErr)
		}
		if _, statErr := os.Lstat(destination); statErr == nil {
			return nil, fmt.Errorf("refusing to overwrite existing file: %s", destination)
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return nil, statErr
		}
		document[privateKey], _ = json.Marshal(newName + ".private.json")
		updates = append(updates, renameUpdate{source: source, destination: destination, content: companion, description: "rename " + filepath.Base(source) + " to " + filepath.Base(destination)})
	}
	if err := renameMonitoringConfig(document, configDir, oldName, newName); err != nil {
		return nil, err
	}
	data, err = json.MarshalIndent(document, "", "  ")
	if err != nil {
		return nil, err
	}
	updates = append(updates, renameUpdate{source: oldPath, destination: newPath, content: append(data, '\n'), description: "rename profile " + oldName + " to " + newName})

	profileNames, err := profile.List(profilesDir)
	if err != nil {
		return nil, err
	}
	for _, name := range profileNames {
		if name == oldName {
			continue
		}
		path := filepath.Join(profilesDir, name+".json")
		updated, changed, err := replaceJSONName(path, "parent", oldName, newName)
		if err != nil {
			return nil, err
		}
		if changed {
			if _, err := profile.Load(profilesDir, name); err != nil {
				return nil, err
			}
			updates = append(updates, renameUpdate{source: path, destination: path, content: updated, description: "update parent reference in " + name})
		}
	}

	groupsDir := filepath.Join(configDir, "groups")
	entries, err := os.ReadDir(groupsDir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	for _, entry := range entries {
		if !entry.Type().IsRegular() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		path := filepath.Join(groupsDir, entry.Name())
		groupData, err := readRenameFile(path)
		if err != nil {
			return nil, err
		}
		var document map[string]json.RawMessage
		if err := json.Unmarshal(groupData, &document); err != nil {
			return nil, fmt.Errorf("cannot decode group %s: %w", path, err)
		}
		var members []string
		membersKey := renameJSONKey(document, "profiles")
		if err := json.Unmarshal(document[membersKey], &members); err != nil {
			return nil, fmt.Errorf("cannot decode group profiles in %s: %w", path, err)
		}
		changed := false
		for index := range members {
			if members[index] == oldName {
				members[index], changed = newName, true
			}
		}
		if changed {
			seen := make(map[string]struct{}, len(members))
			for _, member := range members {
				key := strings.ToLower(member)
				if _, exists := seen[key]; exists {
					return nil, fmt.Errorf("renaming profile would create duplicate member %s in group %s", member, strings.TrimSuffix(entry.Name(), ".json"))
				}
				seen[key] = struct{}{}
			}
			document[membersKey], _ = json.Marshal(members)
			encoded, _ := json.MarshalIndent(document, "", "  ")
			updates = append(updates, renameUpdate{source: path, destination: path, content: append(encoded, '\n'), description: "update group " + strings.TrimSuffix(entry.Name(), ".json")})
		}
	}
	statusUpdates, err := statusRenameUpdates(configDir, oldName, newName)
	if err != nil {
		return nil, err
	}
	updates = append(updates, statusUpdates...)
	monitoringUpdates, err := monitoringRenameUpdates(configDir, oldName, newName, loaded.Monitoring)
	if err != nil {
		return nil, err
	}
	updates = append(updates, monitoringUpdates...)
	if err := validateRenameUpdates(updates); err != nil {
		return nil, err
	}
	return updates, nil
}

func replaceJSONName(path, field, oldName, newName string) ([]byte, bool, error) {
	data, err := readRenameFile(path)
	if err != nil {
		return nil, false, err
	}
	document, err := decodeRenameDocument(path, data)
	if err != nil {
		return nil, false, err
	}
	var value string
	key := renameJSONKey(document, field)
	if raw, ok := document[key]; !ok || json.Unmarshal(raw, &value) != nil || value != oldName {
		return nil, false, nil
	}
	document[key], _ = json.Marshal(newName)
	encoded, err := json.MarshalIndent(document, "", "  ")
	return append(encoded, '\n'), true, err
}

func statusRenameUpdates(configDir, oldName, newName string) ([]renameUpdate, error) {
	var updates []renameUpdate
	root := filepath.Join(configDir, "status", "profiles", oldName)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if errors.Is(walkErr, os.ErrNotExist) && path == root {
			return nil
		}
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() || filepath.Ext(path) != ".json" {
			return nil
		}
		data, err := readRenameFile(path)
		if err != nil {
			return err
		}
		data, belongs, err := renameStatusIdentity(data, oldName, newName)
		if err != nil {
			return err
		}
		if !belongs {
			return fmt.Errorf("status %s has inconsistent profile identity", path)
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		destination := filepath.Join(configDir, "status", "profiles", newName, relative)
		updates = append(updates, renameUpdate{source: path, destination: destination, content: data, description: "move status " + path + " to " + destination})
		return nil
	})
	if err != nil {
		return nil, err
	}

	return updates, nil
}

func renameStatusIdentity(data []byte, oldName, newName string) ([]byte, bool, error) {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, false, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("unexpected trailing JSON value")
		}
		return nil, false, err
	}
	renameStatus := func(status map[string]any) bool {
		key := renameJSONKey(status, "profile")
		if status[key] != oldName {
			return false
		}
		status[key] = newName
		typeKey, nameKey := renameJSONKey(status, "target_type"), renameJSONKey(status, "target_name")
		if status[typeKey] == "profile" && status[nameKey] == oldName {
			status[nameKey] = newName
		}
		return true
	}
	belongsToProfile := false
	switch typed := value.(type) {
	case map[string]any:
		belongsToProfile = renameStatus(typed)
	case []any:
		var identity string
		for _, item := range typed {
			status, ok := item.(map[string]any)
			if !ok {
				return nil, false, errors.New("status history contains an inconsistent profile identity")
			}
			name, ok := status[renameJSONKey(status, "profile")].(string)
			if !ok || name == "" || (identity != "" && name != identity) {
				return nil, false, errors.New("status history contains an inconsistent profile identity")
			}
			identity = name
			belongsToProfile = renameStatus(status)
		}
	default:
		return nil, false, errors.New("status must be a JSON object or array")
	}
	if !belongsToProfile {
		return data, false, nil
	}
	encoded, err := json.MarshalIndent(value, "", "  ")
	return append(encoded, '\n'), belongsToProfile, err
}

func renameJSONKey[T any](document map[string]T, field string) string {
	for key := range document {
		if strings.EqualFold(key, field) {
			return key
		}
	}
	return field
}

func applyRenameUpdates(configDir string, updates []renameUpdate) error {
	originals := make(map[string][]byte, len(updates))
	created := make(map[string]struct{})
	var moved []renameUpdate
	if err := validateRenameUpdates(updates); err != nil {
		return err
	}
	for _, update := range updates {
		if update.moveOnly {
			continue
		}
		if _, captured := originals[update.source]; !captured {
			data, err := os.ReadFile(update.source)
			if err != nil {
				return err
			}
			originals[update.source] = data
		}
		if update.source != update.destination {
			created[update.destination] = struct{}{}
		}
	}
	rollback := func(cause error) error {
		var rollbackErr error
		for index := len(moved) - 1; index >= 0; index-- {
			rollbackErr = errors.Join(rollbackErr, os.Rename(moved[index].destination, moved[index].source))
		}
		for path, data := range originals {
			rollbackErr = errors.Join(rollbackErr, securefile.WriteAtomic(path, data))
		}
		for path := range created {
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				rollbackErr = errors.Join(rollbackErr, err)
			}
		}
		return errors.Join(cause, rollbackErr)
	}
	for _, update := range updates {
		if err := securefile.MakePrivateDir(configDir, filepath.Dir(update.destination)); err != nil {
			return rollback(err)
		}
		if update.moveOnly {
			if err := os.Rename(update.source, update.destination); err != nil {
				return rollback(err)
			}
			moved = append(moved, update)
			continue
		}
		if err := securefile.WriteAtomic(update.destination, update.content); err != nil {
			return rollback(err)
		}
	}
	for _, update := range updates {
		if update.source != update.destination && !update.moveOnly {
			if err := os.Remove(update.source); err != nil {
				return rollback(err)
			}
		}
	}
	return nil
}

func validateRenameUpdates(updates []renameUpdate) error {
	destinations := make(map[string]string, len(updates))
	for _, update := range updates {
		if previous, exists := destinations[update.destination]; exists && previous != update.source {
			return fmt.Errorf("multiple files would be renamed to %s", update.destination)
		}
		destinations[update.destination] = update.source
		if update.source == update.destination {
			continue
		}
		if _, err := os.Lstat(update.destination); err == nil {
			return fmt.Errorf("refusing to overwrite existing file: %s", update.destination)
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("cannot inspect destination %s: %w", update.destination, err)
		}
	}
	return nil
}

func readRenameFile(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("file is not regular: %s", path)
	}
	return os.ReadFile(path)
}

func decodeRenameDocument(path string, data []byte) (map[string]json.RawMessage, error) {
	var document map[string]json.RawMessage
	if err := json.Unmarshal(data, &document); err != nil {
		return nil, fmt.Errorf("cannot decode %s: %w", path, err)
	}
	return document, nil
}
