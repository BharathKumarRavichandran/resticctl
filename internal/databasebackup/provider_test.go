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
	"resticctl/internal/securefile"
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

type sqlServerRunner struct{ call recordedCall }

func (r *sqlServerRunner) RunDatabase(_ context.Context, args []string, env map[string]string, cwd string) error {
	r.call = recordedCall{args: append([]string(nil), args...), env: env, cwd: cwd}
	path, err := sqlServerOutputPath(args)
	if err != nil {
		return err
	}
	return os.WriteFile(path, []byte("dump"), 0o600)
}

type lockedSQLServerRunner struct{ directory string }

func (r lockedSQLServerRunner) RunDatabase(_ context.Context, args []string, _ map[string]string, _ string) error {
	path, err := sqlServerOutputPath(args)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte("dump"), 0o600); err != nil {
		return err
	}
	return os.Chmod(r.directory, 0o500)
}

func sqlServerOutputPath(args []string) (string, error) {
	query := args[len(args)-1]
	const marker = " TO DISK = N'"
	start := strings.Index(query, marker)
	if start < 0 {
		return "", errors.New("missing SQL Server output path")
	}
	path := query[start+len(marker):]
	end := strings.Index(path, "' WITH COPY_ONLY")
	if end < 0 {
		return "", errors.New("malformed SQL Server output path")
	}
	path = path[:end]
	return strings.ReplaceAll(path, "''", "'"), nil
}

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

func TestPostgreSQLStagesUsingMultipleHosts(t *testing.T) {
	runner := &fakeRunner{}
	directory := t.TempDir()
	db := profile.PostgreSQLDatabase{Name: "accounts", Database: "app", Hosts: []string{"pg1.example:5432", "pg2.example:5433"}, Username: "backup", Executable: "pg_dump"}
	if err := (PostgreSQL{Database: db}).Stage(context.Background(), runner, directory, nil); err != nil {
		t.Fatal(err)
	}
	if !containsSequence(runner.calls[0].args, []string{"--host", "pg1.example,pg2.example", "--port", "5432,5433", "--username", "backup"}) {
		t.Fatalf("args = %#v", runner.calls[0].args)
	}
}

func TestPostgreSQLSingleHostWithoutPortUsesClientDefault(t *testing.T) {
	runner := &fakeRunner{}
	directory := t.TempDir()
	db := profile.PostgreSQLDatabase{Name: "accounts", Database: "app", Hosts: []string{"pg.example"}, Executable: "pg_dump"}
	if err := (PostgreSQL{Database: db}).Stage(context.Background(), runner, directory, nil); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(runner.calls[0].args, "--port") {
		t.Fatalf("args unexpectedly override the client port: %#v", runner.calls[0].args)
	}
}

func TestPostgreSQLCanRequirePrimary(t *testing.T) {
	runner := &fakeRunner{}
	directory := t.TempDir()
	db := profile.PostgreSQLDatabase{
		Name: "accounts", Database: "app", Hosts: []string{"pg1.example", "pg2.example"}, Executable: "pg_dump",
		Options: &profile.PostgreSQLOptions{RequirePrimary: true},
	}
	if err := (PostgreSQL{Database: db}).Stage(context.Background(), runner, directory, map[string]string{"PGTARGETSESSIONATTRS": "standby"}); err != nil {
		t.Fatal(err)
	}
	if runner.calls[0].env["PGTARGETSESSIONATTRS"] != "read-write" {
		t.Fatalf("environment = %#v", runner.calls[0].env)
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

func TestMongoDBStagesReplicaSetSeeds(t *testing.T) {
	runner := &fakeRunner{}
	directory := t.TempDir()
	db := profile.MongoDBDatabase{Name: "events", Database: "events", Hosts: []string{"mongo1.example:27017", "mongo2.example:27018"}, Options: &profile.MongoDBOptions{ReplicaSet: "rs0"}, Executable: "mongodump"}
	if err := (MongoDB{Database: db}).Stage(context.Background(), runner, directory, nil); err != nil {
		t.Fatal(err)
	}
	if !containsSequence(runner.calls[0].args, []string{"--host", "rs0/mongo1.example:27017,mongo2.example:27018"}) {
		t.Fatalf("args = %#v", runner.calls[0].args)
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
	if len(runner.call.env) != 2 || runner.call.env["MYSQL_PWD"] != "" || runner.call.env["UNRELATED"] != "secret" {
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

func TestSQLServerStagesCompressedNativeBackupWithPrivateCredentials(t *testing.T) {
	runner := &sqlServerRunner{}
	directory := t.TempDir()
	db := profile.SQLServerDatabase{Name: "warehouse", Database: "report]ing", BackupDirectory: protectedTempDir(t), Host: "db.example", Port: 1433, Username: "backup", Executable: "/opt/sqlcmd", Compress: true, Args: []string{"-l", "30"}}
	if err := (SQLServer{Database: db}).Stage(context.Background(), runner, directory, map[string]string{"SQLSERVER_PASSWORD": "private", "UNRELATED": "secret"}); err != nil {
		t.Fatal(err)
	}
	wantPrefix := []string{"/opt/sqlcmd", "-b", "-r", "1", "-x", "-S", "db.example,1433", "-U", "backup", "-l", "30", "-d", "master", "-Q"}
	if !slices.Equal(runner.call.args[:len(wantPrefix)], wantPrefix) {
		t.Fatalf("args = %#v", runner.call.args)
	}
	query := runner.call.args[len(runner.call.args)-1]
	if !strings.HasPrefix(query, "BACKUP DATABASE [report]]ing] TO DISK = N'") || !strings.HasSuffix(query, "' WITH COPY_ONLY, CHECKSUM, COMPRESSION") {
		t.Fatalf("query = %q", query)
	}
	if len(runner.call.env) != 2 || runner.call.env["SQLCMDPASSWORD"] != "private" || runner.call.env["UNRELATED"] != "secret" || slices.Contains(runner.call.args, "private") {
		t.Fatalf("credentials leaked: args=%#v env=%#v", runner.call.args, runner.call.env)
	}
	if err := requireNonEmptyRegularFile(filepath.Join(directory, "databases", "warehouse.bak")); err != nil {
		t.Fatal(err)
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
		{"sqlserver", SQLServer{Database: profile.SQLServerDatabase{Name: "sqlserver", Database: "app", BackupDirectory: protectedTempDir(t), Executable: "sqlcmd"}}},
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

func TestSQLServerRejectsPublicBackupDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission test")
	}
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o777); err != nil {
		t.Fatal(err)
	}
	db := profile.SQLServerDatabase{Name: "warehouse", Database: "reporting", BackupDirectory: directory, Executable: "sqlcmd"}
	err := (SQLServer{Database: db}).Stage(context.Background(), noArtifactRunner{}, t.TempDir(), nil)
	if err == nil || !strings.Contains(err.Error(), "must not be accessible by other users") {
		t.Fatalf("Stage error = %v", err)
	}
}

func TestSQLServerReportsSourceCleanupFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission test")
	}
	backupDirectory := protectedTempDir(t)
	db := profile.SQLServerDatabase{Name: "warehouse", Database: "reporting", BackupDirectory: backupDirectory, Executable: "sqlcmd"}
	err := (SQLServer{Database: db}).Stage(context.Background(), lockedSQLServerRunner{directory: backupDirectory}, t.TempDir(), nil)
	if chmodErr := os.Chmod(backupDirectory, 0o700); chmodErr != nil {
		t.Fatal(chmodErr)
	}
	if err == nil || !strings.Contains(err.Error(), "remove SQL Server dump") {
		t.Fatalf("Stage error = %v", err)
	}
}

func protectedTempDir(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	if err := securefile.Protect(directory); err != nil {
		t.Fatal(err)
	}
	return directory
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
