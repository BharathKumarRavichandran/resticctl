package profile

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"resticctl/internal/securefile"
)

func TestWriteScheduleStoresOnlyChildOverride(t *testing.T) {
	directory := t.TempDir()
	parent := `{"repository":"local:test","credentials":{"password":{"command":["unused"]}},"backup_paths":["."],"forget_args":["--keep-last","3"]}`
	child := `{"parent":"base","credentials":{"password":{"command":["unused"]}}}`
	for name, content := range map[string]string{"base.json": parent, "child.json": child} {
		path := filepath.Join(directory, name)
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := securefile.Protect(path); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := WriteBackupSchedule(directory, "child", Schedule{Cron: "0 2 * * *", Backend: "auto", CatchUp: true}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(directory, "child.json"))
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	if len(document) != 3 || document["parent"] == nil || document["credentials"] == nil || document["schedule"] == nil || document["repository"] != nil {
		t.Fatalf("child document was flattened: %s", data)
	}
	loaded, err := Load(directory, "child")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Schedule == nil || loaded.Schedule.Cron != "0 2 * * *" || loaded.Repository != "local:test" {
		t.Fatalf("loaded profile = %#v", loaded)
	}
}

func TestWriteScheduleRejectsReadOnlyProfile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("mode bits do not define Windows write access")
	}
	directory := t.TempDir()
	path := filepath.Join(directory, "example.json")
	if err := os.WriteFile(path, []byte(`{}`), 0o400); err != nil {
		t.Fatal(err)
	}
	_, err := WriteBackupSchedule(directory, "example", Schedule{Cron: "@daily", Backend: "auto"})
	if err == nil || !strings.Contains(err.Error(), "not writable") {
		t.Fatalf("error = %v", err)
	}
}

func TestWriteScheduleRejectsUnknownFields(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "example.json")
	if err := os.WriteFile(path, []byte(`{"unknown":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteBackupSchedule(directory, "example", Schedule{Cron: "@daily", Backend: "auto"}); err == nil {
		t.Fatal("unknown profile field was accepted")
	}
}

func TestWriteScheduleRejectsSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation commonly requires elevated Windows privileges")
	}
	directory := t.TempDir()
	target := filepath.Join(directory, "target.json")
	if err := os.WriteFile(target, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(directory, "example.json")); err != nil {
		t.Fatal(err)
	}
	_, err := WriteBackupSchedule(directory, "example", Schedule{Cron: "@daily", Backend: "auto"})
	if err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("error = %v", err)
	}
}
