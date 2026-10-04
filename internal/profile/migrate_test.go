package profile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestForCopyTarget(t *testing.T) {
	value := Profile{
		Name: "home", Repository: "local:source", PrivateFile: "private.json",
		Copies: map[string]CopyTarget{"offsite": {
			Repository: "local:destination", PrivateFile: "target.json",
			Credentials: RepositoryCredentials{Password: PasswordSource{Value: "target-secret"}},
		}},
	}
	got, err := ForCopyTarget(value, "offsite")
	if err != nil {
		t.Fatal(err)
	}
	if got.Repository != "local:destination" || got.Credentials.Password.Value != "target-secret" || got.PrivateFile != "" {
		t.Fatalf("target profile = %#v", got)
	}
}

func TestCutoverRetainsSourceAsRollbackTarget(t *testing.T) {
	directory := t.TempDir()
	writePrivate(t, filepath.Join(directory, "target.json"), `{"credentials":{"password":{"value":"target-secret"}}}`)
	writePrivate(t, filepath.Join(directory, "home.json"), `{
      "repository":"local:source", "credentials":{"password":{"value":"source-secret"}},
      "backup_paths":["."],
      "copies":{"offsite":{"repository":"local:destination","private_file":"target.json"}}}`)

	rollback, err := Cutover(directory, "home", "offsite")
	if err != nil {
		t.Fatal(err)
	}
	if rollback != "rollback-offsite" {
		t.Fatalf("rollback name = %q", rollback)
	}
	loaded, err := Load(directory, "home")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Repository != "local:destination" || loaded.Credentials.Password.Value != "target-secret" {
		t.Fatalf("cutover profile = %#v", loaded)
	}
	old := loaded.Copies[rollback]
	if old.Repository != "local:source" || old.Credentials.Password.Value != "source-secret" {
		t.Fatalf("rollback target = %#v", old)
	}
	if _, exists := loaded.Copies["offsite"]; exists {
		t.Fatal("promoted target was retained as a copy target")
	}
}

func TestCutoverRejectsSourcePrivateOverlayWithoutChangingProfile(t *testing.T) {
	directory := t.TempDir()
	writePrivate(t, filepath.Join(directory, "source.private.json"), `{"credentials":{"password":{"value":"source-secret"}}}`)
	path := filepath.Join(directory, "home.json")
	original := `{"repository":"local:source","private_file":"source.private.json","copies":{"offsite":{"repository":"local:destination","credentials":{"password":{"value":"target-secret"}}}}}`
	writePrivate(t, path, original)
	_, err := Cutover(directory, "home", "offsite")
	if err == nil || !strings.Contains(err.Error(), "private overlays must be cut over manually") {
		t.Fatalf("Cutover error = %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != original {
		t.Fatal("cutover changed the profile")
	}
}

func TestCutoverSuppressesInheritedPromotedTarget(t *testing.T) {
	directory := t.TempDir()
	writePrivate(t, filepath.Join(directory, "parent.json"), `{"copies":{"offsite":{"repository":"local:destination"}}}`)
	writePrivate(t, filepath.Join(directory, "home.json"), `{
		"parent":"parent","repository":"local:source","credentials":{"password":{"value":"source"}},
		"copies":{"offsite":{"credentials":{"password":{"value":"destination"}}}}
	}`)
	if _, err := Cutover(directory, "home", "offsite"); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(directory, "home")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Repository != "local:destination" || loaded.Copies["rollback-offsite"].Credentials.Password.Value != "source" {
		t.Fatalf("cutover profile = %#v", loaded)
	}
	if _, exists := loaded.Copies["offsite"]; exists {
		t.Fatal("promoted target reappeared through inheritance")
	}
}

func TestCutoverVerifiedRejectsChangedConfiguration(t *testing.T) {
	for _, changed := range []string{
		`{"repository":"local:unverified","credentials":{"password":{"value":"destination"}}}`,
		`{"repository":"local:destination","credentials":{"password":{"value":"changed-password"}}}`,
		`{"repository":"local:destination","credentials":{"password":{"value":"destination"}},"tags":["changed-selection"]}`,
	} {
		t.Run(changed, func(t *testing.T) {
			directory := t.TempDir()
			path := filepath.Join(directory, "home.json")
			prefix := `{"repository":"local:source","credentials":{"password":{"value":"source"}},"copies":{"offsite":`
			writePrivate(t, path, prefix+`{"repository":"local:destination","credentials":{"password":{"value":"destination"}}}}}`)
			verified, err := Load(directory, "home")
			if err != nil {
				t.Fatal(err)
			}
			updated := prefix + changed + `}}`
			writePrivate(t, path, updated)
			_, err = CutoverVerified(directory, "home", "offsite", verified)
			if err == nil || !strings.Contains(err.Error(), "changed after migration verification") {
				t.Fatalf("CutoverVerified error = %v", err)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != updated {
				t.Fatal("cutover overwrote changed configuration")
			}
		})
	}
}

func TestCutoverRejectsDisabledRollbackTarget(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "home.json")
	original := `{"repository":"local:source","credentials":{"password":{"value":"source"}},"copies":{"offsite":{"repository":"local:destination","credentials":{"password":{"value":"destination"}}},"ROLLBACK-offsite":null}}`
	writePrivate(t, path, original)
	_, err := Cutover(directory, "home", "offsite")
	if err == nil || !strings.Contains(err.Error(), "already declared") {
		t.Fatalf("Cutover error = %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != original {
		t.Fatal("cutover overwrote a disabled rollback target")
	}
}

func TestCutoverRejectsOverlongRollbackNameBeforeCreatingLock(t *testing.T) {
	directory := t.TempDir()
	_, err := Cutover(directory, "home", strings.Repeat("a", maxNameLength))
	if err == nil || !strings.Contains(err.Error(), "valid rollback copy target name") {
		t.Fatalf("Cutover error = %v", err)
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatal("invalid cutover created files")
	}
}
