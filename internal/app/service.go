package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"resticctl/internal/databasebackup"
	"resticctl/internal/profile"
	"resticctl/internal/restic"
)

// ResticRunner executes a Restic argument vector for a repository.
type ResticRunner interface {
	Run(context.Context, restic.Config, []string, string) error
}

// HookRunner executes a lifecycle hook without invoking a shell.
type HookRunner interface {
	RunHook(context.Context, []string) error
}

// Runner provides every subprocess operation required by a backup workflow.
type Runner interface {
	ResticRunner
	HookRunner
	RunDatabase(context.Context, []string, map[string]string, string) error
}

func resticConfig(backupProfile profile.Profile) restic.Config {
	return restic.Config{
		Repository:      backupProfile.Repository,
		Arguments:       backupProfile.ResticArgs,
		Environment:     backupProfile.Credentials.Environment,
		PasswordCommand: backupProfile.Credentials.Password.Command,
		PasswordFile:    backupProfile.Credentials.Password.File,
	}
}

func Backup(ctx context.Context, runner Runner, backupProfile profile.Profile, dryRun bool, output io.Writer) (runErr error) {
	defer func() {
		runErr = errors.Join(runErr, runHooks(context.WithoutCancel(ctx), runner, "run-finally", backupProfile.RunFinally))
	}()
	if err := runHooks(ctx, runner, "run-before", backupProfile.RunBefore); err != nil {
		runErr = err
	} else if err := ValidateDatabaseTools(backupProfile); err != nil {
		runErr = err
	} else if err := validateBackupSources(backupProfile); err != nil {
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

func runHooks(ctx context.Context, runner HookRunner, phase string, hooks []profile.Hook) error {
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
		return runner.Run(ctx, resticConfig(backupProfile), arguments, "")
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
		return fmt.Errorf("cannot protect database staging directory: %w", err)
	}
	databaseDir := filepath.Join(staging, "databases")
	if err := os.Mkdir(databaseDir, 0o700); err != nil {
		return fmt.Errorf("cannot create database staging directory: %w", err)
	}
	providers := make([]databasebackup.Provider, 0, len(backupProfile.SQLiteDatabases)+len(backupProfile.PostgreSQLDatabases)+len(backupProfile.MongoDBDatabases))
	for _, database := range backupProfile.SQLiteDatabases {
		providers = append(providers, databasebackup.SQLite{Database: database})
	}
	for _, database := range backupProfile.PostgreSQLDatabases {
		providers = append(providers, databasebackup.PostgreSQL{Database: database})
	}
	for _, database := range backupProfile.MongoDBDatabases {
		providers = append(providers, databasebackup.MongoDB{Database: database})
	}
	if err := stageDatabaseProviders(ctx, runner, staging, backupProfile, providers, output); err != nil {
		return err
	}
	arguments = append(arguments, "--")
	arguments = append(arguments, backupProfile.BackupPaths...)
	arguments = append(arguments, "databases")
	return runner.Run(ctx, resticConfig(backupProfile), arguments, staging)
}

func stageDatabaseProviders(ctx context.Context, runner databasebackup.Runner, staging string, backupProfile profile.Profile, providers []databasebackup.Provider, output io.Writer) error {
	if len(providers) == 0 {
		return nil
	}
	concurrency := backupProfile.DatabaseConcurrency
	if concurrency <= 0 {
		concurrency = 1
	}
	concurrency = min(concurrency, len(providers))

	workerCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan databasebackup.Provider)
	var workers sync.WaitGroup
	var firstErr error
	var recordError sync.Once
	var outputMu sync.Mutex
	workers.Add(concurrency)
	for range concurrency {
		go func() {
			defer workers.Done()
			for provider := range jobs {
				if workerCtx.Err() != nil {
					return
				}
				if progress := provider.Progress(); progress != "" {
					outputMu.Lock()
					_, outputErr := fmt.Fprintf(output, "==> %s\n", progress)
					outputMu.Unlock()
					if outputErr != nil {
						recordError.Do(func() { firstErr = fmt.Errorf("cannot write backup progress: %w", outputErr); cancel() })
						return
					}
				}
				environment := backupProfile.Credentials.DatabaseEnvironmentFor(provider.Name())
				if err := provider.Stage(workerCtx, runner, staging, environment); err != nil {
					recordError.Do(func() {
						firstErr = err
						cancel()
					})
					return
				}
			}
		}()
	}

enqueue:
	for _, provider := range providers {
		select {
		case jobs <- provider:
		case <-workerCtx.Done():
			break enqueue
		}
	}
	close(jobs)
	workers.Wait()
	if firstErr != nil {
		return firstErr
	}
	return ctx.Err()
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
