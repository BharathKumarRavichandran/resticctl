package group

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteScheduleRoundTrip(t *testing.T) {
	directory := t.TempDir()
	groups := filepath.Join(directory, "groups")
	if err := os.MkdirAll(groups, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(groups, "daily.json"), []byte(`{"profiles":["home"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	want := Schedule{Cron: "0 1 * * *", Backend: "auto", CatchUp: true, Prune: true}
	if _, err := WriteSchedule(directory, "daily", "forget", want); err != nil {
		t.Fatal(err)
	}
	configured, err := Load(directory, "daily")
	if err != nil {
		t.Fatal(err)
	}
	if got := configured.Schedules["forget"]; got != want {
		t.Fatalf("schedule = %#v, want %#v", got, want)
	}
}

func TestWriteScheduleRejectsInvalidNameBeforeCreatingLock(t *testing.T) {
	directory := t.TempDir()
	outside := filepath.Join(directory, "outside")
	if _, err := WriteSchedule(filepath.Join(directory, "config"), "../../outside", "backup", Schedule{Cron: "@daily", Backend: "auto"}); err == nil || !strings.Contains(err.Error(), "invalid group name") {
		t.Fatalf("error = %v", err)
	}
	if _, err := os.Lstat(outside + ".json.lock"); !os.IsNotExist(err) {
		t.Fatalf("unexpected lock outside group directory: %v", err)
	}
}
