package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"resticctl/internal/profile"
)

// RenamedMonitoringPath moves paths within the profile's monitoring directory.
// Explicit output paths outside that directory retain their configured location.
func RenamedMonitoringPath(configDir, oldName, newName, path string) string {
	if path == "" {
		return ""
	}
	root, err := filepath.Abs(filepath.Join(configDir, "monitoring", oldName))
	if err != nil {
		return path
	}
	relative, err := filepath.Rel(root, path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return path
	}
	return filepath.Join(filepath.Dir(root), newName, relative)
}

func renameMonitoringConfig(document map[string]json.RawMessage, configDir, oldName, newName string) error {
	key := renameJSONKey(document, "monitoring")
	raw, ok := document[key]
	if !ok {
		return nil
	}
	var monitoring map[string]json.RawMessage
	if err := json.Unmarshal(raw, &monitoring); err != nil {
		return err
	}
	update := func(object map[string]json.RawMessage, field string) {
		key := renameJSONKey(object, field)
		var path string
		if json.Unmarshal(object[key], &path) != nil {
			return
		}
		expanded := os.ExpandEnv(path)
		if strings.HasPrefix(expanded, "~/") || strings.HasPrefix(expanded, `~\`) {
			if home, err := os.UserHomeDir(); err == nil {
				expanded = filepath.Join(home, expanded[2:])
			}
		}
		if !filepath.IsAbs(expanded) {
			return
		}
		renamed := RenamedMonitoringPath(configDir, oldName, newName, expanded)
		if renamed != expanded {
			object[key], _ = json.Marshal(renamed)
		}
	}
	update(monitoring, "status_file")
	update(monitoring, "prometheus_textfile")
	logsKey := renameJSONKey(monitoring, "logs")
	if raw, ok := monitoring[logsKey]; ok {
		var logs []map[string]json.RawMessage
		if err := json.Unmarshal(raw, &logs); err != nil {
			return err
		}
		for _, log := range logs {
			var kind string
			if json.Unmarshal(log[renameJSONKey(log, "type")], &kind) == nil && kind == "file" {
				update(log, "path")
			}
		}
		monitoring[logsKey], _ = json.Marshal(logs)
	}
	document[key], _ = json.Marshal(monitoring)
	return nil
}

func monitoringRenameUpdates(configDir, oldName, newName string, monitoring profile.Monitoring) ([]renameUpdate, error) {
	var updates []renameUpdate
	root := filepath.Join(configDir, "monitoring", oldName)
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
		if !entry.Type().IsRegular() {
			return fmt.Errorf("monitoring file %s must be a regular file", path)
		}
		moveOnly := path != monitoring.StatusFile && path != monitoring.PrometheusTextfile
		var data []byte
		if !moveOnly {
			var err error
			data, err = readRenameFile(path)
			if err != nil {
				return err
			}
		}
		if path == monitoring.StatusFile {
			var belongs bool
			var err error
			data, belongs, err = renameStatusIdentity(data, oldName, newName)
			if err != nil {
				return err
			}
			if !belongs {
				return fmt.Errorf("monitoring status %s has inconsistent profile identity", path)
			}
		} else if path == monitoring.PrometheusTextfile {
			data = []byte(strings.ReplaceAll(string(data), `profile="`+oldName+`"`, `profile="`+newName+`"`))
		}
		destination := RenamedMonitoringPath(configDir, oldName, newName, path)
		updates = append(updates, renameUpdate{source: path, destination: destination, content: data, moveOnly: moveOnly, description: "move monitoring " + path + " to " + destination})
		return nil
	})
	return updates, err
}
