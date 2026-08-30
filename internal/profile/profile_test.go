package profile

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestLoadRejectsDuplicateDatabaseNames(t *testing.T) {
	directory := t.TempDir()
	password := filepath.Join(directory, "password")
	writePrivate(t, password, "secret\n")
	credentials := filepath.Join(directory, "credentials.json")
	writePrivate(t, credentials, `{"password":{"file":"password"}}`)
	writePrivate(t, filepath.Join(directory, "example.json"), `{
          "repository":"local:test",
          "credentials_file":"credentials.json",
          "sqlite_databases":[
            {"name":"Data","path":"one"},
            {"name":"data","path":"two"}
          ]
        }`)
	_, err := Load(directory, "example")
	if err == nil || !strings.Contains(err.Error(), "duplicate SQLite") {
		t.Fatalf("Load error = %v", err)
	}
}

func TestLoadRejectsUnknownJSONField(t *testing.T) {
	directory := t.TempDir()
	writePrivate(t, filepath.Join(directory, "example.json"), `{
          "repository":"local:test",
          "credentials_file":"credentials.json",
          "typo":true
        }`)
	_, err := Load(directory, "example")
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("Load error = %v", err)
	}
}

func TestLoadRejectsEmptyBackupPath(t *testing.T) {
	directory := t.TempDir()
	writePrivate(t, filepath.Join(directory, "credentials.json"), `{"password":{"command":["password-command"]}}`)
	writePrivate(t, filepath.Join(directory, "example.json"), `{
          "repository":"local:test",
          "credentials_file":"credentials.json",
          "backup_paths":[""]
        }`)
	_, err := Load(directory, "example")
	if err == nil || !strings.Contains(err.Error(), "backup_paths") {
		t.Fatalf("Load error = %v", err)
	}
}

func TestValidateNameRejectsNonPortableNames(t *testing.T) {
	for _, name := range []string{"", "../escape", "trailing.", "CON", "con.txt", "LPT9", strings.Repeat("a", maxNameLength+1)} {
		if err := ValidateName(name); err == nil {
			t.Errorf("ValidateName(%q) succeeded", name)
		}
	}
	for _, name := range []string{"home", "home-server", "photos.2026", "auxiliary"} {
		if err := ValidateName(name); err != nil {
			t.Errorf("ValidateName(%q): %v", name, err)
		}
	}
}

func TestExpandPathRejectsUnsetEnvironmentVariable(t *testing.T) {
	const name = "RESTICCTL_TEST_MISSING_VARIABLE"
	t.Setenv(name, "temporary")
	if err := os.Unsetenv(name); err != nil {
		t.Fatal(err)
	}
	_, err := expandPath("${"+name+"}/files", t.TempDir())
	if err == nil || !strings.Contains(err.Error(), name) {
		t.Fatalf("expandPath error = %v", err)
	}
}

func TestLoadRejectsReservedResticOptions(t *testing.T) {
	directory := t.TempDir()
	writePrivate(t, filepath.Join(directory, "credentials.json"), `{"password":{"command":["password-command"]}}`)
	writePrivate(t, filepath.Join(directory, "example.json"), `{
          "repository":"local:test",
          "credentials_file":"credentials.json",
          "backup_paths":["files"],
          "backup_args":["--repo=other"]
        }`)
	_, err := Load(directory, "example")
	if err == nil || !strings.Contains(err.Error(), "must not override") {
		t.Fatalf("Load error = %v", err)
	}
}

func TestLoadRejectsReservedResticEnvironment(t *testing.T) {
	directory := t.TempDir()
	writePrivate(t, filepath.Join(directory, "credentials.json"), `{
          "environment":{"RESTIC_PASSWORD":"secret"},
          "password":{"command":["password-command"]}
        }`)
	writePrivate(t, filepath.Join(directory, "example.json"), `{
          "repository":"local:test",
          "credentials_file":"credentials.json"
        }`)
	_, err := Load(directory, "example")
	if err == nil || !strings.Contains(err.Error(), "must not set RESTIC_PASSWORD") {
		t.Fatalf("Load error = %v", err)
	}
}

func TestLoadSchedule(t *testing.T) {
	directory := t.TempDir()
	writePrivate(t, filepath.Join(directory, "credentials.json"), `{"password":{"command":["password-command"]}}`)
	writePrivate(t, filepath.Join(directory, "example.json"), `{
          "repository":"local:test",
          "credentials_file":"credentials.json",
          "forget_args":["--keep-daily","7"],
          "schedule":{"cron":" 0  2 * * * ","catch_up":true},
          "forget":{"schedule":"@daily","catch_up":true,"prune":true}
        }`)
	loaded, err := Load(directory, "example")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Schedule == nil || loaded.Schedule.Backend != "auto" || loaded.Schedule.Cron != "0 2 * * *" || !loaded.Schedule.CatchUp {
		t.Fatalf("schedule = %#v", loaded.Schedule)
	}
	if loaded.Forget == nil || loaded.Forget.Backend != "auto" || loaded.Forget.Schedule != "0 0 * * *" || !loaded.Forget.CatchUp || !loaded.Forget.Prune {
		t.Fatalf("forget = %#v", loaded.Forget)
	}
}

func TestLoadRejectsInvalidSchedule(t *testing.T) {
	for _, configuredSchedule := range []string{
		`{"backend":"systemd","cron":"0 2 * * *"}`,
		`{"backend":"auto","cron":"99 2 * * *"}`,
	} {
		t.Run(configuredSchedule, func(t *testing.T) {
			directory := t.TempDir()
			writePrivate(t, filepath.Join(directory, "credentials.json"), `{"password":{"command":["password-command"]}}`)
			writePrivate(t, filepath.Join(directory, "example.json"), `{
              "repository":"local:test",
              "credentials_file":"credentials.json",
              "schedule":`+configuredSchedule+`
            }`)
			if _, err := Load(directory, "example"); err == nil || !strings.Contains(err.Error(), "schedule") {
				t.Fatalf("Load error = %v", err)
			}
		})
	}
}

func TestLoadRequiresPrivateCredentialsFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission test")
	}
	directory := t.TempDir()
	credentials := filepath.Join(directory, "credentials.json")
	writePrivate(t, filepath.Join(directory, "example.json"), `{"repository":"local:test","credentials_file":"credentials.json"}`)
	if err := os.WriteFile(credentials, []byte(`{"password":{"command":["printf","secret"]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(credentials, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(directory, "example")
	if err == nil || !strings.Contains(err.Error(), "group or others") {
		t.Fatalf("Load error = %v", err)
	}
}

func writePrivate(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
}
