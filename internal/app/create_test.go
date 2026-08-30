package app

import (
	"os"
	"runtime"
	"strings"
	"testing"

	"resticctl/internal/profile"
)

func TestCreateProfileIsPrivateAndRefusesOverwrite(t *testing.T) {
	directory := t.TempDir()
	profilePath, credentialsPath, err := CreateProfile(directory, "example")
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		for _, path := range []string{profilePath, credentialsPath} {
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if got := info.Mode().Perm(); got != 0o600 {
				t.Fatalf("%s mode = %o, want 600", path, got)
			}
		}
	}
	content, err := os.ReadFile(profilePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "example.credentials.json") {
		t.Fatal("profile template does not reference its credentials file")
	}
	profiles, err := profile.List(directory)
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
