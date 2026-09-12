package profile

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadNamedCopyTargets(t *testing.T) {
	directory := t.TempDir()
	writePrivate(t, filepath.Join(directory, "source.json"), `{"password":{"value":"source-secret"},"environment":{"SOURCE_TOKEN":"one"}}`)
	writePrivate(t, filepath.Join(directory, "offsite.json"), `{"password":{"value":"target-secret"},"environment":{"TARGET_TOKEN":"two"}}`)
	writePrivate(t, filepath.Join(directory, "home.json"), `{
          "repository":"local:primary", "credentials_file":"source.json",
          "copies":{"offsite":{"repository":"local:secondary","credentials_file":"offsite.json",
			"initialize_repository":true,"copy_chunker_params":true,"snapshot_ids":["latest"],
			"hosts":["host-a"],"tags":["important"],"paths":["/home"]}}}
        `)
	loaded, err := Load(directory, "home")
	if err != nil {
		t.Fatal(err)
	}
	target := loaded.Copies["offsite"]
	if target.Repository != "local:secondary" || target.Credentials.Password.Value != "target-secret" {
		t.Fatalf("copy target = %#v", target)
	}
	if !filepath.IsAbs(target.CredentialsFile) {
		t.Fatalf("credentials path = %q", target.CredentialsFile)
	}
}

func TestLoadRejectsUnsafeCopyTargets(t *testing.T) {
	for _, test := range []struct{ name, target, want string }{
		{"same repository", `"repository":"local:primary","credentials_file":"target.json"`, "must differ"},
		{"missing credentials", `"repository":"local:secondary"`, "credentials_file is required"},
		{"reserved argument", `"repository":"local:secondary","credentials_file":"target.json","args":["--from-repo=evil"]`, "must not override"},
		{"dry-run argument", `"repository":"local:secondary","credentials_file":"target.json","args":["--dry-run"]`, "workflow-owned dry-run"},
		{"chunker without init", `"repository":"local:secondary","credentials_file":"target.json","copy_chunker_params":true`, "requires initialize"},
	} {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			writePrivate(t, filepath.Join(directory, "source.json"), `{"password":{"value":"source"}}`)
			writePrivate(t, filepath.Join(directory, "target.json"), `{"password":{"value":"target"}}`)
			writePrivate(t, filepath.Join(directory, "home.json"), `{"repository":"local:primary","credentials_file":"source.json","copies":{"offsite":{`+test.target+`}}}`)
			_, err := Load(directory, "home")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Load error = %v", err)
			}
		})
	}
}

func TestLoadAllowsDisabledCopyDryRunArgument(t *testing.T) {
	directory := t.TempDir()
	writePrivate(t, filepath.Join(directory, "source.json"), `{"password":{"value":"source"}}`)
	writePrivate(t, filepath.Join(directory, "target.json"), `{"password":{"value":"target"}}`)
	writePrivate(t, filepath.Join(directory, "home.json"), `{
          "repository":"local:primary",
          "credentials_file":"source.json",
          "copies":{"offsite":{
            "repository":"local:secondary",
            "credentials_file":"target.json",
            "args":["--dry-run=false"]
          }}
        }`)

	loaded, err := Load(directory, "home")
	if err != nil {
		t.Fatal(err)
	}
	if got := loaded.Copies["offsite"].Args; len(got) != 1 || got[0] != "--dry-run=false" {
		t.Fatalf("copy arguments = %q", got)
	}
}

func TestLoadRejectsAmbiguousCopyCommandArguments(t *testing.T) {
	directory := t.TempDir()
	writePrivate(t, filepath.Join(directory, "source.json"), `{"password":{"value":"source"}}`)
	writePrivate(t, filepath.Join(directory, "target.json"), `{"password":{"value":"target"}}`)
	writePrivate(t, filepath.Join(directory, "home.json"), `{
      "repository":"local:primary","credentials_file":"source.json",
      "commands":{"copy":{"args":["--host","legacy"]}},
      "copies":{"offsite":{"repository":"local:secondary","credentials_file":"target.json"}}}`)
	_, err := Load(directory, "home")
	if err == nil || !strings.Contains(err.Error(), "commands.copy cannot be combined") {
		t.Fatalf("Load error = %v", err)
	}
}

func TestCopyCredentialsFileDoesNotFlowThroughInheritance(t *testing.T) {
	directory := t.TempDir()
	writePrivate(t, filepath.Join(directory, "parent.json"), `{
      "repository":"local:parent","credentials_file":"parent.credentials.json",
      "copies":{"offsite":{"repository":"local:secondary","credentials_file":"offsite.credentials.json"}}}`)
	writePrivate(t, filepath.Join(directory, "child.json"), `{
      "parent":"parent","repository":"local:child","credentials_file":"child.credentials.json"}`)
	for _, name := range []string{"parent.credentials.json", "child.credentials.json", "offsite.credentials.json"} {
		writePrivate(t, filepath.Join(directory, name), `{"password":{"value":"secret"}}`)
	}
	_, err := Load(directory, "child")
	if err == nil || !strings.Contains(err.Error(), "copies.offsite.credentials_file is required") {
		t.Fatalf("Load error = %v", err)
	}
}

func TestRedactedResolvedProfileRedactsCopyRepository(t *testing.T) {
	value := Profile{Copies: map[string]CopyTarget{
		"offsite": {
			Repository: "rest:https://user:secret@example.test/repo?token=secret", CredentialsFile: "/private/copy.json",
			Credentials: RepositoryCredentials{Password: PasswordSource{Value: "secret"}},
		},
	}}
	encoded := RedactedResolvedProfile(value)
	target := encoded.Copies["offsite"]
	if strings.Contains(target.Repository, "secret") || target.CredentialsFile != redactedValue || target.Credentials.Password.Configured() {
		t.Fatalf("redacted target = %#v", target)
	}
}
