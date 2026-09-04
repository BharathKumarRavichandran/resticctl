package databasebackup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"resticctl/internal/profile"
	"resticctl/internal/securefile"
	"resticctl/internal/sqlitebackup"
)

// Runner executes a database client locally. Arguments are always passed as an
// argument vector; Environment contains private values loaded from credentials.
type Runner interface {
	RunDatabase(context.Context, []string, map[string]string, string) error
}

// Provider stages a consistent database dump below a backup staging directory.
type Provider interface {
	Name() string
	Progress() string
	Stage(context.Context, Runner, string, map[string]string) error
}

type SQLite struct{ Database profile.SQLiteDatabase }

func (s SQLite) Name() string { return s.Database.Name }

func (s SQLite) Progress() string { return "Snapshotting SQLite database: " + s.Database.Name }

func (s SQLite) Stage(ctx context.Context, _ Runner, directory string, _ map[string]string) error {
	destination := filepath.Join(directory, "databases", s.Database.Name+".sqlite3")
	temporary, err := temporaryArtifact(directory, s.Database.Name+"-*.sqlite3")
	if err != nil {
		return fmt.Errorf("prepare SQLite database %s: %w", s.Database.Name, err)
	}
	defer os.Remove(temporary)
	if err := sqlitebackup.Create(ctx, s.Database.Path, temporary); err != nil {
		return fmt.Errorf("snapshot SQLite database %s: %w", s.Database.Name, err)
	}
	if err := os.Rename(temporary, destination); err != nil {
		return fmt.Errorf("publish SQLite database %s: %w", s.Database.Name, err)
	}
	return nil
}

type PostgreSQL struct{ Database profile.PostgreSQLDatabase }

func (p PostgreSQL) Name() string { return p.Database.Name }

func (p PostgreSQL) Progress() string { return "" }

func (p PostgreSQL) Stage(ctx context.Context, runner Runner, directory string, environment map[string]string) error {
	db := p.Database
	dump := filepath.Join("databases", db.Name+".dump")
	temporaryDump, err := temporaryArtifact(directory, db.Name+"-*.dump")
	if err != nil {
		return fmt.Errorf("prepare PostgreSQL dump %s: %w", db.Name, err)
	}
	defer os.Remove(temporaryDump)
	args := []string{db.Executable, "--format=custom", "--file", temporaryDump}
	args = appendConnection(args, db.Host, db.Port, db.Username)
	for _, pattern := range db.TablePatterns {
		args = append(args, "--table="+pattern)
	}
	args = append(args, db.Args...)
	args = append(args, db.Database)
	if err := runner.RunDatabase(ctx, args, environment, directory); err != nil {
		return fmt.Errorf("dump PostgreSQL database %s: %w", db.Name, err)
	}
	if err := requireNonEmptyRegularFile(temporaryDump); err != nil {
		return fmt.Errorf("verify PostgreSQL database dump %s: %w", db.Name, err)
	}
	if err := os.Rename(temporaryDump, filepath.Join(directory, dump)); err != nil {
		return fmt.Errorf("publish PostgreSQL database dump %s: %w", db.Name, err)
	}
	if db.Globals {
		globalsDump := filepath.Join("databases", db.Name+"-globals.sql")
		temporaryGlobals, err := temporaryArtifact(directory, db.Name+"-globals-*.sql")
		if err != nil {
			return fmt.Errorf("prepare PostgreSQL globals %s: %w", db.Name, err)
		}
		defer os.Remove(temporaryGlobals)
		globals := []string{db.GlobalsExecutable, "--globals-only", "--file", temporaryGlobals}
		globals = appendConnection(globals, db.Host, db.Port, db.Username)
		if err := runner.RunDatabase(ctx, globals, environment, directory); err != nil {
			return fmt.Errorf("dump PostgreSQL globals %s: %w", db.Name, err)
		}
		if err := requireNonEmptyRegularFile(temporaryGlobals); err != nil {
			return fmt.Errorf("verify PostgreSQL globals dump %s: %w", db.Name, err)
		}
		if err := os.Rename(temporaryGlobals, filepath.Join(directory, globalsDump)); err != nil {
			return fmt.Errorf("publish PostgreSQL globals dump %s: %w", db.Name, err)
		}
	}
	if len(db.TablePatterns) > 0 {
		if err := writeSelectionManifest(directory, db.Name, "postgresql", db.Database, "table_patterns", db.TablePatterns); err != nil {
			return fmt.Errorf("record PostgreSQL selection %s: %w", db.Name, err)
		}
	}
	return nil
}

func appendConnection(args []string, host string, port int, username string) []string {
	if host != "" {
		args = append(args, "--host", host)
	}
	if port != 0 {
		args = append(args, "--port", strconv.Itoa(port))
	}
	if username != "" {
		args = append(args, "--username", username)
	}
	return args
}

type MongoDB struct{ Database profile.MongoDBDatabase }

func (m MongoDB) Name() string { return m.Database.Name }

func (m MongoDB) Progress() string { return "" }

func (m MongoDB) Stage(ctx context.Context, runner Runner, directory string, environment map[string]string) error {
	db := m.Database
	dump := filepath.Join("databases", db.Name)
	databaseDir := filepath.Join(directory, "databases")
	if err := os.MkdirAll(databaseDir, 0o700); err != nil {
		return fmt.Errorf("prepare MongoDB dump %s: %w", db.Name, err)
	}
	temporaryDump, err := os.MkdirTemp(databaseDir, "."+db.Name+"-*")
	if err != nil {
		return fmt.Errorf("prepare MongoDB dump %s: %w", db.Name, err)
	}
	defer os.RemoveAll(temporaryDump)
	args := []string{db.Executable, "--out", temporaryDump}
	if db.ConfigFile != "" {
		args = append(args, "--config", db.ConfigFile)
	}
	if db.Host != "" {
		args = append(args, "--host", db.Host)
	}
	if db.Port != 0 {
		args = append(args, "--port", strconv.Itoa(db.Port))
	}
	if db.Database != "" {
		args = append(args, "--db", db.Database)
	}
	if db.Collection != "" {
		args = append(args, "--collection", db.Collection)
	}
	for _, collection := range db.ExcludeCollections {
		args = append(args, "--excludeCollection", collection)
	}
	args = append(args, db.Args...)
	if err := runner.RunDatabase(ctx, args, environment, directory); err != nil {
		return fmt.Errorf("dump MongoDB database %s: %w", db.Name, err)
	}
	if err := requireMongoDumpDirectory(temporaryDump); err != nil {
		return fmt.Errorf("verify MongoDB database dump %s: %w", db.Name, err)
	}
	if err := os.Rename(temporaryDump, filepath.Join(directory, dump)); err != nil {
		return fmt.Errorf("publish MongoDB database dump %s: %w", db.Name, err)
	}
	if db.Collection != "" {
		if err := writeSelectionManifest(directory, db.Name, "mongodb", db.Database, "collection", []string{db.Collection}); err != nil {
			return fmt.Errorf("record MongoDB selection %s: %w", db.Name, err)
		}
	} else if len(db.ExcludeCollections) > 0 {
		if err := writeSelectionManifest(directory, db.Name, "mongodb", db.Database, "exclude_collections", db.ExcludeCollections); err != nil {
			return fmt.Errorf("record MongoDB selection %s: %w", db.Name, err)
		}
	}
	return nil
}

type MySQL struct{ Database profile.MySQLDatabase }

func (m MySQL) Name() string { return m.Database.Name }

func (m MySQL) Progress() string { return "" }

func (m MySQL) Stage(ctx context.Context, runner Runner, directory string, environment map[string]string) (stageErr error) {
	db := m.Database
	optionFile, err := os.CreateTemp(directory, ".mysql-client-*")
	if err != nil {
		return fmt.Errorf("create MySQL client option file for %s: %w", db.Name, err)
	}
	optionPath := optionFile.Name()
	defer func() {
		if err := os.Remove(optionPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			stageErr = errors.Join(stageErr, fmt.Errorf("remove MySQL client option file for %s: %w", db.Name, err))
		}
	}()
	if err := securefile.Protect(optionPath); err != nil {
		optionFile.Close()
		return fmt.Errorf("protect MySQL client option file for %s: %w", db.Name, err)
	}
	contents := mysqlOptionFile(db.Username, environment["MYSQL_PASSWORD"])
	if _, err := optionFile.Write(contents); err != nil {
		optionFile.Close()
		return fmt.Errorf("write MySQL client option file for %s: %w", db.Name, err)
	}
	if err := optionFile.Close(); err != nil {
		return fmt.Errorf("close MySQL client option file for %s: %w", db.Name, err)
	}

	temporaryDump, err := temporaryArtifact(directory, db.Name+"-*.sql")
	if err != nil {
		return fmt.Errorf("prepare MySQL dump %s: %w", db.Name, err)
	}
	defer os.Remove(temporaryDump)
	args := []string{db.Executable, "--defaults-extra-file=" + optionPath,
		"--single-transaction", "--result-file=" + temporaryDump}
	if db.Socket != "" {
		args = append(args, "--protocol=socket", "--socket", db.Socket)
	} else {
		if db.Host != "" {
			args = append(args, "--host", db.Host)
		}
		if db.Port != 0 {
			args = append(args, "--port", strconv.Itoa(db.Port))
		}
	}
	if db.Routines {
		args = append(args, "--routines")
	}
	if db.Events {
		args = append(args, "--events")
	}
	if db.Triggers {
		args = append(args, "--triggers")
	} else {
		args = append(args, "--skip-triggers")
	}
	args = append(args, db.Args...)
	args = append(args, db.Database)
	args = append(args, db.Tables...)
	// MYSQL_PWD is explicitly blanked so an ambient password cannot reach the
	// client. The configured password exists only in the temporary option file.
	if err := runner.RunDatabase(ctx, args, map[string]string{"MYSQL_PWD": ""}, directory); err != nil {
		return fmt.Errorf("dump MySQL database %s: %w", db.Name, err)
	}
	if err := requireNonEmptyRegularFile(temporaryDump); err != nil {
		return fmt.Errorf("verify MySQL database dump %s: %w", db.Name, err)
	}
	if err := os.Rename(temporaryDump, filepath.Join(directory, "databases", db.Name+".sql")); err != nil {
		return fmt.Errorf("publish MySQL database dump %s: %w", db.Name, err)
	}
	if len(db.Tables) > 0 {
		if err := writeSelectionManifest(directory, db.Name, "mysql", db.Database, "tables", db.Tables); err != nil {
			return fmt.Errorf("record MySQL selection %s: %w", db.Name, err)
		}
	}
	return nil
}

type selectionManifest struct {
	Provider string   `json:"provider"`
	Database string   `json:"database"`
	Kind     string   `json:"kind"`
	Values   []string `json:"values"`
}

func writeSelectionManifest(directory, name, provider, database, kind string, values []string) error {
	data, err := json.MarshalIndent(selectionManifest{Provider: provider, Database: database, Kind: kind, Values: values}, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	path := filepath.Join(directory, "databases", name+".selection.json")
	temporary, err := os.CreateTemp(filepath.Dir(path), "."+name+"-*.selection.json")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}

func temporaryArtifact(directory, pattern string) (string, error) {
	databaseDir := filepath.Join(directory, "databases")
	if err := os.MkdirAll(databaseDir, 0o700); err != nil {
		return "", err
	}
	file, err := os.CreateTemp(databaseDir, "."+pattern)
	if err != nil {
		return "", err
	}
	path := file.Name()
	if err := file.Close(); err != nil {
		os.Remove(path)
		return "", err
	}
	if err := os.Remove(path); err != nil {
		return "", err
	}
	return path, nil
}

func requireNonEmptyRegularFile(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("artifact is not a regular file")
	}
	if info.Size() == 0 {
		return errors.New("artifact is empty")
	}
	return nil
}

func requireMongoDumpDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errors.New("artifact is not a directory")
	}
	found := false
	err = filepath.WalkDir(path, func(_ string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if found || entry.IsDir() {
			return nil
		}
		entryInfo, infoErr := entry.Info()
		if infoErr != nil {
			return infoErr
		}
		if entryInfo.Mode().IsRegular() && entryInfo.Size() > 0 && isMongoDumpFile(entry.Name()) {
			found = true
		}
		return nil
	})
	if err != nil {
		return err
	}
	if !found {
		return errors.New("artifact directory contains no non-empty BSON or metadata files")
	}
	return nil
}

func isMongoDumpFile(name string) bool {
	return strings.HasSuffix(name, ".bson") || strings.HasSuffix(name, ".bson.gz") ||
		strings.HasSuffix(name, ".metadata.json") || strings.HasSuffix(name, ".metadata.json.gz")
}

func mysqlOptionFile(username, password string) []byte {
	var contents bytes.Buffer
	contents.WriteString("[client]\n")
	if username != "" {
		fmt.Fprintf(&contents, "user=\"%s\"\n", escapeMySQLOption(username))
	}
	if password != "" {
		fmt.Fprintf(&contents, "password=\"%s\"\n", escapeMySQLOption(password))
	}
	return contents.Bytes()
}

func escapeMySQLOption(value string) string {
	replacer := strings.NewReplacer("\\", "\\\\", "\"", "\\\"", "\n", "\\n", "\r", "\\r", "\t", "\\t")
	return replacer.Replace(value)
}
