package databasebackup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"resticctl/internal/profile"
)

type recordedCall struct {
	args []string
	env  map[string]string
	cwd  string
}

type mysqlRunner struct {
	call       recordedCall
	optionPath string
	optionData string
	optionMode os.FileMode
	err        error
}

func (r *mysqlRunner) RunDatabase(_ context.Context, args []string, env map[string]string, cwd string) error {
	r.call = recordedCall{args: append([]string(nil), args...), env: env, cwd: cwd}
	r.optionPath = strings.TrimPrefix(args[1], "--defaults-extra-file=")
	data, err := os.ReadFile(r.optionPath)
	if err != nil {
		return err
	}
	info, err := os.Stat(r.optionPath)
	if err != nil {
		return err
	}
	r.optionData = string(data)
	r.optionMode = info.Mode().Perm()
	return r.err
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

func TestMySQLStagesRemoteDatabaseWithPrivateCredentials(t *testing.T) {
	runner := &mysqlRunner{}
	directory := t.TempDir()
	db := profile.MySQLDatabase{Name: "accounts", Database: "app", Host: "db.example", Port: 3307, Username: "backup", Executable: "mariadb-dump", Tables: []string{"customers", "orders"}, Routines: true, Events: true, Triggers: true, Args: []string{"--hex-blob"}}
	if err := (MySQL{Database: db}).Stage(context.Background(), runner, directory, map[string]string{"MYSQL_PASSWORD": "p\\\"a\nss", "UNRELATED": "secret"}); err != nil {
		t.Fatal(err)
	}
	want := []string{"mariadb-dump", "--defaults-extra-file=" + runner.optionPath, "--single-transaction", "--result-file=" + filepath.Join("databases", "accounts.sql"), "--host", "db.example", "--port", "3307", "--routines", "--events", "--triggers", "--hex-blob", "app", "customers", "orders"}
	if !slices.Equal(runner.call.args, want) {
		t.Fatalf("args = %#v", runner.call.args)
	}
	if runtime.GOOS != "windows" && runner.optionMode != 0o600 {
		t.Fatalf("option file mode=%#o", runner.optionMode)
	}
	if runner.optionData != "[client]\nuser=\"backup\"\npassword=\"p\\\\\\\"a\\nss\"\n" {
		t.Fatalf("option file mode=%#o data=%q", runner.optionMode, runner.optionData)
	}
	if len(runner.call.env) != 1 || runner.call.env["MYSQL_PWD"] != "" {
		t.Fatalf("environment = %#v", runner.call.env)
	}
	if _, err := os.Stat(runner.optionPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temporary option file remains: %v", err)
	}
}

func TestMySQLStagesSocketAndCleansOptionFileAfterFailure(t *testing.T) {
	runner := &mysqlRunner{err: errors.New("dump failed")}
	db := profile.MySQLDatabase{Name: "local", Database: "app", Socket: "/run/mysqld/mysqld.sock", Executable: "mysqldump"}
	err := (MySQL{Database: db}).Stage(context.Background(), runner, t.TempDir(), map[string]string{"MYSQL_PASSWORD": "private"})
	if err == nil || !strings.Contains(err.Error(), "dump MySQL database local") {
		t.Fatalf("error = %v", err)
	}
	wantConnection := []string{"--protocol=socket", "--socket", "/run/mysqld/mysqld.sock"}
	if !containsSequence(runner.call.args, wantConnection) || !slices.Contains(runner.call.args, "--skip-triggers") {
		t.Fatalf("args = %#v", runner.call.args)
	}
	if _, err := os.Stat(runner.optionPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temporary option file remains: %v", err)
	}
}

func containsSequence(values, sequence []string) bool {
	for i := 0; i+len(sequence) <= len(values); i++ {
		if slices.Equal(values[i:i+len(sequence)], sequence) {
			return true
		}
	}
	return false
}
