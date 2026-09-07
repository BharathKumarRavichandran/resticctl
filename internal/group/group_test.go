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
