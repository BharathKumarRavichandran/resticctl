package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"

	"resticctl/internal/profile"
	"resticctl/internal/restic"
	"resticctl/internal/runstatus"
)

type snapshotIdentityRunner interface {
	SnapshotIdentities(context.Context, restic.Config, []string) ([]restic.SnapshotIdentity, error)
}

// VerifyMigration compares selected snapshots and checks the destination
// repository without changing the active profile.
func VerifyMigration(ctx context.Context, newRunner RunnerFactory, configDir string, backupProfile profile.Profile, targetName string, testRestore bool, output io.Writer) error {
	targetProfile, err := profile.ForCopyTarget(backupProfile, targetName)
	if err != nil {
		return err
	}
	return runstatus.WithProfileLock(ctx, configDir, backupProfile.Name, func() error {
		return verifyMigration(ctx, newRunner, backupProfile, targetProfile, targetName, testRestore, output)
	})
}

// CutoverMigration verifies the destination and promotes it without releasing
// the profile-wide operation lock between those steps.
func CutoverMigration(ctx context.Context, newRunner RunnerFactory, configDir, profileDir, profileName, targetName string, testRestore bool, output io.Writer) (string, error) {
	var rollback string
	err := runstatus.WithProfileLock(ctx, configDir, profileName, func() error {
		backupProfile, err := profile.Load(profileDir, profileName)
		if err != nil {
			return err
		}
		if backupProfile.PrivateFile != "" || backupProfile.CredentialsFile == "" {
			return errors.New("migration cutover requires the source profile to use credentials_file")
		}
		targetProfile, err := profile.ForCopyTarget(backupProfile, targetName)
		if err != nil {
			return err
		}
		if err := verifyMigration(ctx, newRunner, backupProfile, targetProfile, targetName, testRestore, output); err != nil {
			return err
		}
		rollback, err = profile.Cutover(profileDir, profileName, targetName)
		return err
	})
	return rollback, err
}

func verifyMigration(ctx context.Context, newRunner RunnerFactory, backupProfile, targetProfile profile.Profile, targetName string, testRestore bool, output io.Writer) error {
	return runWithPolicy(ctx, newRunner, targetProfile, false, func(runCtx context.Context, runner Runner) error {
		capable, ok := runner.(snapshotIdentityRunner)
		if !ok {
			return errors.New("runner does not support migration snapshot verification")
		}
		target := backupProfile.Copies[targetName]
		sourceSnapshots, err := capable.SnapshotIdentities(runCtx, resticConfig(backupProfile), copyArguments(backupProfile, target))
		if err != nil {
			return fmt.Errorf("list selected source snapshots: %w", err)
		}
		if len(sourceSnapshots) == 0 {
			return errors.New("migration source selection contains no snapshots")
		}
		destinationSnapshots, err := capable.SnapshotIdentities(runCtx, resticConfig(targetProfile), []string{"--tag", profileTag(backupProfile)})
		if err != nil {
			return fmt.Errorf("list destination snapshots: %w", err)
		}
		missing := missingSnapshotIDs(sourceSnapshots, destinationSnapshots)
		if len(missing) != 0 {
			shown := missing[:min(len(missing), 10)]
			return fmt.Errorf("destination is missing %d selected source snapshot(s); first missing IDs: %v", len(missing), shown)
		}
		if err := Check(runCtx, runner, targetProfile); err != nil {
			return fmt.Errorf("check destination repository: %w", err)
		}
		if testRestore {
			snapshotID, err := representativeDestinationSnapshot(sourceSnapshots, destinationSnapshots)
			if err != nil {
				return err
			}
			if err := testRestoreSnapshot(runCtx, runner, targetProfile, snapshotID); err != nil {
				return err
			}
		}
		if output != nil {
			restoreSuffix := ""
			if testRestore {
				restoreSuffix = " and completed a verified test restore"
			}
			_, err = fmt.Fprintf(output, "Verified %d selected source snapshot(s) in migration target %s%s\n", len(sourceSnapshots), targetName, restoreSuffix)
		}
		return err
	})
}

func representativeDestinationSnapshot(source, destination []restic.SnapshotIdentity) (string, error) {
	wanted := originalSnapshotID(source[0])
	for _, snapshot := range destination {
		if originalSnapshotID(snapshot) == wanted {
			return snapshot.ID, nil
		}
	}
	return "", errors.New("cannot select a destination snapshot for test restore")
}

func testRestoreSnapshot(ctx context.Context, runner ResticRunner, targetProfile profile.Profile, snapshotID string) (runErr error) {
	directory, err := os.MkdirTemp("", "resticctl-migration-restore-")
	if err != nil {
		return fmt.Errorf("create test restore directory: %w", err)
	}
	defer func() {
		if err := os.RemoveAll(directory); err != nil {
			runErr = errors.Join(runErr, fmt.Errorf("remove test restore directory %s: %w", directory, err))
		}
	}()
	arguments := []string{"restore", snapshotID, "--target", directory, "--verify"}
	if err := invokeRestic(ctx, runner, targetProfile, arguments, ""); err != nil {
		return fmt.Errorf("test restore destination snapshot: %w", err)
	}
	return nil
}

func missingSnapshotIDs(source, destination []restic.SnapshotIdentity) []string {
	present := make(map[string]struct{}, len(destination))
	for _, snapshot := range destination {
		present[originalSnapshotID(snapshot)] = struct{}{}
	}
	var missing []string
	for _, snapshot := range source {
		identity := originalSnapshotID(snapshot)
		if _, ok := present[identity]; !ok {
			missing = append(missing, identity)
		}
	}
	slices.Sort(missing)
	return missing
}

func originalSnapshotID(snapshot restic.SnapshotIdentity) string {
	if snapshot.Original != "" {
		return snapshot.Original
	}
	return snapshot.ID
}
