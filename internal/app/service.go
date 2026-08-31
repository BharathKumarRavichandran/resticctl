package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"resticctl/internal/databasebackup"
	"resticctl/internal/profile"
	"resticctl/internal/sqlitebackup"
)

type Runner interface {
	Run(context.Context, profile.Profile, []string, string) error
	RunHook(context.Context, []string) error
	RunDatabase(context.Context, []string, map[string]string, string) error
}

func Backup(ctx context.Context, runner Runner, backupProfile profile.Profile, dryRun bool, output io.Writer) (runErr error) {
	if err := ValidateDatabaseTools(backupProfile); err != nil {
		return err
	}
	if err := validateBackupSources(backupProfile); err != nil {
		return err
	}
	defer func() {
		runErr = errors.Join(runErr, runHooks(context.WithoutCancel(ctx), runner, "run-finally", backupProfile.RunFinally))
	}()
	if err := runHooks(ctx, runner, "run-before", backupProfile.RunBefore); err != nil {
		runErr = err
	} else {
		runErr = runBackupWorkflow(ctx, runner, backupProfile, dryRun, output)
		if runErr == nil {
			runErr = runHooks(ctx, runner, "run-after", backupProfile.RunAfter)
		}
	}
	if runErr != nil {
		runErr = errors.Join(runErr, runHooks(context.WithoutCancel(ctx), runner, "run-after-fail", backupProfile.RunAfterFail))
	}
	return runErr
}

func ValidateDatabaseTools(backupProfile profile.Profile) error {
	if err := databasebackup.Preflight(backupProfile); err != nil {
		return fmt.Errorf("database client preflight: %w", err)
	}
	return nil
}

func runBackupWorkflow(ctx context.Context, runner Runner, backupProfile profile.Profile, dryRun bool, output io.Writer) error {
	if backupProfile.CheckBefore {
		if err := Check(ctx, runner, backupProfile); err != nil {
			return fmt.Errorf("check before backup: %w", err)
		}
	}
	if backupProfile.PruneBefore {
		if err := Forget(ctx, runner, backupProfile, dryRun, true); err != nil {
			return fmt.Errorf("prune before backup: %w", err)
		}
	}
	if err := backup(ctx, runner, backupProfile, dryRun, output); err != nil {
		return err
	}
	if backupProfile.CheckAfter {
		if err := Check(ctx, runner, backupProfile); err != nil {
			return fmt.Errorf("check after backup: %w", err)
		}
	}
	if backupProfile.PruneAfter {
		if err := Forget(ctx, runner, backupProfile, dryRun, true); err != nil {
			return fmt.Errorf("prune after backup: %w", err)
		}
	}
	return nil
}

func runHooks(ctx context.Context, runner Runner, phase string, hooks []profile.Hook) error {
	for index, hook := range hooks {
		timeout := profile.DefaultHookTimeout
		if hook.Timeout != "" {
			var err error
			timeout, err = time.ParseDuration(hook.Timeout)
			if err != nil || timeout <= 0 {
				return fmt.Errorf("%s hook %d has invalid timeout %q", phase, index+1, hook.Timeout)
			}
		}
		if len(hook.Command) == 0 {
			return fmt.Errorf("%s hook %d has an empty command", phase, index+1)
		}
		hookCtx, cancel := context.WithTimeout(ctx, timeout)
		err := runner.RunHook(hookCtx, hook.Command)
		cancel()
		if err != nil {
			return fmt.Errorf("%s hook %d: %w", phase, index+1, err)
		}
	}
	return nil
}

func backup(ctx context.Context, runner Runner, backupProfile profile.Profile, dryRun bool, output io.Writer) (backupErr error) {
	arguments := []string{"backup", "--group-by", "host,tags", "--tag", profileTag(backupProfile)}
	for _, tag := range backupProfile.Tags {
		arguments = append(arguments, "--tag", tag)
	}
	arguments = append(arguments, backupProfile.BackupArgs...)
	if dryRun {
		arguments = append(arguments, "--dry-run")
	}
	if len(backupProfile.SQLiteDatabases) == 0 && len(backupProfile.PostgreSQLDatabases) == 0 && len(backupProfile.MongoDBDatabases) == 0 {
		arguments = append(arguments, "--")
		arguments = append(arguments, backupProfile.BackupPaths...)
		return runner.Run(ctx, backupProfile, arguments, "")
	}

	staging, err := os.MkdirTemp("", "resticctl-"+backupProfile.Name+"-")
	if err != nil {
		return fmt.Errorf("cannot create database staging directory: %w", err)
	}
	defer func() {
		if err := os.RemoveAll(staging); err != nil {
			backupErr = errors.Join(backupErr, fmt.Errorf("cannot remove database staging directory %s: %w", staging, err))
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
	providers := make([]databasebackup.Provider, 0, len(backupProfile.PostgreSQLDatabases)+len(backupProfile.MongoDBDatabases))
	for _, database := range backupProfile.PostgreSQLDatabases {
		providers = append(providers, databasebackup.PostgreSQL{Database: database})
	}
	for _, database := range backupProfile.MongoDBDatabases {
		providers = append(providers, databasebackup.MongoDB{Database: database})
	}
	for _, provider := range providers {
		if err := provider.Stage(ctx, runner, staging, backupProfile.Credentials.DatabaseEnvironment); err != nil {
			return err
		}
	}
	arguments = append(arguments, "--")
	arguments = append(arguments, backupProfile.BackupPaths...)
	arguments = append(arguments, "databases")
	return runner.Run(ctx, backupProfile, arguments, staging)
}

func validateBackupSources(backupProfile profile.Profile) error {
	if len(backupProfile.BackupPaths) == 0 && len(backupProfile.SQLiteDatabases) == 0 && len(backupProfile.PostgreSQLDatabases) == 0 && len(backupProfile.MongoDBDatabases) == 0 {
		return errors.New("profile has no backup paths or databases")
	}
	for _, path := range backupProfile.BackupPaths {
		if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("backup path not found: %s", path)
		} else if err != nil {
			return fmt.Errorf("cannot inspect backup path %s: %w", path, err)
		}
	}
	return nil
}

func Snapshots(ctx context.Context, runner Runner, backupProfile profile.Profile) error {
	return runner.Run(ctx, backupProfile, []string{"snapshots", "--tag", profileTag(backupProfile)}, "")
}

func Stats(ctx context.Context, runner Runner, backupProfile profile.Profile, mode string) error {
	arguments := []string{"stats", "--tag", profileTag(backupProfile)}
	if mode != "" {
		arguments = append(arguments, "--mode", mode)
	}
	return runner.Run(ctx, backupProfile, arguments, "")
}

func ListSnapshot(ctx context.Context, runner Runner, backupProfile profile.Profile, snapshot string, paths []string, long, recursive, humanReadable bool, sort string, reverse bool) error {
	arguments := []string{"ls", snapshot, "--tag", profileTag(backupProfile)}
	if long {
		arguments = append(arguments, "--long")
	}
	if recursive {
		arguments = append(arguments, "--recursive")
	}
	if humanReadable {
		arguments = append(arguments, "--human-readable")
	}
	if sort != "" {
		arguments = append(arguments, "--sort", sort)
	}
	if reverse {
		arguments = append(arguments, "--reverse")
	}
	arguments = append(arguments, paths...)
	return runner.Run(ctx, backupProfile, arguments, "")
}

func Find(ctx context.Context, runner Runner, backupProfile profile.Profile, patterns []string, ignoreCase, long, humanReadable, reverse bool) error {
	arguments := []string{"find", "--tag", profileTag(backupProfile)}
	if ignoreCase {
		arguments = append(arguments, "--ignore-case")
	}
	if long {
		arguments = append(arguments, "--long")
	}
	if humanReadable {
		arguments = append(arguments, "--human-readable")
	}
	if reverse {
		arguments = append(arguments, "--reverse")
	}
	arguments = append(arguments, patterns...)
	return runner.Run(ctx, backupProfile, arguments, "")
}

func Diff(ctx context.Context, runner Runner, backupProfile profile.Profile, first, second string, metadata bool) error {
	arguments := []string{"diff", first, second}
	if metadata {
		arguments = append(arguments, "--metadata")
	}
	return runner.Run(ctx, backupProfile, arguments, "")
}

func Dump(ctx context.Context, runner Runner, backupProfile profile.Profile, snapshot, path, archive, target string) error {
	arguments := []string{"dump", snapshot, path, "--tag", profileTag(backupProfile)}
	if archive != "" {
		arguments = append(arguments, "--archive", archive)
	}
	if target != "" {
		arguments = append(arguments, "--target", target)
	}
	return runner.Run(ctx, backupProfile, arguments, "")
}

func Check(ctx context.Context, runner Runner, backupProfile profile.Profile) error {
	return runner.Run(ctx, backupProfile, append([]string{"check"}, backupProfile.CheckArgs...), "")
}

func Forget(ctx context.Context, runner Runner, backupProfile profile.Profile, dryRun, prune bool) error {
	if len(backupProfile.ForgetArgs) == 0 {
		return errors.New("profile has no forget_args")
	}
	arguments := []string{"forget", "--tag", profileTag(backupProfile), "--group-by", "host,tags"}
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
	arguments := []string{"restore", snapshot, "--tag", profileTag(backupProfile), "--target", target}
	if dryRun {
		arguments = append(arguments, "--dry-run", "--verbose=2")
	}
	return runner.Run(ctx, backupProfile, arguments, "")
}

func profileTag(backupProfile profile.Profile) string {
	return "profile:" + backupProfile.Name
}
