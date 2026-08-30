package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"resticctl/internal/profile"
	"resticctl/internal/sqlitebackup"
)

type Runner interface {
	Run(context.Context, profile.Profile, []string, string) error
}

func Backup(ctx context.Context, runner Runner, backupProfile profile.Profile, dryRun bool, output io.Writer) (backupErr error) {
	if len(backupProfile.BackupPaths) == 0 && len(backupProfile.SQLiteDatabases) == 0 {
		return errors.New("profile has no backup_paths or sqlite_databases")
	}
	for _, path := range backupProfile.BackupPaths {
		if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("backup path not found: %s", path)
		} else if err != nil {
			return fmt.Errorf("cannot inspect backup path %s: %w", path, err)
		}
	}

	arguments := []string{"backup", "--group-by", "host,tags", "--tag", "profile:" + backupProfile.Name}
	for _, tag := range backupProfile.Tags {
		arguments = append(arguments, "--tag", tag)
	}
	arguments = append(arguments, backupProfile.BackupArgs...)
	if dryRun {
		arguments = append(arguments, "--dry-run")
	}
	if len(backupProfile.SQLiteDatabases) == 0 {
		arguments = append(arguments, "--")
		arguments = append(arguments, backupProfile.BackupPaths...)
		return runner.Run(ctx, backupProfile, arguments, "")
	}

	staging, err := os.MkdirTemp("", "resticctl-"+backupProfile.Name+"-")
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
	for _, database := range backupProfile.SQLiteDatabases {
		if _, err := fmt.Fprintf(output, "==> Snapshotting SQLite database: %s\n", database.Name); err != nil {
			return fmt.Errorf("cannot write backup progress: %w", err)
		}
		if err := sqlitebackup.Create(ctx, database.Path, filepath.Join(databaseDir, database.Name+".sqlite3")); err != nil {
			return err
		}
	}
	arguments = append(arguments, "--")
	arguments = append(arguments, backupProfile.BackupPaths...)
	arguments = append(arguments, "databases")
	return runner.Run(ctx, backupProfile, arguments, staging)
}

func Snapshots(ctx context.Context, runner Runner, backupProfile profile.Profile) error {
	return runner.Run(ctx, backupProfile, []string{"snapshots", "--tag", "profile:" + backupProfile.Name}, "")
}

func Check(ctx context.Context, runner Runner, backupProfile profile.Profile) error {
	return runner.Run(ctx, backupProfile, append([]string{"check"}, backupProfile.CheckArgs...), "")
}

func Forget(ctx context.Context, runner Runner, backupProfile profile.Profile, dryRun, prune bool) error {
	if len(backupProfile.ForgetArgs) == 0 {
		return errors.New("profile has no forget_args")
	}
	arguments := []string{"forget", "--tag", "profile:" + backupProfile.Name, "--group-by", "host,tags"}
	arguments = append(arguments, backupProfile.ForgetArgs...)
	if prune {
		arguments = append(arguments, "--prune")
	}
	if dryRun {
		arguments = append(arguments, "--dry-run")
	}
	return runner.Run(ctx, backupProfile, arguments, "")
}

func Restore(ctx context.Context, runner Runner, backupProfile profile.Profile, snapshot, target string, dryRun bool) error {
	arguments := []string{"restore", snapshot, "--tag", "profile:" + backupProfile.Name, "--target", target}
	if dryRun {
		arguments = append(arguments, "--dry-run", "--verbose=2")
	}
	return runner.Run(ctx, backupProfile, arguments, "")
}
