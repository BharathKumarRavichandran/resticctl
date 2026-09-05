package app

import (
	"encoding/json"
	"os"
	"runtime"
	"strings"
	"testing"

	"resticctl/internal/profile"
)

func TestCreateProfileIsPrivateAndRefusesOverwrite(t *testing.T) {
	directory := t.TempDir()
	profilePath, privatePath, err := CreateProfile(directory, "example")
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		for _, path := range []string{profilePath, privatePath} {
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
	if !strings.Contains(string(content), "example.private.json") {
		t.Fatal("profile template does not reference its private file")
	}
	privateContent, err := os.ReadFile(privatePath)
	if err != nil {
		t.Fatal(err)
	}
	var privateConfig struct {
		Credentials struct {
			Password struct {
				Command []string `json:"command"`
			} `json:"password"`
		} `json:"credentials"`
	}
	if err := json.Unmarshal(privateContent, &privateConfig); err != nil {
		t.Fatalf("decode private configuration: %v", err)
	}
	command := privateConfig.Credentials.Password.Command
	if len(command) == 0 || command[len(command)-1] != "example" {
		t.Fatal("private configuration template does not contain the profile name")
	}
	if strings.Contains(string(content), "<profile>") || strings.Contains(string(privateContent), "<profile>") {
		t.Fatal("generated files contain an unresolved profile placeholder")
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
