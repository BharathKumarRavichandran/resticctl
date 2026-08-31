package databasebackup

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"

	"resticctl/internal/profile"
)

// Runner executes a database client locally. Arguments are always passed as an
// argument vector; Environment contains private values loaded from credentials.
type Runner interface {
	RunDatabase(context.Context, []string, map[string]string, string) error
}

// Provider stages a consistent database dump below a backup staging directory.
type Provider interface {
	Stage(context.Context, Runner, string, map[string]string) error
}

type PostgreSQL struct{ Database profile.PostgreSQLDatabase }

func (p PostgreSQL) Stage(ctx context.Context, runner Runner, directory string, environment map[string]string) error {
	db := p.Database
	args := []string{db.Executable, "--format=custom", "--file", filepath.Join("databases", db.Name+".dump")}
	args = appendConnection(args, db.Host, db.Port, db.Username)
	args = append(args, db.Args...)
	args = append(args, db.Database)
	if err := runner.RunDatabase(ctx, args, environment, directory); err != nil {
		return fmt.Errorf("dump PostgreSQL database %s: %w", db.Name, err)
	}
	if db.Globals {
		globals := []string{db.GlobalsExecutable, "--globals-only", "--file", filepath.Join("databases", db.Name+"-globals.sql")}
		globals = appendConnection(globals, db.Host, db.Port, db.Username)
		if err := runner.RunDatabase(ctx, globals, environment, directory); err != nil {
			return fmt.Errorf("dump PostgreSQL globals %s: %w", db.Name, err)
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

func (m MongoDB) Stage(ctx context.Context, runner Runner, directory string, environment map[string]string) error {
	db := m.Database
	args := []string{db.Executable, "--out", filepath.Join("databases", db.Name)}
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
	args = append(args, db.Args...)
	if err := runner.RunDatabase(ctx, args, environment, directory); err != nil {
		return fmt.Errorf("dump MongoDB database %s: %w", db.Name, err)
	}
	return nil
}
