package profile

import (
	"path/filepath"
	"testing"
)

func TestForCopyTarget(t *testing.T) {
	value := Profile{
		Name: "home", Repository: "local:source", PrivateFile: "private.json",
		Copies: map[string]CopyTarget{"offsite": {
			Repository: "local:destination", CredentialsFile: "target.json",
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
	writePrivate(t, filepath.Join(directory, "source.json"), `{"password":{"value":"source-secret"}}`)
	writePrivate(t, filepath.Join(directory, "target.json"), `{"password":{"value":"target-secret"}}`)
	writePrivate(t, filepath.Join(directory, "home.json"), `{
      "repository":"local:source", "credentials_file":"source.json",
      "backup_paths":["."],
      "copies":{"offsite":{"repository":"local:destination","credentials_file":"target.json"}}}`)

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
