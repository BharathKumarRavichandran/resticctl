package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"resticctl/internal/app"
	"resticctl/internal/profile"
	"resticctl/internal/runstatus"
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
	command := &cobra.Command{
		Use:               "backup <profile>",
		Short:             "Back up a profile",
		Long:              "Back up a profile, including its configured check and prune orchestration steps.",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: cli.completeProfiles,
		RunE: execute(func(command *cobra.Command, arguments []string) error {
			configDir, err := cli.resolveConfigDir()
			if err != nil {
				return err
			}
			backupProfile, err := profile.Load(configDir, arguments[0])
			if err != nil {
				return err
			}
			return cli.runBackup(command.Context(), configDir, backupProfile, dryRun)
		}),
	}
	command.Flags().BoolVar(&dryRun, "dry-run", false, "preview the backup without writing a snapshot")
	return command
}

func (cli *commandLine) validateCommand() *cobra.Command {
	return &cobra.Command{
		Use: "validate <profile>", Short: "Validate a profile and its database clients",
		Args: cobra.ExactArgs(1), ValidArgsFunction: cli.completeProfiles,
		RunE: execute(func(_ *cobra.Command, arguments []string) error {
			configDir, err := cli.resolveConfigDir()
			if err != nil {
				return err
			}
			backupProfile, err := profile.Load(configDir, arguments[0])
			if err != nil {
				return err
			}
			if err := app.ValidateDatabaseTools(backupProfile); err != nil {
				return err
			}
			return writeOutput(cli.stdout, "Profile %s is valid\n", arguments[0])
		}),
	}
}

func (cli *commandLine) runBackup(ctx context.Context, configDir string, backupProfile profile.Profile, dryRun bool) error {
	if err := app.ValidateDatabaseTools(backupProfile); err != nil {
		return err
	}
	runner, err := cli.newRunner()
	if err != nil {
		return err
	}
	if dryRun {
		return app.Backup(ctx, runner, backupProfile, true, cli.stdout)
	}
	recorder, err := runstatus.Begin(configDir, backupProfile.Name, cli.now())
	if err != nil {
		return err
	}
	runErr := app.Backup(ctx, runner, backupProfile, false, cli.stdout)
	statusErr := recorder.Finish(runErr, cli.now())
	return errors.Join(runErr, statusErr)
}

func (cli *commandLine) snapshotsCommand() *cobra.Command {
	return cli.profileCommand("snapshots", "List snapshots for a profile", app.Snapshots)
}

func (cli *commandLine) statsCommand() *cobra.Command {
	var mode string
	command := cli.profileCommand("stats", "Show repository statistics for a profile", func(ctx context.Context, runner app.Runner, backupProfile profile.Profile) error {
		return app.Stats(ctx, runner, backupProfile, mode)
	})
	command.Flags().StringVar(&mode, "mode", "", "counting mode (restore-size, files-by-contents, blobs-per-file, or raw-data)")
	return command
}

func (cli *commandLine) lsCommand() *cobra.Command {
	var long, recursive, humanReadable, reverse bool
	var sort string
	command := &cobra.Command{
		Use:               "ls <profile> <snapshot> [path...]",
		Short:             "List files in a snapshot",
		Args:              cobra.MinimumNArgs(2),
		ValidArgsFunction: cli.completeFirstProfile,
		RunE: execute(func(command *cobra.Command, arguments []string) error {
			return cli.executeForProfile(command.Context(), arguments[0], func(ctx context.Context, runner app.Runner, backupProfile profile.Profile) error {
				return app.ListSnapshot(ctx, runner, backupProfile, arguments[1], arguments[2:], long, recursive, humanReadable, sort, reverse)
			})
		}),
	}
	command.Flags().BoolVarP(&long, "long", "l", false, "show size and mode")
	command.Flags().BoolVar(&recursive, "recursive", false, "include files in subdirectories")
	command.Flags().BoolVar(&humanReadable, "human-readable", false, "print human-readable sizes")
	command.Flags().StringVarP(&sort, "sort", "s", "", "sort by name, size, time, or extension")
	command.Flags().BoolVar(&reverse, "reverse", false, "reverse the sort order")
	return command
}

func (cli *commandLine) findCommand() *cobra.Command {
	var ignoreCase, long, humanReadable, reverse bool
	command := &cobra.Command{
		Use:               "find <profile> <pattern>...",
		Short:             "Find files in a profile's snapshots",
		Args:              cobra.MinimumNArgs(2),
		ValidArgsFunction: cli.completeFirstProfile,
		RunE: execute(func(command *cobra.Command, arguments []string) error {
			return cli.executeForProfile(command.Context(), arguments[0], func(ctx context.Context, runner app.Runner, backupProfile profile.Profile) error {
				return app.Find(ctx, runner, backupProfile, arguments[1:], ignoreCase, long, humanReadable, reverse)
			})
		}),
	}
	command.Flags().BoolVarP(&ignoreCase, "ignore-case", "i", false, "ignore case in patterns")
	command.Flags().BoolVarP(&long, "long", "l", false, "show size and mode")
	command.Flags().BoolVar(&humanReadable, "human-readable", false, "print human-readable sizes")
	command.Flags().BoolVarP(&reverse, "reverse", "R", false, "show oldest snapshots first")
	return command
}

func (cli *commandLine) diffCommand() *cobra.Command {
	var metadata bool
	command := &cobra.Command{
		Use:               "diff <profile> <snapshot-a> <snapshot-b>",
		Short:             "Compare two snapshots",
		Args:              cobra.ExactArgs(3),
		ValidArgsFunction: cli.completeFirstProfile,
		RunE: execute(func(command *cobra.Command, arguments []string) error {
			return cli.executeForProfile(command.Context(), arguments[0], func(ctx context.Context, runner app.Runner, backupProfile profile.Profile) error {
				return app.Diff(ctx, runner, backupProfile, arguments[1], arguments[2], metadata)
			})
		}),
	}
	command.Flags().BoolVar(&metadata, "metadata", false, "show metadata changes")
	return command
}

func (cli *commandLine) dumpCommand() *cobra.Command {
	var archive, target string
	command := &cobra.Command{
		Use:               "dump <profile> <snapshot> <path>",
		Short:             "Extract a file or directory from a snapshot",
		Args:              cobra.ExactArgs(3),
		ValidArgsFunction: cli.completeFirstProfile,
		RunE: execute(func(command *cobra.Command, arguments []string) error {
			return cli.executeForProfile(command.Context(), arguments[0], func(ctx context.Context, runner app.Runner, backupProfile profile.Profile) error {
				return app.Dump(ctx, runner, backupProfile, arguments[1], arguments[2], archive, target)
			})
		}),
	}
	command.Flags().StringVarP(&archive, "archive", "a", "", "archive format (tar or zip)")
	command.Flags().StringVarP(&target, "target", "t", "", "write output to a file")
	return command
}

func (cli *commandLine) checkCommand() *cobra.Command {
	return cli.profileCommand("check", "Check a repository for errors", app.Check)
}

func (cli *commandLine) keyCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "key",
		Short: "Manage repository keys",
		Args:  cobra.NoArgs,
	}
	command.AddCommand(
		cli.profileCommand("list", "List repository keys", func(ctx context.Context, runner app.Runner, backupProfile profile.Profile) error {
			return runner.Run(ctx, backupProfile, []string{"key", "list"}, "")
		}),
		cli.profileCommand("add", "Add a repository key", func(ctx context.Context, runner app.Runner, backupProfile profile.Profile) error {
			return runner.Run(ctx, backupProfile, []string{"key", "add"}, "")
		}),
		cli.keyRemoveCommand(),
	)
	return command
}

func (cli *commandLine) keyRemoveCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <profile> <key-id>",
		Short: "Remove a repository key",
		Args: cobra.MatchAll(cobra.ExactArgs(2), func(_ *cobra.Command, arguments []string) error {
			if !isKeyID(arguments[1]) {
				return fmt.Errorf("invalid key ID: %s", arguments[1])
			}
			return nil
		}),
		ValidArgsFunction: cli.completeFirstProfile,
		RunE: execute(func(command *cobra.Command, arguments []string) error {
			return cli.executeForProfile(command.Context(), arguments[0], func(ctx context.Context, runner app.Runner, backupProfile profile.Profile) error {
				return runner.Run(ctx, backupProfile, []string{"key", "remove", arguments[1]}, "")
			})
		}),
	}
}

func isKeyID(value string) bool {
	if value == "" || len(value) > 64 {
		return false
	}
	for index := range len(value) {
		if !isHexDigit(value[index]) {
			return false
		}
	}
	return true
}

func isHexDigit(character byte) bool {
	return character >= '0' && character <= '9' ||
		character >= 'a' && character <= 'f' ||
		character >= 'A' && character <= 'F'
}

func (cli *commandLine) forgetCommand() *cobra.Command {
	var dryRun, prune bool
	command := &cobra.Command{
		Use:               "forget <profile>",
		Short:             "Apply a profile's retention rules",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: cli.completeProfiles,
		RunE: execute(func(command *cobra.Command, arguments []string) error {
			configDir, err := cli.resolveConfigDir()
			if err != nil {
				return err
			}
			backupProfile, err := profile.Load(configDir, arguments[0])
			if err != nil {
				return err
			}
			return cli.runForget(command.Context(), configDir, backupProfile, dryRun, prune)
		}),
	}
	command.Flags().BoolVar(&dryRun, "dry-run", false, "preview retention changes")
	command.Flags().BoolVar(&prune, "prune", false, "remove unreferenced repository data")
	return command
}

func (cli *commandLine) runForget(ctx context.Context, configDir string, backupProfile profile.Profile, dryRun, prune bool) error {
	runner, err := cli.newRunner()
	if err != nil {
		return err
	}
	if dryRun {
		return app.Forget(ctx, runner, backupProfile, true, prune)
	}
	recorder, err := runstatus.BeginAction(configDir, backupProfile.Name, "forget", cli.now())
	if err != nil {
		return err
	}
	runErr := app.Forget(ctx, runner, backupProfile, false, prune)
	statusErr := recorder.Finish(runErr, cli.now())
	return errors.Join(runErr, statusErr)
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
