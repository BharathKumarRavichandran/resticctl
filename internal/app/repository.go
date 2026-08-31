package app

import (
	"context"
	"errors"

	"resticctl/internal/profile"
)

func Snapshots(ctx context.Context, runner ResticRunner, backupProfile profile.Profile) error {
	return runner.Run(ctx, backupProfile, []string{"snapshots", "--tag", profileTag(backupProfile)}, "")
}

func Stats(ctx context.Context, runner ResticRunner, backupProfile profile.Profile, mode string) error {
	arguments := []string{"stats", "--tag", profileTag(backupProfile)}
	if mode != "" {
		arguments = append(arguments, "--mode", mode)
	}
	return runner.Run(ctx, backupProfile, arguments, "")
}

func ListSnapshot(ctx context.Context, runner ResticRunner, backupProfile profile.Profile, snapshot string, paths []string, long, recursive, humanReadable bool, sort string, reverse bool) error {
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

func Find(ctx context.Context, runner ResticRunner, backupProfile profile.Profile, patterns []string, ignoreCase, long, humanReadable, reverse bool) error {
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

func Diff(ctx context.Context, runner ResticRunner, backupProfile profile.Profile, first, second string, metadata bool) error {
	arguments := []string{"diff", first, second}
	if metadata {
		arguments = append(arguments, "--metadata")
	}
	return runner.Run(ctx, backupProfile, arguments, "")
}

func Dump(ctx context.Context, runner ResticRunner, backupProfile profile.Profile, snapshot, path, archive, target string) error {
	arguments := []string{"dump", snapshot, path, "--tag", profileTag(backupProfile)}
	if archive != "" {
		arguments = append(arguments, "--archive", archive)
	}
	if target != "" {
		arguments = append(arguments, "--target", target)
	}
	return runner.Run(ctx, backupProfile, arguments, "")
}

func Check(ctx context.Context, runner ResticRunner, backupProfile profile.Profile) error {
	return runner.Run(ctx, backupProfile, append([]string{"check"}, backupProfile.CheckArgs...), "")
}

func Forget(ctx context.Context, runner ResticRunner, backupProfile profile.Profile, dryRun, prune bool) error {
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

func Restore(ctx context.Context, runner ResticRunner, backupProfile profile.Profile, snapshot, target string, dryRun bool) error {
	arguments := []string{"restore", snapshot, "--tag", profileTag(backupProfile), "--target", target}
	if dryRun {
		arguments = append(arguments, "--dry-run", "--verbose=2")
	}
	return runner.Run(ctx, backupProfile, arguments, "")
}

func profileTag(backupProfile profile.Profile) string {
	return "profile:" + backupProfile.Name
}
