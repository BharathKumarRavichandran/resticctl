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
	if r.err != nil {
		return r.err
	}
	return createFakeArtifact(args, cwd)
}

type fakeRunner struct{ calls []recordedCall }

func (r *fakeRunner) RunDatabase(_ context.Context, args []string, env map[string]string, cwd string) error {
	r.calls = append(r.calls, recordedCall{append([]string(nil), args...), env, cwd})
	return createFakeArtifact(args, cwd)
}

type noArtifactRunner struct{}

func (noArtifactRunner) RunDatabase(context.Context, []string, map[string]string, string) error {
	return nil
}

type artifactThenErrorRunner struct{}

func (artifactThenErrorRunner) RunDatabase(_ context.Context, args []string, _ map[string]string, cwd string) error {
	if err := createFakeArtifact(args, cwd); err != nil {
		return err
	}
	return errors.New("client failed")
}

func createFakeArtifact(args []string, cwd string) error {
	for i, argument := range args {
		var path string
		switch {
		case argument == "--file" && i+1 < len(args):
			path = args[i+1]
		case strings.HasPrefix(argument, "--result-file="):
			path = strings.TrimPrefix(argument, "--result-file=")
		case argument == "--out" && i+1 < len(args):
			path = filepath.Join(args[i+1], "dump.bson")
		}
		if path == "" {
			continue
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(cwd, path)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte("dump"), 0o600); err != nil {
			return err
		}
	}
	return nil
}

func TestPostgreSQLStagesRemoteDatabaseAndGlobals(t *testing.T) {
	runner := &fakeRunner{}
	directory := t.TempDir()
	env := map[string]string{"PGPASSWORD": "private"}
	db := profile.PostgreSQLDatabase{Name: "accounts", Database: "app", Host: "db.example", Port: 5433, Username: "backup", Executable: "/opt/pg_dump", GlobalsExecutable: "/opt/pg_dumpall", Globals: true, Args: []string{"--no-owner"}, TablePatterns: []string{"public.customers", "public.orders"}}
	if err := (PostgreSQL{Database: db}).Stage(context.Background(), runner, directory, env); err != nil {
		t.Fatal(err)
	}
	want := []string{"/opt/pg_dump", "--format=custom", "--file", runner.calls[0].args[3], "--host", "db.example", "--port", "5433", "--username", "backup", "--table=public.customers", "--table=public.orders", "--no-owner", "app"}
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
	want := []string{"mongodump", "--out", runner.calls[0].args[2], "--config", "/private/mongo.yml", "--host", "/var/run/mongodb/mongodb.sock", "--db", "events", "--oplog"}
	if len(runner.calls) != 1 || !slices.Equal(runner.calls[0].args, want) {
		t.Fatalf("calls = %#v", runner.calls)
	}
}

func TestMongoDBStagesSelectedCollection(t *testing.T) {
	runner := &fakeRunner{}
	directory := t.TempDir()
	db := profile.MongoDBDatabase{Name: "events", Database: "events", Executable: "mongodump", Collection: "activity"}
	if err := (MongoDB{Database: db}).Stage(context.Background(), runner, directory, nil); err != nil {
		t.Fatal(err)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("calls = %#v", runner.calls)
	}
	args := runner.calls[0].args
	if !containsSequence(args, []string{"--db", "events", "--collection", "activity"}) {
		t.Fatalf("args = %#v", args)
	}
	manifest, err := os.ReadFile(filepath.Join(directory, "databases", "events.selection.json"))
	if err != nil || !strings.Contains(string(manifest), `"values": [`) || !strings.Contains(string(manifest), `"activity"`) {
		t.Fatalf("selection manifest = %q, %v", manifest, err)
	}
}

func TestMongoDBStagesDatabaseWithExcludedCollections(t *testing.T) {
	runner := &fakeRunner{}
	directory := t.TempDir()
	db := profile.MongoDBDatabase{
		Name: "events", Database: "events", Executable: "mongodump",
		ExcludeCollections: []string{"temporary", "cache"},
	}
	if err := (MongoDB{Database: db}).Stage(context.Background(), runner, directory, nil); err != nil {
		t.Fatal(err)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("calls = %#v", runner.calls)
	}
	args := runner.calls[0].args
	if !containsSequence(args, []string{"--db", "events", "--excludeCollection", "temporary", "--excludeCollection", "cache"}) {
		t.Fatalf("args = %#v", args)
	}
	manifest, err := os.ReadFile(filepath.Join(directory, "databases", "events.selection.json"))
	if err != nil || !strings.Contains(string(manifest), `"kind": "exclude_collections"`) || !strings.Contains(string(manifest), `"cache"`) {
		t.Fatalf("selection manifest = %q, %v", manifest, err)
	}
}

func TestMySQLStagesRemoteDatabaseWithPrivateCredentials(t *testing.T) {
	runner := &mysqlRunner{}
	directory := t.TempDir()
	db := profile.MySQLDatabase{Name: "accounts", Database: "app", Host: "db.example", Port: 3307, Username: "backup", Executable: "mariadb-dump", Tables: []string{"customers", "orders"}, Routines: true, Events: true, Triggers: true, Args: []string{"--hex-blob"}}
	if err := (MySQL{Database: db}).Stage(context.Background(), runner, directory, map[string]string{"MYSQL_PASSWORD": "p\\\"a\nss", "UNRELATED": "secret"}); err != nil {
		t.Fatal(err)
	}
	want := []string{"mariadb-dump", "--defaults-extra-file=" + runner.optionPath, "--single-transaction", runner.call.args[3], "--host", "db.example", "--port", "3307", "--routines", "--events", "--triggers", "--hex-blob", "app", "customers", "orders"}
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

func TestExternalProvidersRejectMissingArtifactsAfterSuccessfulClient(t *testing.T) {
	tests := []struct {
		name     string
		provider Provider
	}{
		{"postgresql", PostgreSQL{Database: profile.PostgreSQLDatabase{Name: "pg", Database: "app", Executable: "pg_dump"}}},
		{"mongodb", MongoDB{Database: profile.MongoDBDatabase{Name: "mongo", Database: "app", Executable: "mongodump"}}},
		{"mysql", MySQL{Database: profile.MySQLDatabase{Name: "mysql", Database: "app", Executable: "mysqldump"}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			if err := os.Mkdir(filepath.Join(directory, "databases"), 0o700); err != nil {
				t.Fatal(err)
			}
			err := test.provider.Stage(context.Background(), noArtifactRunner{}, directory, nil)
			if err == nil || !strings.Contains(err.Error(), "verify") {
				t.Fatalf("Stage error = %v", err)
			}
			entries, readErr := os.ReadDir(filepath.Join(directory, "databases"))
			if readErr != nil || len(entries) != 0 {
				t.Fatalf("unverified artifacts were published: %v, %v", entries, readErr)
			}
		})
	}
}

func TestMongoDBDoesNotPublishArtifactAfterClientFailure(t *testing.T) {
	directory := t.TempDir()
	databaseDir := filepath.Join(directory, "databases")
	if err := os.Mkdir(databaseDir, 0o700); err != nil {
		t.Fatal(err)
	}
	db := profile.MongoDBDatabase{Name: "events", Database: "events", Collection: "activity", Executable: "mongodump"}
	err := (MongoDB{Database: db}).Stage(context.Background(), artifactThenErrorRunner{}, directory, nil)
	if err == nil || !strings.Contains(err.Error(), "client failed") {
		t.Fatalf("Stage error = %v", err)
	}
	entries, err := os.ReadDir(databaseDir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("failed artifact was published: %v, %v", entries, err)
	}
}

func TestArtifactValidationRejectsEmptyAndNonRegularOutputs(t *testing.T) {
	directory := t.TempDir()
	empty := filepath.Join(directory, "empty")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := requireNonEmptyRegularFile(empty); err == nil {
		t.Fatal("empty file was accepted")
	}
	if err := requireNonEmptyRegularFile(directory); err == nil {
		t.Fatal("directory was accepted as a dump file")
	}
	if err := requireMongoDumpDirectory(directory); err == nil {
		t.Fatal("directory without a non-empty dump file was accepted")
	}
	if err := os.WriteFile(filepath.Join(directory, "help.txt"), []byte("usage"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := requireMongoDumpDirectory(directory); err == nil {
		t.Fatal("unrecognized MongoDB output was accepted")
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
