package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

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
	destination.Arguments, _ = scopeCopyTags(source.Arguments, profileTag(backupProfile))
	source.Arguments = nil
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
	arguments, hasTags := scopeCopyTags(target.Args, profileTag(backupProfile))
	for _, host := range target.Hosts {
		arguments = append(arguments, "--host", host)
	}
	_, globalTags := scopeCopyTags(backupProfile.ResticArgs, profileTag(backupProfile))
	if len(target.Tags) == 0 && !hasTags && !globalTags {
		arguments = append(arguments, "--tag", profileTag(backupProfile))
	}
	for _, tag := range target.Tags {
		arguments = append(arguments, "--tag", profileTag(backupProfile)+","+tag)
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

func scopeCopyTags(arguments []string, tag string) ([]string, bool) {
	scoped := append([]string(nil), arguments...)
	found := false
	for i := 0; i < len(scoped); i++ {
		if copyOptionTakesValue(scoped[i]) {
			i++
			continue
		}
		if scoped[i] == "--tag" && i+1 < len(scoped) {
			i++
			scoped[i] = tag + "," + scoped[i]
			found = true
		} else if value, ok := strings.CutPrefix(scoped[i], "--tag="); ok {
			scoped[i] = "--tag=" + tag + "," + value
			found = true
		}
	}
	return scoped, found
}

func copyOptionTakesValue(argument string) bool {
	switch argument {
	case "--host", "--path", "-H", "--option", "-o", "--cache-dir", "--cacert", "--tls-client-cert", "--key-hint", "--compression", "--limit-download", "--limit-upload", "--pack-size", "--retry-lock", "--stuck-request-timeout", "--http-user-agent":
		return true
	}
	if strings.HasPrefix(argument, "-") && !strings.HasPrefix(argument, "--") {
		for i, flag := range argument[1:] {
			switch flag {
			case 'q', 'v', 'h':
			case 'H', 'o':
				return i+2 == len(argument)
			default:
				return false
			}
		}
	}
	return false
}
