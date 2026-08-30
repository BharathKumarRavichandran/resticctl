package app

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCreateProfileIsPrivateAndRefusesOverwrite(t *testing.T) {
	directory := t.TempDir()
	profile, credentials, err := CreateProfile(directory, "example")
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		for _, path := range []string{profile, credentials} {
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if got := info.Mode().Perm(); got != 0o600 {
				t.Fatalf("%s mode = %o, want 600", path, got)
			}
		}
	}
	content, err := os.ReadFile(profile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "example.credentials.json") {
		t.Fatal("profile template does not reference its credentials file")
	}
	profiles, err := ListProfiles(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(profiles) != 1 || profiles[0] != "example" {
		t.Fatalf("profiles = %v", profiles)
	}
	if _, _, err := CreateProfile(directory, "example"); err == nil || !strings.Contains(err.Error(), "refusing to overwrite") {
		t.Fatalf("CreateProfile overwrite error = %v", err)
	}
}

func TestLoadProfileRejectsDuplicateDatabaseNames(t *testing.T) {
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
	_, err := LoadProfile(directory, "example")
	if err == nil || !strings.Contains(err.Error(), "duplicate SQLite") {
		t.Fatalf("LoadProfile error = %v", err)
	}
}

func TestLoadProfileRejectsUnknownJSONField(t *testing.T) {
	directory := t.TempDir()
	writePrivate(t, filepath.Join(directory, "example.json"), `{
          "repository":"local:test",
          "credentials_file":"credentials.json",
          "typo":true
        }`)
	_, err := LoadProfile(directory, "example")
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("LoadProfile error = %v", err)
	}
}

func TestLoadProfileRejectsEmptyBackupPath(t *testing.T) {
	directory := t.TempDir()
	writePrivate(t, filepath.Join(directory, "credentials.json"), `{"password":{"command":["password-command"]}}`)
	writePrivate(t, filepath.Join(directory, "example.json"), `{
          "repository":"local:test",
          "credentials_file":"credentials.json",
          "backup_paths":[""]
        }`)
	_, err := LoadProfile(directory, "example")
	if err == nil || !strings.Contains(err.Error(), "backup_paths") {
		t.Fatalf("LoadProfile error = %v", err)
	}
}

func TestProfileNamesArePortable(t *testing.T) {
	for _, name := range []string{"", "../escape", "trailing.", "CON", "con.txt", "LPT9", strings.Repeat("a", maxNameLength+1)} {
		if err := ValidateProfileName(name); err == nil {
			t.Errorf("ValidateProfileName(%q) succeeded", name)
		}
	}
	for _, name := range []string{"home", "home-server", "photos.2026", "auxiliary"} {
		if err := ValidateProfileName(name); err != nil {
			t.Errorf("ValidateProfileName(%q): %v", name, err)
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

func TestProfileCannotOverrideManagedResticOptions(t *testing.T) {
	directory := t.TempDir()
	writePrivate(t, filepath.Join(directory, "credentials.json"), `{"password":{"command":["password-command"]}}`)
	writePrivate(t, filepath.Join(directory, "example.json"), `{
          "repository":"local:test",
          "credentials_file":"credentials.json",
          "backup_paths":["files"],
          "backup_args":["--repo=other"]
        }`)
	_, err := LoadProfile(directory, "example")
	if err == nil || !strings.Contains(err.Error(), "must not override") {
		t.Fatalf("LoadProfile error = %v", err)
	}
}

func TestCredentialsCannotBypassPasswordSource(t *testing.T) {
	directory := t.TempDir()
	writePrivate(t, filepath.Join(directory, "credentials.json"), `{
          "environment":{"RESTIC_PASSWORD":"secret"},
          "password":{"command":["password-command"]}
        }`)
	writePrivate(t, filepath.Join(directory, "example.json"), `{
          "repository":"local:test",
          "credentials_file":"credentials.json"
        }`)
	_, err := LoadProfile(directory, "example")
	if err == nil || !strings.Contains(err.Error(), "must not set RESTIC_PASSWORD") {
		t.Fatalf("LoadProfile error = %v", err)
	}
}

func TestCredentialsMustBePrivate(t *testing.T) {
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
	_, err := LoadProfile(directory, "example")
	if err == nil || !strings.Contains(err.Error(), "group or others") {
		t.Fatalf("LoadProfile error = %v", err)
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
