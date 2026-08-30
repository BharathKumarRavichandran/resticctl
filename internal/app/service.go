package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func Backup(ctx context.Context, runner Runner, profile Profile, dryRun bool, output io.Writer) (backupErr error) {
	if len(profile.BackupPaths) == 0 && len(profile.SQLiteDatabases) == 0 {
		return errors.New("profile has no backup_paths or sqlite_databases")
	}
	for _, path := range profile.BackupPaths {
		if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("backup path not found: %s", path)
		} else if err != nil {
			return fmt.Errorf("cannot inspect backup path %s: %w", path, err)
		}
	}

	arguments := []string{"backup", "--group-by", "host,tags", "--tag", "profile:" + profile.Name}
	for _, tag := range profile.Tags {
		arguments = append(arguments, "--tag", tag)
	}
	arguments = append(arguments, profile.BackupArgs...)
	if dryRun {
		arguments = append(arguments, "--dry-run")
	}
	if len(profile.SQLiteDatabases) == 0 {
		arguments = append(arguments, "--")
		arguments = append(arguments, profile.BackupPaths...)
		return runner.Run(ctx, profile, arguments, "")
	}

	staging, err := os.MkdirTemp("", "resticctl-"+profile.Name+"-")
	if err != nil {
		return fmt.Errorf("cannot create SQLite staging directory: %w", err)
	}
	defer func() {
		if err := os.RemoveAll(staging); err != nil {
			backupErr = errors.Join(backupErr, fmt.Errorf("cannot remove SQLite staging directory %s: %w", staging, err))
		}
	}()
	if err := os.Chmod(staging, 0o700); err != nil {
		return fmt.Errorf("cannot protect SQLite staging directory: %w", err)
	}
	databaseDir := filepath.Join(staging, "databases")
	if err := os.Mkdir(databaseDir, 0o700); err != nil {
		return fmt.Errorf("cannot create SQLite staging directory: %w", err)
	}
	for _, database := range profile.SQLiteDatabases {
		fmt.Fprintf(output, "==> Snapshotting SQLite database: %s\n", database.Name)
		if err := CreateSnapshot(ctx, database, filepath.Join(databaseDir, database.Name+".sqlite3")); err != nil {
			return err
		}
	}
	arguments = append(arguments, "--")
	arguments = append(arguments, profile.BackupPaths...)
	arguments = append(arguments, "databases")
	return runner.Run(ctx, profile, arguments, staging)
}

func Snapshots(ctx context.Context, runner Runner, profile Profile) error {
	return runner.Run(ctx, profile, []string{"snapshots", "--tag", "profile:" + profile.Name}, "")
}

func Check(ctx context.Context, runner Runner, profile Profile) error {
	return runner.Run(ctx, profile, append([]string{"check"}, profile.CheckArgs...), "")
}

func Forget(ctx context.Context, runner Runner, profile Profile, dryRun, prune bool) error {
	if len(profile.ForgetArgs) == 0 {
		return errors.New("profile has no forget_args")
	}
	arguments := []string{"forget", "--tag", "profile:" + profile.Name, "--group-by", "host,tags"}
	arguments = append(arguments, profile.ForgetArgs...)
	if prune {
		arguments = append(arguments, "--prune")
	}
	if dryRun {
		arguments = append(arguments, "--dry-run")
	}
	return runner.Run(ctx, profile, arguments, "")
}

func Restore(ctx context.Context, runner Runner, profile Profile, snapshot, target string, dryRun bool) error {
	arguments := []string{"restore", snapshot, "--tag", "profile:" + profile.Name, "--target", target}
	if dryRun {
		arguments = append(arguments, "--dry-run", "--verbose=2")
	}
	return runner.Run(ctx, profile, arguments, "")
}
