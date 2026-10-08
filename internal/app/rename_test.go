package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"resticctl/internal/profile"
	"resticctl/internal/securefile"
)

func TestRenameProfileUpdatesLocalReferences(t *testing.T) {
	configDir := t.TempDir()
	profilesDir := profile.Dir(configDir)
	writeRenameTestFile(t, filepath.Join(profilesDir, "old.json"), `{"repository":"repo","private_file":"old.private.json","backup_paths":["."]}`)
	writeRenameTestFile(t, filepath.Join(profilesDir, "old.private.json"), `{"repository":"repo","credentials":{"password":{"command":["unused"]}}}`)
	writeRenameTestFile(t, filepath.Join(profilesDir, "child.json"), `{"parent":"old","backup_paths":["child"],"credentials":{"password":{"command":["unused"]}}}`)
	writeRenameTestFile(t, filepath.Join(configDir, "groups", "daily.json"), `{"name":"daily","profiles":["old","child"]}`)
	writeRenameTestFile(t, filepath.Join(configDir, "status", "profiles", "old", "backup.json"), `{"profile":"old"}`)
	writeRenameTestFile(t, filepath.Join(configDir, "status", "profiles", "old", "copies", "remote", "copy.json"), `{"profile":"old","action":"copy"}`)
	writeRenameTestFile(t, filepath.Join(configDir, "status", "profiles", "old.check", "backup.json"), `{"profile":"old.check"}`)

	changes, err := RenameProfile(context.Background(), configDir, "old", "new", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 6 {
		t.Fatalf("changes = %q", changes)
	}
	for _, path := range []string{
		filepath.Join(profilesDir, "new.json"), filepath.Join(profilesDir, "new.private.json"),
		filepath.Join(configDir, "status", "profiles", "new", "backup.json"), filepath.Join(configDir, "status", "profiles", "new", "copies", "remote", "copy.json"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected %s: %v", path, err)
		}
	}
	for _, path := range []string{filepath.Join(profilesDir, "old.json"), filepath.Join(profilesDir, "old.private.json")} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("old path remains %s: %v", path, err)
		}
	}
	assertRenameJSONField(t, filepath.Join(profilesDir, "new.json"), "private_file", "new.private.json")
	assertRenameJSONField(t, filepath.Join(profilesDir, "child.json"), "parent", "new")
	assertRenameJSONField(t, filepath.Join(configDir, "status", "profiles", "new", "backup.json"), "profile", "new")
	assertRenameJSONField(t, filepath.Join(configDir, "status", "profiles", "old.check", "backup.json"), "profile", "old.check")

	var configured struct {
		Profiles []string `json:"profiles"`
	}
	data, _ := os.ReadFile(filepath.Join(configDir, "groups", "daily.json"))
	if err := json.Unmarshal(data, &configured); err != nil || configured.Profiles[0] != "new" {
		t.Fatalf("group = %#v, error = %v", configured, err)
	}
}

func TestRenameProfileDryRunDoesNotWrite(t *testing.T) {
	configDir := t.TempDir()
	oldPath := filepath.Join(profile.Dir(configDir), "old.json")
	writeRenameTestFile(t, oldPath, `{"repository":"repo","credentials":{"password":{"value":"secret"}},"backup_paths":["."]}`)
	changes, err := RenameProfile(context.Background(), configDir, "old", "new", true)
	if err != nil || len(changes) != 1 {
		t.Fatalf("changes=%q error=%v", changes, err)
	}
	if _, err := os.Stat(oldPath); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(profile.Dir(configDir), "new.json")); !os.IsNotExist(err) {
		t.Fatalf("dry run created destination: %v", err)
	}
	if _, err := os.Stat(filepath.Join(configDir, "status")); !os.IsNotExist(err) {
		t.Fatalf("dry run created status directory: %v", err)
	}
	if _, err := os.Stat(filepath.Join(configDir, ".resticctl.lock")); !os.IsNotExist(err) {
		t.Fatalf("dry run created configuration lock: %v", err)
	}
}

func TestRenameProfileRejectsDuplicateGroupMember(t *testing.T) {
	configDir := t.TempDir()
	writeRenameTestFile(t, filepath.Join(profile.Dir(configDir), "old.json"), `{"repository":"repo","credentials":{"password":{"command":["unused"]}},"backup_paths":["."]}`)
	writeRenameTestFile(t, filepath.Join(configDir, "groups", "daily.json"), `{"name":"daily","profiles":["old","new"]}`)
	if _, err := RenameProfile(context.Background(), configDir, "old", "new", true); err == nil {
		t.Fatal("rename unexpectedly accepted a duplicate group member")
	}
}

func TestRenameProfileDryRunRejectsStatusCollision(t *testing.T) {
	configDir := t.TempDir()
	writeRenameTestFile(t, filepath.Join(profile.Dir(configDir), "old.json"), `{"repository":"repo","credentials":{"password":{"command":["unused"]}},"backup_paths":["."]}`)
	writeRenameTestFile(t, filepath.Join(configDir, "status", "profiles", "old", "backup.json"), `{"profile":"old"}`)
	writeRenameTestFile(t, filepath.Join(configDir, "status", "profiles", "new", "backup.json"), `{"profile":"new"}`)
	if _, err := RenameProfile(context.Background(), configDir, "old", "new", true); err == nil {
		t.Fatal("dry run unexpectedly accepted a status destination collision")
	}
}

func TestRenameStatusIdentityPreservesLargeIntegers(t *testing.T) {
	const input = `{"profile":"old","backup_statistics":{"total_bytes_processed":18446744073709551615}}`
	data, changed, err := renameStatusIdentity([]byte(input), "old", "new")
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("status identity was not detected")
	}
	if !strings.Contains(string(data), "18446744073709551615") {
		t.Fatalf("large integer changed: %s", data)
	}
}

func TestRenameStatusIdentityPreservesCopyTargetAndNestedFields(t *testing.T) {
	const input = `{"profile":"old","target_type":"copy","target_name":"old","details":{"profile":"old"}}`
	data, changed, err := renameStatusIdentity([]byte(input), "old", "new")
	if err != nil || !changed {
		t.Fatalf("changed=%v error=%v", changed, err)
	}
	var status map[string]any
	if err := json.Unmarshal(data, &status); err != nil {
		t.Fatal(err)
	}
	if status["profile"] != "new" || status["target_name"] != "old" {
		t.Fatalf("status identity = %#v", status)
	}
	details := status["details"].(map[string]any)
	if details["profile"] != "old" {
		t.Fatalf("nested profile was changed: %#v", details)
	}
}

func writeRenameTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := securefile.Protect(path); err != nil {
		t.Fatal(err)
	}
}

func assertRenameJSONField(t *testing.T, path, field, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	var got string
	if err := json.Unmarshal(document[field], &got); err != nil || got != want {
		t.Fatalf("%s = %q, want %q (error %v)", field, got, want, err)
	}
}

func TestRenameMovesCurrentStatusAndCopyHistory(t *testing.T) {
	configDir := t.TempDir()
	writeRenameTestFile(t, filepath.Join(profile.Dir(configDir), "old.json"), `{"repository":"repo","credentials":{"password":{"command":["unused"]}},"backup_paths":["."]}`)
	for _, subdir := range []string{"", "history"} {
		for _, actionPath := range []string{"backup", "check", filepath.Join("copies", "remote", "copy")} {
			data := `{"profile":"old"}`
			if strings.HasSuffix(subdir, "history") {
				data = "[" + data + "]"
			}
			writeRenameTestFile(t, filepath.Join(configDir, "status", "profiles", "old", filepath.Dir(actionPath), subdir, filepath.Base(actionPath)+".json"), data)
		}
	}
	if _, err := RenameProfile(context.Background(), configDir, "old", "new", false); err != nil {
		t.Fatal(err)
	}
	for _, subdir := range []string{"", "history"} {
		for _, actionPath := range []string{"backup", "check", filepath.Join("copies", "remote", "copy")} {
			path := filepath.Join(configDir, "status", "profiles", "new", filepath.Dir(actionPath), subdir, filepath.Base(actionPath)+".json")
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(data), `"profile": "new"`) {
				t.Fatalf("identity was not updated: %s", data)
			}
			if _, err := os.Stat(filepath.Join(configDir, "status", "profiles", "old", filepath.Dir(actionPath), subdir, filepath.Base(actionPath)+".json")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("old state remains: %v", err)
			}
		}
	}
}

func TestRenamePreservesUnrelatedHistory(t *testing.T) {
	configDir := t.TempDir()
	writeRenameTestFile(t, filepath.Join(profile.Dir(configDir), "old.json"), `{"repository":"repo","credentials":{"password":{"command":["unused"]}}}`)
	path := filepath.Join(configDir, "status", "profiles", "old.check", "history", "backup.json")
	const history = `[{"profile":"old.check"},{"profile":"old.check"}]`
	writeRenameTestFile(t, path, history)
	if _, err := RenameProfile(context.Background(), configDir, "old", "new", false); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != history {
		t.Fatalf("unrelated history changed: %s, %v", data, err)
	}
}

func TestRenameUpdatesCaseInsensitiveJSONReferences(t *testing.T) {
	configDir := t.TempDir()
	profilesDir := profile.Dir(configDir)
	writeRenameTestFile(t, filepath.Join(profilesDir, "old.json"), `{"repository":"repo","PRIVATE_FILE":"old.private.json"}`)
	writeRenameTestFile(t, filepath.Join(profilesDir, "old.private.json"), `{"credentials":{"password":{"command":["unused"]}}}`)
	writeRenameTestFile(t, filepath.Join(profilesDir, "child.json"), `{"PARENT":"old","credentials":{"password":{"command":["unused"]}}}`)
	writeRenameTestFile(t, filepath.Join(configDir, "groups", "daily.json"), `{"Profiles":["old"]}`)
	if _, err := RenameProfile(context.Background(), configDir, "old", "new", false); err != nil {
		t.Fatal(err)
	}
	if _, err := profile.Load(profilesDir, "child"); err != nil {
		t.Fatal("child profile broken:", err)
	}
	assertRenameJSONField(t, filepath.Join(profilesDir, "child.json"), "PARENT", "new")
	assertRenameJSONField(t, filepath.Join(profilesDir, "new.json"), "PRIVATE_FILE", "new.private.json")
	data, err := os.ReadFile(filepath.Join(configDir, "groups", "daily.json"))
	if err != nil {
		t.Fatal(err)
	}
	var document map[string][]string
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	if members := document["Profiles"]; len(members) != 1 || members[0] != "new" {
		t.Fatalf("group members=%v", members)
	}
}

func TestRenameMovesMonitoringAndUpdatesAbsoluteOutputs(t *testing.T) {
	configDir := t.TempDir()
	root := filepath.Join(configDir, "monitoring", "old")
	statusPath := filepath.Join(root, "latest.json")
	metricsPath := filepath.Join(root, "metrics.prom")
	eventPath := filepath.Join(root, "events.jsonl")
	data, err := json.Marshal(map[string]any{
		"repository": "repo", "credentials": map[string]any{"password": map[string]any{"value": "secret"}},
		"monitoring": map[string]any{"status_file": statusPath, "prometheus_textfile": metricsPath, "logs": []any{map[string]any{"type": "file", "path": eventPath}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	writeRenameTestFile(t, filepath.Join(profile.Dir(configDir), "old.json"), string(data))
	writeRenameTestFile(t, statusPath, `{"profile":"old","target_type":"profile","target_name":"old"}`)
	writeRenameTestFile(t, filepath.Join(root, "latest-forget.json"), `{"profile":"old","target_type":"profile","target_name":"old","action":"forget"}`)
	writeRenameTestFile(t, metricsPath, "resticctl_success{profile=\"old\",command=\"backup\"} 1\n")
	const events = "historical event bytes\n"
	writeRenameTestFile(t, eventPath, events)
	writeRenameTestFile(t, filepath.Join(root, "backup.log"), "stderr bytes\n")
	changes, err := RenameProfile(context.Background(), configDir, "old", "new", true)
	if err != nil || len(changes) != 6 {
		t.Fatalf("changes=%q, err=%v", changes, err)
	}
	if _, err := os.Stat(eventPath); err != nil {
		t.Fatal("dry run changed monitoring", err)
	}
	if _, err := RenameProfile(context.Background(), configDir, "old", "new", false); err != nil {
		t.Fatal(err)
	}
	loaded, err := profile.Load(profile.Dir(configDir), "new")
	if err != nil {
		t.Fatal(err)
	}
	newRoot := filepath.Join(configDir, "monitoring", "new")
	if loaded.Monitoring.StatusFile != filepath.Join(newRoot, "latest.json") || loaded.Monitoring.Logs[0].Path != filepath.Join(newRoot, "events.jsonl") {
		t.Fatalf("paths=%+v", loaded.Monitoring)
	}
	assertRenameJSONField(t, loaded.Monitoring.StatusFile, "target_name", "new")
	assertRenameJSONField(t, filepath.Join(newRoot, "latest-forget.json"), "profile", "new")
	assertRenameJSONField(t, filepath.Join(newRoot, "latest-forget.json"), "target_name", "new")
	metrics, err := os.ReadFile(loaded.Monitoring.PrometheusTextfile)
	if err != nil || !strings.Contains(string(metrics), `profile="new"`) {
		t.Fatalf("metrics=%s, err=%v", metrics, err)
	}
	copied, err := os.ReadFile(loaded.Monitoring.Logs[0].Path)
	if err != nil || string(copied) != events {
		t.Fatalf("events=%s, err=%v", copied, err)
	}
	for _, name := range []string{"latest.json", "latest-forget.json", "metrics.prom", "events.jsonl", "backup.log"} {
		if _, err := os.Stat(filepath.Join(root, name)); !os.IsNotExist(err) {
			t.Fatalf("old file %s remains: %v", name, err)
		}
	}
}

func TestRenameMonitoringCollisionPreservesOriginals(t *testing.T) {
	configDir := t.TempDir()
	oldPath := filepath.Join(profile.Dir(configDir), "old.json")
	writeRenameTestFile(t, oldPath, `{"repository":"repo","credentials":{"password":{"value":"secret"}}}`)
	writeRenameTestFile(t, filepath.Join(configDir, "monitoring", "old", "events.jsonl"), "old events")
	writeRenameTestFile(t, filepath.Join(configDir, "monitoring", "new", "events.jsonl"), "destination events")
	if _, err := RenameProfile(context.Background(), configDir, "old", "new", false); err == nil {
		t.Fatal("accepted collision")
	}
	if _, err := os.Stat(oldPath); err != nil {
		t.Fatal("modified profile", err)
	}
}

func TestRenameMonitoringExpandsEnvironmentPath(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("RESTICCTL_TEST_MONITORING_ROOT", configDir)
	writeRenameTestFile(t, filepath.Join(profile.Dir(configDir), "old.json"), `{
  "repository":"repo","credentials":{"password":{"value":"secret"}},
  "monitoring":{"logs":[{"type":"file","path":"${RESTICCTL_TEST_MONITORING_ROOT}/monitoring/old/events.jsonl"}]}
 }`)
	writeRenameTestFile(t, filepath.Join(configDir, "monitoring", "old", "events.jsonl"), "events")
	if _, err := RenameProfile(context.Background(), configDir, "old", "new", false); err != nil {
		t.Fatal(err)
	}
	loaded, err := profile.Load(profile.Dir(configDir), "new")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(configDir, "monitoring", "new", "events.jsonl")
	if loaded.Monitoring.Logs[0].Path != want {
		t.Fatalf("path=%s want=%s", loaded.Monitoring.Logs[0].Path, want)
	}
}

func TestRenameRollsBackMovedLogOnLaterFailure(t *testing.T) {
	configDir := t.TempDir()
	source := filepath.Join(configDir, "monitoring", "old", "events.jsonl")
	destination := filepath.Join(configDir, "monitoring", "new", "events.jsonl")
	writeRenameTestFile(t, source, "events")
	profilePath := filepath.Join(configDir, "profiles", "old.json")
	writeRenameTestFile(t, profilePath, "original")
	outside := filepath.Join(t.TempDir(), "new.json")
	updates := []renameUpdate{
		{source: source, destination: destination, moveOnly: true},
		{source: profilePath, destination: profilePath, content: []byte("replacement")},
		{source: profilePath, destination: outside, content: []byte("outside")},
	}
	if err := applyRenameUpdates(configDir, updates); err == nil {
		t.Fatal("accepted destination outside configuration directory")
	}
	data, err := os.ReadFile(source)
	if err != nil || string(data) != "events" {
		t.Fatalf("source=%s, err=%v", data, err)
	}
	data, err = os.ReadFile(profilePath)
	if err != nil || string(data) != "original" {
		t.Fatalf("profile=%s, err=%v", data, err)
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatalf("destination remains: %v", err)
	}
}
