package profile

import (
	"path/filepath"
	"testing"
)

func TestExplainInheritanceMatchesMergeRules(t *testing.T) {
	directory := t.TempDir()
	writePrivate(t, filepath.Join(directory, "base.json"), `{
        "repository":"local:base",
        "tags":["base"],
        "schedule":{"cron":"0 2 * * *"},
        "databases":{"postgresql":{"accounts":{"connection":{
          "database":"accounts",
          "password":{"command":["old-password"]}
        }}}}
      }`)
	writePrivate(t, filepath.Join(directory, "middle.json"), `{
        "parent":"base",
        "tags":["middle"]
      }`)
	writePrivate(t, filepath.Join(directory, "child.json"), `{
        "parent":"middle",
        "repository":"local:child",
        "credentials":{"password":{"value":"repository-secret"}},
        "schedule":null,
        "databases":{"postgresql":{"accounts":{"connection":{
          "password":{"value":"database-secret"}
        }}}}
      }`)

	explanations, err := ExplainInheritance(directory, "child")
	if err != nil {
		t.Fatal(err)
	}
	byPath := make(map[string]FieldExplanation, len(explanations))
	for _, explanation := range explanations {
		byPath[explanation.Path] = explanation
	}
	assertExplanation(t, byPath, "repository", "child", "overridden")
	assertExplanation(t, byPath, "tags", "middle", "inherited")
	assertExplanation(t, byPath, "schedule", "child", "cleared")
	assertExplanation(t, byPath, "databases.postgresql.accounts.connection.database", "base", "inherited")
	assertExplanation(t, byPath, "databases.postgresql.accounts.connection.password", "child", "overridden")
	if _, exists := byPath["databases.postgresql.accounts.connection.password.command"]; exists {
		t.Fatal("replaced password command was reported as inherited")
	}
	if _, exists := byPath["databases.postgresql.accounts.connection.password.value"]; exists {
		t.Fatal("atomic password object was expanded into secret-bearing structure")
	}
}

func TestExplainInheritanceDoesNotInheritCredentialFields(t *testing.T) {
	directory := t.TempDir()
	writePrivate(t, filepath.Join(directory, "base.json"), `{
        "credentials_file":"base.credentials.json",
        "private_file":"base.private.json"
      }`)
	writePrivate(t, filepath.Join(directory, "child.json"), `{
        "parent":"base",
        "credentials_file":"child.credentials.json"
      }`)

	explanations, err := ExplainInheritance(directory, "child")
	if err != nil {
		t.Fatal(err)
	}
	byPath := make(map[string]FieldExplanation, len(explanations))
	for _, explanation := range explanations {
		byPath[explanation.Path] = explanation
	}
	assertExplanation(t, byPath, "credentials_file", "child", "defined")
	if _, exists := byPath["private_file"]; exists {
		t.Fatal("parent private_file was reported as inherited")
	}
}

func assertExplanation(t *testing.T, explanations map[string]FieldExplanation, path, source, action string) {
	t.Helper()
	explanation, exists := explanations[path]
	if !exists {
		t.Fatalf("missing explanation for %s", path)
	}
	if explanation.Source != source || explanation.Action != action {
		t.Fatalf("%s explanation = %#v, want source %q action %q", path, explanation, source, action)
	}
}
