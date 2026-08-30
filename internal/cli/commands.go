package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"resticctl/internal/app"
	"resticctl/internal/profile"
)

func (cli *commandLine) createCommand() *cobra.Command {
	return &cobra.Command{
		Use:               "create <profile>",
		Short:             "Create a profile and credentials file",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: cobra.NoFileCompletions,
		RunE: execute(func(_ *cobra.Command, arguments []string) error {
			configDir, err := cli.resolveConfigDir()
			if err != nil {
				return err
			}
			profilePath, credentialsPath, err := app.CreateProfile(configDir, arguments[0])
			if err != nil {
				return err
			}
			return writeOutput(
				cli.stdout,
				"Created profile:\n  %s\n  %s\nEdit both files before running: resticctl init %s\n",
				profilePath,
				credentialsPath,
				arguments[0],
			)
		}),
	}
}

func (cli *commandLine) listCommand() *cobra.Command {
	return &cobra.Command{
		Use:               "list",
		Short:             "List configured profiles",
		Args:              cobra.NoArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		RunE: execute(func(_ *cobra.Command, _ []string) error {
			configDir, err := cli.resolveConfigDir()
			if err != nil {
				return err
			}
			profiles, err := profile.List(configDir)
			if err != nil {
				return err
			}
			if len(profiles) == 0 {
				return fmt.Errorf("no profiles found in %s", configDir)
			}
			return writeOutput(cli.stdout, "%s\n", strings.Join(profiles, "\n"))
		}),
	}
}

func (cli *commandLine) initCommand() *cobra.Command {
	return cli.profileCommand("init", "Initialize a restic repository", func(ctx context.Context, runner app.Runner, backupProfile profile.Profile) error {
		return runner.Run(ctx, backupProfile, []string{"init"}, "")
	})
}

func (cli *commandLine) backupCommand() *cobra.Command {
	var dryRun bool
	command := cli.profileCommand("backup", "Back up a profile", func(ctx context.Context, runner app.Runner, backupProfile profile.Profile) error {
		return app.Backup(ctx, runner, backupProfile, dryRun, cli.stdout)
	})
	command.Flags().BoolVar(&dryRun, "dry-run", false, "preview the backup without writing a snapshot")
	return command
}

func (cli *commandLine) snapshotsCommand() *cobra.Command {
	return cli.profileCommand("snapshots", "List snapshots for a profile", app.Snapshots)
}

func (cli *commandLine) checkCommand() *cobra.Command {
	return cli.profileCommand("check", "Check a repository for errors", app.Check)
}

func (cli *commandLine) forgetCommand() *cobra.Command {
	var dryRun, prune bool
	command := cli.profileCommand("forget", "Apply a profile's retention rules", func(ctx context.Context, runner app.Runner, backupProfile profile.Profile) error {
		return app.Forget(ctx, runner, backupProfile, dryRun, prune)
	})
	command.Flags().BoolVar(&dryRun, "dry-run", false, "preview retention changes")
	command.Flags().BoolVar(&prune, "prune", false, "remove unreferenced repository data")
	return command
}

func (cli *commandLine) restoreCommand() *cobra.Command {
	var dryRun bool
	command := &cobra.Command{
		Use:               "restore <profile> <snapshot> <target>",
		Short:             "Restore a snapshot",
		Args:              cobra.ExactArgs(3),
		ValidArgsFunction: cli.completeRestoreArguments,
		RunE: execute(func(command *cobra.Command, arguments []string) error {
			return cli.executeForProfile(command.Context(), arguments[0], func(ctx context.Context, runner app.Runner, backupProfile profile.Profile) error {
				return app.Restore(ctx, runner, backupProfile, arguments[1], arguments[2], dryRun)
			})
		}),
	}
	command.Flags().BoolVar(&dryRun, "dry-run", false, "preview the restore")
	return command
}

func (cli *commandLine) profileCommand(name, description string, action profileAction) *cobra.Command {
	return &cobra.Command{
		Use:               name + " <profile>",
		Short:             description,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: cli.completeProfiles,
		RunE: execute(func(command *cobra.Command, arguments []string) error {
			return cli.executeForProfile(command.Context(), arguments[0], action)
		}),
	}
}
