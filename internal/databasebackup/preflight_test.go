package databasebackup

import (
	"errors"
	"strings"
	"testing"

	"resticctl/internal/profile"
)

func TestPreflightReportsAllMissingClients(t *testing.T) {
	configured := profile.Profile{
		PostgreSQLDatabases: []profile.PostgreSQLDatabase{{Executable: "pg-dump-missing", Globals: true, GlobalsExecutable: "pg-globals-missing"}},
		MongoDBDatabases:    []profile.MongoDBDatabase{{Executable: "mongo-dump-missing"}},
	}
	err := preflight(configured, func(name string) (string, error) { return "", errors.New("missing " + name) })
	if err == nil {
		t.Fatal("preflight succeeded")
	}
	for _, name := range []string{"pg-dump-missing", "pg-globals-missing", "mongo-dump-missing"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("error %q does not mention %s", err, name)
		}
	}
}

func TestPreflightOnlyRequiresGlobalsWhenEnabled(t *testing.T) {
	configured := profile.Profile{PostgreSQLDatabases: []profile.PostgreSQLDatabase{{Executable: "pg_dump", GlobalsExecutable: "pg_dumpall"}}}
	var lookedUp []string
	err := preflight(configured, func(name string) (string, error) { lookedUp = append(lookedUp, name); return name, nil })
	if err != nil {
		t.Fatal(err)
	}
	if len(lookedUp) != 1 || lookedUp[0] != "pg_dump" {
		t.Fatalf("lookups = %v", lookedUp)
	}
}
