package app

import (
	"context"
	"errors"
	"fmt"

	"resticctl/internal/profile"
	"resticctl/internal/restic"
)

type copyRunner interface {
	Copy(context.Context, restic.CopyConfig, []string) error
	InitCopyDestination(context.Context, restic.CopyConfig, bool) error
}

func copyConfig(backupProfile profile.Profile, target profile.CopyTarget) restic.CopyConfig {
	destination := restic.Config{
		Repository: target.Repository, Environment: target.Credentials.Environment,
		PasswordCommand: target.Credentials.Password.Command,
		PasswordFile:    target.Credentials.Password.File, PasswordValue: target.Credentials.Password.Value,
	}
	source := resticConfig(backupProfile)
	destination.Arguments, source.Arguments = source.Arguments, nil
	return restic.CopyConfig{Source: source, Destination: destination}
}

func Copy(ctx context.Context, runner Runner, backupProfile profile.Profile, targetName string, dryRun bool) (runErr error) {
	target, ok := backupProfile.Copies[targetName]
	if !ok {
		return fmt.Errorf("profile %s has no copy target %q", backupProfile.Name, targetName)
	}
	capable, ok := runner.(copyRunner)
	if !ok {
		return errors.New("runner does not support copy workflows")
	}
	defer func() {
		runErr = errors.Join(runErr, runHooks(context.WithoutCancel(ctx), runner, "copy-run-finally", target.RunFinally))
	}()
	if err := runHooks(ctx, runner, "copy-run-before", target.RunBefore); err != nil {
		runErr = err
	} else {
		config := copyConfig(backupProfile, target)
		if err := initializeCopyDestination(ctx, runner, capable, config, target, dryRun); err != nil {
			runErr = err
		} else if dryRun {
			runErr = nil
		} else {
			arguments := copyArguments(backupProfile, target)
			runErr = applyResticExitPolicy(ctx, backupProfile, capable.Copy(ctx, config, arguments))
			if runErr == nil {
				runErr = runHooks(ctx, runner, "copy-run-after", target.RunAfter)
			}
		}
	}
	if runErr != nil {
		runErr = errors.Join(runErr, runHooks(context.WithoutCancel(ctx), runner, "copy-run-after-fail", target.RunAfterFail))
	}
	return runErr
}

func copyArguments(backupProfile profile.Profile, target profile.CopyTarget) []string {
	arguments := append([]string(nil), target.Args...)
	for _, host := range target.Hosts {
		arguments = append(arguments, "--host", host)
	}
	arguments = append(arguments, "--tag", profileTag(backupProfile))
	for _, tag := range target.Tags {
		arguments = append(arguments, "--tag", tag)
	}
	for _, path := range target.Paths {
		arguments = append(arguments, "--path", path)
	}
	if len(target.SnapshotIDs) != 0 {
		arguments = append(arguments, "--")
		arguments = append(arguments, target.SnapshotIDs...)
	}
	return arguments
}

func initializeCopyDestination(ctx context.Context, runner Runner, capable copyRunner, config restic.CopyConfig, target profile.CopyTarget, dryRun bool) error {
	if !target.InitializeRepository {
		return nil
	}
	probe, ok := runner.(repositoryProbe)
	if !ok {
		return errors.New("runner does not support repository probing")
	}
	exists, err := probe.RepositoryExists(ctx, config.Destination)
	if err != nil {
		return fmt.Errorf("probe copy destination: %w", err)
	}
	if exists {
		return nil
	}
	if dryRun {
		return errors.New("copy destination is missing; dry run will not initialize it")
	}
	if err := capable.InitCopyDestination(ctx, config, target.CopyChunkerParams); err == nil {
		return nil
	} else {
		raceExists, probeErr := probe.RepositoryExists(ctx, config.Destination)
		if probeErr == nil && raceExists {
			return nil
		}
		return fmt.Errorf("initialize copy destination: %w", errors.Join(err, probeErr))
	}
}
