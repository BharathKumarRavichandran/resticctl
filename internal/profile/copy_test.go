package profile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSameRepositoryLocalAliases(t *testing.T) {
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for _, pair := range [][2]string{
		{"local:primary", filepath.Join(workingDirectory, "primary")},
		{"./primary", "local:primary"},
	} {
		if !sameRepository(pair[0], pair[1]) {
			t.Errorf("repositories %q and %q were treated as distinct", pair[0], pair[1])
		}
	}
	directory := t.TempDir()
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(directory, alias); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if !sameRepository(directory, "local:"+alias) {
		t.Fatal("symlink alias was treated as a distinct repository")
	}
	if sameRepository("s3:bucket/primary", "s3:bucket/secondary") || sameRepository("local:primary", "local:secondary") {
		t.Fatal("distinct repositories were treated as identical")
	}
}

func TestLoadNamedCopyTargets(t *testing.T) {
	directory := t.TempDir()
	writePrivate(t, filepath.Join(directory, "source.json"), `{"credentials":{"password":{"value":"source-secret"},"environment":{"SOURCE_TOKEN":"one"}}}`)
	writePrivate(t, filepath.Join(directory, "offsite.json"), `{"credentials":{"password":{"value":"target-secret"},"environment":{"TARGET_TOKEN":"two"}}}`)
	writePrivate(t, filepath.Join(directory, "home.json"), `{
          "repository":"local:primary", "private_file":"source.json",
          "copies":{"offsite":{"repository":"local:secondary","private_file":"offsite.json",
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
	if !filepath.IsAbs(target.PrivateFile) {
		t.Fatalf("credentials path = %q", target.PrivateFile)
	}
}

func TestLoadRejectsUnsafeCopyTargets(t *testing.T) {
	for _, test := range []struct{ name, target, want string }{
		{"same repository", `"repository":"local:primary","private_file":"target.json"`, "must differ"},
		{"missing credentials", `"repository":"local:secondary"`, "set private_file or valid inline credentials"},
		{"reserved argument", `"repository":"local:secondary","private_file":"target.json","args":["--from-repo=evil"]`, "must not override"},
		{"dry-run argument", `"repository":"local:secondary","private_file":"target.json","args":["--dry-run"]`, "workflow-owned dry-run"},
		{"chunker without init", `"repository":"local:secondary","private_file":"target.json","copy_chunker_params":true`, "requires initialize"},
	} {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			writePrivate(t, filepath.Join(directory, "source.json"), `{"credentials":{"password":{"value":"source"}}}`)
			writePrivate(t, filepath.Join(directory, "target.json"), `{"credentials":{"password":{"value":"target"}}}`)
			writePrivate(t, filepath.Join(directory, "home.json"), `{"repository":"local:primary","private_file":"source.json","copies":{"offsite":{`+test.target+`}}}`)
			_, err := Load(directory, "home")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Load error = %v", err)
			}
		})
	}
}

func TestLoadAllowsDisabledCopyDryRunArgument(t *testing.T) {
	directory := t.TempDir()
	writePrivate(t, filepath.Join(directory, "source.json"), `{"credentials":{"password":{"value":"source"}}}`)
	writePrivate(t, filepath.Join(directory, "target.json"), `{"credentials":{"password":{"value":"target"}}}`)
	writePrivate(t, filepath.Join(directory, "home.json"), `{
          "repository":"local:primary",
          "private_file":"source.json",
          "copies":{"offsite":{
            "repository":"local:secondary",
            "private_file":"target.json",
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
	writePrivate(t, filepath.Join(directory, "source.json"), `{"credentials":{"password":{"value":"source"}}}`)
	writePrivate(t, filepath.Join(directory, "target.json"), `{"credentials":{"password":{"value":"target"}}}`)
	writePrivate(t, filepath.Join(directory, "home.json"), `{
      "repository":"local:primary","private_file":"source.json",
      "commands":{"copy":{"args":["--host","legacy"]}},
      "copies":{"offsite":{"repository":"local:secondary","private_file":"target.json"}}}`)
	_, err := Load(directory, "home")
	if err == nil || !strings.Contains(err.Error(), "commands.copy cannot be combined") {
		t.Fatalf("Load error = %v", err)
	}
}

func TestCopyPrivateFileDoesNotFlowThroughInheritance(t *testing.T) {
	directory := t.TempDir()
	writePrivate(t, filepath.Join(directory, "parent.json"), `{
      "repository":"local:parent","private_file":"parent.credentials.json",
      "copies":{"offsite":{"repository":"local:secondary","private_file":"offsite.credentials.json"}}}`)
	writePrivate(t, filepath.Join(directory, "child.json"), `{
      "parent":"parent","repository":"local:child","private_file":"child.credentials.json"}`)
	for _, name := range []string{"parent.credentials.json", "child.credentials.json", "offsite.credentials.json"} {
		writePrivate(t, filepath.Join(directory, name), `{"credentials":{"password":{"value":"secret"}}}`)
	}
	_, err := Load(directory, "child")
	if err == nil || !strings.Contains(err.Error(), "copies.offsite: set private_file or valid inline credentials") {
		t.Fatalf("Load error = %v", err)
	}
}

func TestRedactedResolvedProfileRedactsCopyRepository(t *testing.T) {
	value := Profile{Copies: map[string]CopyTarget{
		"offsite": {
			Repository: "rest:https://user:secret@example.test/repo?token=secret", PrivateFile: "/private/copy.json",
			Credentials: RepositoryCredentials{Password: PasswordSource{Value: "secret"}},
		},
	}}
	encoded := RedactedResolvedProfile(value)
	target := encoded.Copies["offsite"]
	if target.Repository != redactedValue || target.PrivateFile != redactedValue || target.Credentials.Password.Configured() {
		t.Fatalf("redacted target = %#v", target)
	}
}

func TestLoadCopyPrivateRepositoryOverride(t *testing.T) {
	directory := t.TempDir()
	writePrivate(t, filepath.Join(directory, "target.private.json"), `{"repository":"local:secondary","credentials":{"password":{"value":"target"}}}`)
	writePrivate(t, filepath.Join(directory, "home.json"), `{
		"repository":"local:primary","credentials":{"password":{"value":"source"}},
		"copies":{"offsite":{"private_file":"target.private.json"}}
	}`)
	loaded, err := Load(directory, "home")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Copies["offsite"].Repository != "local:secondary" {
		t.Fatalf("copy repository = %q", loaded.Copies["offsite"].Repository)
	}
}

func TestCopyInlineCredentialsDoNotFlowThroughInheritance(t *testing.T) {
	directory := t.TempDir()
	writePrivate(t, filepath.Join(directory, "parent.json"), `{
		"repository":"local:primary",
		"copies":{"offsite":{"repository":"local:secondary","Credentials":{"password":{"value":"parent-secret"}}}}
	}`)
	writePrivate(t, filepath.Join(directory, "child.json"), `{"parent":"parent","credentials":{"password":{"value":"source"}}}`)
	_, err := Load(directory, "child")
	if err == nil || !strings.Contains(err.Error(), "copies.offsite: set private_file or valid inline credentials") {
		t.Fatalf("Load error = %v", err)
	}
}

func TestLoadCanDisableInheritedCopyTarget(t *testing.T) {
	directory := t.TempDir()
	writePrivate(t, filepath.Join(directory, "parent.json"), `{"copies":{"offsite":{"repository":"local:secondary"}}}`)
	writePrivate(t, filepath.Join(directory, "child.json"), `{"parent":"parent","repository":"local:primary","credentials":{"password":{"value":"source"}},"copies":{"offsite":null}}`)
	loaded, err := Load(directory, "child")
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Copies) != 0 {
		t.Fatalf("disabled copies = %#v", loaded.Copies)
	}
}
