package app

import (
	"context"
	"encoding/json"
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
	writeRenameTestFile(t, filepath.Join(configDir, "status", "old.json"), `{"profile":"old"}`)
	writeRenameTestFile(t, filepath.Join(configDir, "status", "old+copy+remote.copy.json"), `{"profile":"old","action":"copy"}`)
	writeRenameTestFile(t, filepath.Join(configDir, "status", "old.check.json"), `{"profile":"old.check"}`)

	changes, err := RenameProfile(context.Background(), configDir, "old", "new", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 6 {
		t.Fatalf("changes = %q", changes)
	}
	for _, path := range []string{
		filepath.Join(profilesDir, "new.json"), filepath.Join(profilesDir, "new.private.json"),
		filepath.Join(configDir, "status", "new.json"), filepath.Join(configDir, "status", "new+copy+remote.copy.json"),
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
	assertRenameJSONField(t, filepath.Join(configDir, "status", "new.json"), "profile", "new")
	assertRenameJSONField(t, filepath.Join(configDir, "status", "old.check.json"), "profile", "old.check")

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
	writeRenameTestFile(t, filepath.Join(configDir, "status", "old.json"), `{"profile":"old"}`)
	writeRenameTestFile(t, filepath.Join(configDir, "status", "new.json"), `{"profile":"new"}`)
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
