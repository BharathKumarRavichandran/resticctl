package databasebackup

import (
	"context"
	"path/filepath"
	"slices"
	"testing"

	"resticctl/internal/profile"
)

type recordedCall struct {
	args []string
	env  map[string]string
	cwd  string
}
type fakeRunner struct{ calls []recordedCall }

func (r *fakeRunner) RunDatabase(_ context.Context, args []string, env map[string]string, cwd string) error {
	r.calls = append(r.calls, recordedCall{append([]string(nil), args...), env, cwd})
	return nil
}

func TestPostgreSQLStagesRemoteDatabaseAndGlobals(t *testing.T) {
	runner := &fakeRunner{}
	directory := t.TempDir()
	env := map[string]string{"PGPASSWORD": "private"}
	db := profile.PostgreSQLDatabase{Name: "accounts", Database: "app", Host: "db.example", Port: 5433, Username: "backup", Executable: "/opt/pg_dump", GlobalsExecutable: "/opt/pg_dumpall", Globals: true, Args: []string{"--no-owner"}}
	if err := (PostgreSQL{Database: db}).Stage(context.Background(), runner, directory, env); err != nil {
		t.Fatal(err)
	}
	want := []string{"/opt/pg_dump", "--format=custom", "--file", filepath.Join("databases", "accounts.dump"), "--host", "db.example", "--port", "5433", "--username", "backup", "--no-owner", "app"}
	if len(runner.calls) != 2 || !slices.Equal(runner.calls[0].args, want) {
		t.Fatalf("calls = %#v", runner.calls)
	}
	if runner.calls[0].env["PGPASSWORD"] != "private" || slices.Contains(runner.calls[0].args, "private") {
		t.Fatal("password was not confined to the environment")
	}
}

func TestMongoDBStagesLocalSocketConfiguration(t *testing.T) {
	runner := &fakeRunner{}
	directory := t.TempDir()
	db := profile.MongoDBDatabase{Name: "events", Database: "events", Host: "/var/run/mongodb/mongodb.sock", Executable: "mongodump", ConfigFile: "/private/mongo.yml", Args: []string{"--oplog"}}
	if err := (MongoDB{Database: db}).Stage(context.Background(), runner, directory, nil); err != nil {
		t.Fatal(err)
	}
	want := []string{"mongodump", "--out", filepath.Join("databases", "events"), "--config", "/private/mongo.yml", "--host", "/var/run/mongodb/mongodb.sock", "--db", "events", "--oplog"}
	if len(runner.calls) != 1 || !slices.Equal(runner.calls[0].args, want) {
		t.Fatalf("calls = %#v", runner.calls)
	}
}
