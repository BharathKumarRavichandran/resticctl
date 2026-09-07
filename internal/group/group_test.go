package group

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestLoadPreservesProfileOrder(t *testing.T) {
	directory := t.TempDir()
	writeGroup(t, directory, "daily", `{
      "name":"daily",
      "profiles":["home","databases"],
      "continue_on_error":true
    }`)

	configured, err := Load(directory, "daily")
	if err != nil {
		t.Fatal(err)
	}
	if configured.Name != "daily" || !configured.ContinueOnError || !slices.Equal(configured.Profiles, []string{"home", "databases"}) {
		t.Fatalf("group = %#v", configured)
	}
}

func TestLoadRejectsInvalidGroups(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{"empty", `{}`, "at least one"},
		{"duplicate", `{"profiles":["home","HOME"]}`, "duplicate profile"},
		{"mismatch", `{"name":"other","profiles":["home"]}`, "does not match"},
		{"unknown", `{"profiles":["home"],"extra":true}`, "unknown field"},
		{"duplicate field", `{"profiles":["home"],"PROFILES":["databases"]}`, "duplicate JSON field"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			writeGroup(t, directory, "daily", test.content)
			_, err := Load(directory, "daily")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestListReturnsSortedGroups(t *testing.T) {
	directory := t.TempDir()
	writeGroup(t, directory, "weekly", `{"profiles":["home"]}`)
	writeGroup(t, directory, "daily", `{"profiles":["home"]}`)
	groups, err := List(directory)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(groups, []string{"daily", "weekly"}) {
		t.Fatalf("groups = %q", groups)
	}
}

func TestCreateMakesDirectoryAndRefusesOverwrite(t *testing.T) {
	directory := t.TempDir()
	want := Group{Name: "daily", Profiles: []string{"home", "databases"}, ContinueOnError: true}
	path, err := Create(directory, want)
	if err != nil {
		t.Fatal(err)
	}
	if path != filepath.Join(directory, "groups", "daily.json") {
		t.Fatalf("path = %q", path)
	}
	got, err := Load(directory, "daily")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != want.Name || got.ContinueOnError != want.ContinueOnError || !slices.Equal(got.Profiles, want.Profiles) {
		t.Fatalf("group = %#v, want %#v", got, want)
	}
	if _, err := Create(directory, want); err == nil || !strings.Contains(err.Error(), "refusing to overwrite") {
		t.Fatalf("overwrite error = %v", err)
	}
}

func writeGroup(t *testing.T, directory, name, content string) {
	t.Helper()
	groupsDir := filepath.Join(directory, "groups")
	if err := os.MkdirAll(groupsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(groupsDir, name+".json"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
