package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"resticctl/internal/app"
	"resticctl/internal/group"
	"resticctl/internal/profile"
	"resticctl/internal/runstatus"
	"resticctl/internal/schedule"
)

func (cli *commandLine) groupCommand() *cobra.Command {
	command := &cobra.Command{Use: "group", Short: "Manage and run profile groups", Args: cobra.NoArgs}
	command.AddCommand(cli.groupCreateCommand(), cli.groupListCommand(), cli.groupShowCommand(), cli.groupValidateCommand(), cli.groupStatusCommand())
	for _, action := range []string{schedule.ActionBackup, schedule.ActionCheck, schedule.ActionForget, schedule.ActionPrune, schedule.ActionCopy} {
		command.AddCommand(cli.groupActionCommand(action))
	}
	return command
}

func (cli *commandLine) groupCreateCommand() *cobra.Command {
	var groupName string
	var profileNames []string
	var continueOnError bool
	command := &cobra.Command{
		Use:   "create <group> <profile>...",
		Short: "Create a group from existing profiles",
		Args: func(command *cobra.Command, arguments []string) error {
			usingFlags := groupName != "" || len(profileNames) != 0
			if !usingFlags {
				return cobra.MinimumNArgs(2)(command, arguments)
			}
			if len(arguments) != 0 {
				return errors.New("positional group and profiles must not be combined with --group or --profile")
			}
			if groupName == "" {
				return errors.New("--group is required when using --profile")
			}
			if len(profileNames) == 0 {
				return errors.New("at least one --profile is required when using --group")
			}
			return nil
		},
		ValidArgsFunction: cobra.NoFileCompletions,
		RunE: execute(func(_ *cobra.Command, arguments []string) error {
			if len(arguments) != 0 {
				groupName = arguments[0]
				profileNames = arguments[1:]
			}
			configDir, err := cli.resolveConfigDir()
			if err != nil {
				return err
			}
			for _, name := range profileNames {
				if _, err := profile.Load(profile.Dir(configDir), name); err != nil {
					return fmt.Errorf("cannot add profile %s to group %s: %w", name, groupName, err)
				}
			}
			path, err := group.Create(configDir, group.Group{
				Name: groupName, Profiles: profileNames, ContinueOnError: continueOnError,
			})
			if err != nil {
				return err
			}
			return writeOutput(cli.stdout, "Created group %s: %s\n", groupName, path)
		}),
	}
	command.Flags().StringVar(&groupName, "group", "", "group name")
	command.Flags().StringArrayVar(&profileNames, "profile", nil, "profile to include; may be specified multiple times")
	command.Flags().BoolVar(&continueOnError, "continue-on-error", false, "continue running profiles after a failure")
	return command
}

func (cli *commandLine) groupListCommand() *cobra.Command {
	return &cobra.Command{
		Use: "list", Short: "List configured groups", Args: cobra.NoArgs,
		ValidArgsFunction: cobra.NoFileCompletions,
		RunE: execute(func(_ *cobra.Command, _ []string) error {
			configDir, err := cli.resolveConfigDir()
			if err != nil {
				return err
			}
			groups, err := group.List(configDir)
			if err != nil {
				return err
			}
			if len(groups) == 0 {
				return fmt.Errorf("no groups found in %s", configDir)
			}
			return writeOutput(cli.stdout, "%s\n", strings.Join(groups, "\n"))
		}),
	}
}

func (cli *commandLine) groupShowCommand() *cobra.Command {
	return &cobra.Command{
		Use: "show <group>", Short: "Show a group", Args: cobra.ExactArgs(1),
		ValidArgsFunction: cli.completeGroups,
		RunE: execute(func(_ *cobra.Command, arguments []string) error {
			configDir, err := cli.resolveConfigDir()
			if err != nil {
				return err
			}
			configured, err := group.Load(configDir, arguments[0])
			if err != nil {
				return err
			}
			return writeJSON(cli.stdout, configured)
		}),
	}
}

func (cli *commandLine) groupValidateCommand() *cobra.Command {
	return &cobra.Command{
		Use: "validate <group>", Short: "Validate a group and all its profiles", Args: cobra.ExactArgs(1),
		ValidArgsFunction: cli.completeGroups,
		RunE: execute(func(_ *cobra.Command, arguments []string) error {
			configDir, configured, _, err := cli.loadGroup(arguments[0])
			if err != nil {
				return err
			}
			return writeOutput(cli.stdout, "Group %s is valid (%d profiles in %s)\n", configured.Name, len(configured.Profiles), configDir)
		}),
	}
}

func (cli *commandLine) groupStatusCommand() *cobra.Command {
	var action string
	var jsonOutput bool
	command := &cobra.Command{
		Use: "status <group>", Short: "Show the latest aggregate group run status", Args: cobra.ExactArgs(1),
		ValidArgsFunction: cli.completeGroups,
		RunE: execute(func(_ *cobra.Command, arguments []string) error {
			configDir, err := cli.resolveConfigDir()
			if err != nil {
				return err
			}
			status, err := runstatus.LoadGroupAction(configDir, arguments[0], action)
			if err != nil {
				return err
			}
			if jsonOutput {
				return writeJSON(cli.stdout, status)
			}
			return writeRunStatus(cli, status)
		}),
	}
	command.Flags().StringVar(&action, "action", schedule.ActionBackup, "status action: backup, check, forget, prune, or copy")
	command.Flags().BoolVar(&jsonOutput, "json", false, "write machine-readable JSON")
	return command
}

func (cli *commandLine) groupActionCommand(action string) *cobra.Command {
	var dryRun bool
	var prune bool
	command := &cobra.Command{
		Use: action + " <group>", Short: "Run " + action + " for every profile in a group sequentially", Args: cobra.ExactArgs(1),
		ValidArgsFunction: cli.completeGroups,
		RunE: execute(func(command *cobra.Command, arguments []string) (returnErr error) {
			configDir, configured, profiles, err := cli.loadGroup(arguments[0])
			if err != nil {
				return err
			}
			configuredDryRun := false
			for _, member := range profiles {
				configuredDryRun = configuredDryRun || groupActionDryRun(member, action)
			}
			var recorder *runstatus.Recorder
			finished := false
			if !dryRun && !configuredDryRun {
				recorder, err = runstatus.BeginGroupAction(configDir, configured.Name, action, cli.now())
				if err != nil {
					return err
				}
				defer func() {
					if !finished {
						returnErr = errors.Join(returnErr, recorder.Finish(returnErr, cli.now()))
					}
				}()
			}
			var failures []error
			for index, backupProfile := range profiles {
				message := "Running " + action + " for"
				if action == schedule.ActionBackup {
					message = "Backing up"
				}
				if err := writeOutput(cli.stdout, "==> [%d/%d] %s profile %s\n", index+1, len(profiles), message, backupProfile.Name); err != nil {
					return err
				}
				err := cli.runGroupMember(command.Context(), configDir, backupProfile, action, dryRun, prune)
				if err == nil {
					if err := writeOutput(cli.stdout, "<== Profile %s succeeded\n", backupProfile.Name); err != nil {
						return err
					}
					continue
				}
				failure := fmt.Errorf("profile %s: %w", backupProfile.Name, err)
				failures = append(failures, failure)
				if outputErr := writeOutput(cli.stdout, "<== Profile %s failed: %v\n", backupProfile.Name, err); outputErr != nil {
					return errors.Join(failure, outputErr)
				}
				if command.Context().Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || !configured.ContinueOnError {
					if outputErr := writeOutput(cli.stdout, "<== Group %s failed\n", configured.Name); outputErr != nil {
						return errors.Join(failure, outputErr)
					}
					if recorder != nil {
						finished = true
						return errors.Join(failure, recorder.Finish(failure, cli.now()))
					}
					return failure
				}
			}
			if len(failures) != 0 {
				if err := writeOutput(cli.stdout, "<== Group %s failed (%d profiles failed)\n", configured.Name, len(failures)); err != nil {
					return errors.Join(errors.Join(failures...), err)
				}
				failure := errors.Join(failures...)
				if recorder != nil {
					finished = true
					return errors.Join(failure, recorder.Finish(failure, cli.now()))
				}
				return failure
			}
			if recorder != nil {
				finished = true
				if err := recorder.Finish(nil, cli.now()); err != nil {
					return err
				}
			}
			return writeOutput(cli.stdout, "<== Group %s succeeded\n", configured.Name)
		}),
	}
	if action != schedule.ActionCheck {
		command.Flags().BoolVar(&dryRun, "dry-run", false, "preview each action without changing the repository")
	}
	if action == schedule.ActionForget {
		command.Flags().BoolVar(&prune, "prune", false, "remove unreferenced repository data")
	}
	return command
}

func groupActionDryRun(backupProfile profile.Profile, action string) bool {
	var arguments []string
	if action == schedule.ActionBackup {
		arguments = backupProfile.BackupArgs
	} else if action == schedule.ActionForget {
		arguments = backupProfile.ForgetArgs
	}
	arguments = append(arguments, backupProfile.Commands[action].Args...)
	for _, argument := range arguments {
		if profile.IsDryRunOption(argument) {
			return true
		}
	}
	return false
}

func (cli *commandLine) runGroupMember(ctx context.Context, configDir string, backupProfile profile.Profile, action string, dryRun, prune bool) error {
	switch action {
	case schedule.ActionBackup:
		return cli.runBackup(ctx, configDir, backupProfile, dryRun)
	case schedule.ActionCheck:
		return app.RunCheck(ctx, cli.newRunner, configDir, backupProfile, cli.now)
	case schedule.ActionForget:
		return cli.runForget(ctx, configDir, backupProfile, dryRun, prune)
	case schedule.ActionPrune, schedule.ActionCopy:
		var arguments []string
		if dryRun {
			arguments = []string{"--dry-run"}
		}
		return app.RunRecordedRestic(ctx, cli.newRunner, configDir, backupProfile, action, arguments, cli.now, cli.stdout)
	default:
		return fmt.Errorf("unsupported group action %q", action)
	}
}

func (cli *commandLine) loadGroup(name string) (string, group.Group, []profile.Profile, error) {
	configDir, err := cli.resolveConfigDir()
	if err != nil {
		return "", group.Group{}, nil, err
	}
	configured, err := group.Load(configDir, name)
	if err != nil {
		return "", group.Group{}, nil, err
	}
	profiles := make([]profile.Profile, 0, len(configured.Profiles))
	for _, member := range configured.Profiles {
		backupProfile, err := profile.Load(profile.Dir(configDir), member)
		if err != nil {
			return "", group.Group{}, nil, fmt.Errorf("invalid profile %s in group %s: %w", member, configured.Name, err)
		}
		if err := app.ValidateDatabaseTools(backupProfile); err != nil {
			return "", group.Group{}, nil, fmt.Errorf("invalid profile %s in group %s: %w", member, configured.Name, err)
		}
		profiles = append(profiles, backupProfile)
	}
	return configDir, configured, profiles, nil
}

func (cli *commandLine) createCommand() *cobra.Command {
	return &cobra.Command{
		Use:               "create <profile>",
		Short:             "Create a public profile and private override file",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: cobra.NoFileCompletions,
		RunE: execute(func(_ *cobra.Command, arguments []string) error {
			configDir, err := cli.resolveConfigDir()
			if err != nil {
				return err
			}
			profilePath, privatePath, err := app.CreateProfile(configDir, arguments[0])
			if err != nil {
				return err
			}
			return writeOutput(
				cli.stdout,
				"Created profile:\n  %s\n  %s\nEdit both files before running: resticctl init %s\n",
				profilePath,
				privatePath,
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
			profiles, err := profile.List(profile.Dir(configDir))
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

func (cli *commandLine) showCommand() *cobra.Command {
	var explain bool
	var profileName string
	command := &cobra.Command{
		Use:   "show [profile]",
		Short: "Show a resolved profile with secrets redacted",
		Args: func(command *cobra.Command, arguments []string) error {
			if err := cobra.MaximumNArgs(1)(command, arguments); err != nil {
				return err
			}
			if profileName != "" && len(arguments) != 0 {
				return errors.New("profile must be provided either as an argument or with --profile, not both")
			}
			if profileName == "" && len(arguments) == 0 {
				return errors.New("profile is required")
			}
			return nil
		},
		ValidArgsFunction: cli.completeProfiles,
		RunE: execute(func(_ *cobra.Command, arguments []string) error {
			name := profileName
			if len(arguments) != 0 {
				name = arguments[0]
			}
			configDir, err := cli.resolveConfigDir()
			if err != nil {
				return err
			}
			backupProfile, err := profile.Load(profile.Dir(configDir), name)
			if err != nil {
				return err
			}
			resolved := profile.RedactedResolvedProfile(backupProfile)
			if !explain {
				return writeJSON(cli.stdout, resolved)
			}
			explanation, err := profile.ExplainInheritance(profile.Dir(configDir), name)
			if err != nil {
				return err
			}
			return writeJSON(cli.stdout, struct {
				Profile     profile.ResolvedProfile    `json:"profile"`
				Explanation []profile.FieldExplanation `json:"explanation"`
			}{Profile: resolved, Explanation: explanation})
		}),
	}
	command.Flags().StringVar(&profileName, "profile", "", "profile to show")
	command.Flags().BoolVar(&explain, "explain", false, "show where each configured field came from")
	return command
}

func (cli *commandLine) initCommand() *cobra.Command {
	return cli.profileCommand("init", "Initialize a restic repository", func(ctx context.Context, runner app.ResticRunner, backupProfile profile.Profile) error {
		return app.RunRestic(ctx, runner, backupProfile, "init", nil)
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
			backupProfile, err := profile.Load(profile.Dir(configDir), arguments[0])
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
			backupProfile, err := profile.Load(profile.Dir(configDir), arguments[0])
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
	return app.RunBackup(ctx, cli.newRunner, configDir, backupProfile, dryRun, cli.stdout, cli.now)
}

func (cli *commandLine) snapshotsCommand() *cobra.Command {
	return cli.profileCommand("snapshots", "List snapshots for a profile", app.Snapshots)
}

func (cli *commandLine) statsCommand() *cobra.Command {
	var mode string
	command := cli.profileCommand("stats", "Show repository statistics for a profile", func(ctx context.Context, runner app.ResticRunner, backupProfile profile.Profile) error {
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
			return cli.executeForProfile(command.Context(), arguments[0], func(ctx context.Context, runner app.ResticRunner, backupProfile profile.Profile) error {
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
			return cli.executeForProfile(command.Context(), arguments[0], func(ctx context.Context, runner app.ResticRunner, backupProfile profile.Profile) error {
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
			return cli.executeForProfile(command.Context(), arguments[0], func(ctx context.Context, runner app.ResticRunner, backupProfile profile.Profile) error {
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
			return cli.executeForProfile(command.Context(), arguments[0], func(ctx context.Context, runner app.ResticRunner, backupProfile profile.Profile) error {
				return app.Dump(ctx, runner, backupProfile, arguments[1], arguments[2], archive, target)
			})
		}),
	}
	command.Flags().StringVarP(&archive, "archive", "a", "", "archive format (tar or zip)")
	command.Flags().StringVarP(&target, "target", "t", "", "write output to a file")
	return command
}

func (cli *commandLine) checkCommand() *cobra.Command {
	return &cobra.Command{
		Use: "check <profile>", Short: "Check a repository for errors", Args: cobra.ExactArgs(1), ValidArgsFunction: cli.completeProfiles,
		RunE: execute(func(command *cobra.Command, arguments []string) error {
			configDir, err := cli.resolveConfigDir()
			if err != nil {
				return err
			}
			backupProfile, err := profile.Load(profile.Dir(configDir), arguments[0])
			if err != nil {
				return err
			}
			return app.RunCheck(command.Context(), cli.newRunner, configDir, backupProfile, cli.now)
		}),
	}
}

func (cli *commandLine) runCommand() *cobra.Command {
	command := &cobra.Command{
		Use:                "run <profile> <restic-command> [args...]",
		Short:              "Run a supported restic command",
		Long:               "Run a supported restic command with arguments passed through unchanged. Repository and password flags are managed by resticctl. Use <restic-command> --help to show help from the installed Restic version.",
		DisableFlagParsing: true,
		Args:               cobra.MinimumNArgs(2),
		ValidArgsFunction:  cli.completeFirstProfile,
		RunE: execute(func(command *cobra.Command, arguments []string) error {
			arguments, err := cli.extractRunConfigDir(arguments)
			if err != nil {
				return err
			}
			if len(arguments) < 2 {
				return errors.New("run requires a profile and Restic command")
			}
			configDir, err := cli.resolveConfigDir()
			if err != nil {
				return err
			}
			backupProfile, err := profile.Load(profile.Dir(configDir), arguments[0])
			if err != nil {
				return err
			}
			action := arguments[1]
			if action == "backup" || action == "check" || action == "forget" || action == "prune" || action == "copy" {
				return app.RunRecordedRestic(command.Context(), cli.newRunner, configDir, backupProfile, action, arguments[2:], cli.now, cli.stdout)
			}
			runner, err := cli.newRunner()
			if err != nil {
				return err
			}
			return app.RunRestic(command.Context(), runner, backupProfile, action, arguments[2:])
		}),
	}
	return command
}

func (cli *commandLine) extractRunConfigDir(arguments []string) ([]string, error) {
	index := 0
	for index < len(arguments) {
		argument := arguments[index]
		switch {
		case argument == "--config-dir":
			if index+1 == len(arguments) {
				return nil, errors.New("--config-dir requires a value")
			}
			cli.configDir = arguments[index+1]
			index += 2
		case strings.HasPrefix(argument, "--config-dir="):
			cli.configDir = strings.TrimPrefix(argument, "--config-dir=")
			index++
		default:
			return arguments[index:], nil
		}
	}
	return nil, nil
}

func (cli *commandLine) keyCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "key",
		Short: "Manage repository keys",
		Args:  cobra.NoArgs,
	}
	command.AddCommand(
		cli.profileCommand("list", "List repository keys", func(ctx context.Context, runner app.ResticRunner, backupProfile profile.Profile) error {
			return app.RunRestic(ctx, runner, backupProfile, "key", []string{"list"})
		}),
		cli.profileCommand("add", "Add a repository key", func(ctx context.Context, runner app.ResticRunner, backupProfile profile.Profile) error {
			return app.RunRestic(ctx, runner, backupProfile, "key", []string{"add"})
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
			return cli.executeForProfile(command.Context(), arguments[0], func(ctx context.Context, runner app.ResticRunner, backupProfile profile.Profile) error {
				return app.RunRestic(ctx, runner, backupProfile, "key", []string{"remove", arguments[1]})
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
			backupProfile, err := profile.Load(profile.Dir(configDir), arguments[0])
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
	return app.RunForget(ctx, cli.newRunner, configDir, backupProfile, dryRun, prune, cli.now)
}

func (cli *commandLine) restoreCommand() *cobra.Command {
	var dryRun bool
	command := &cobra.Command{
		Use:               "restore <profile> <snapshot> <target>",
		Short:             "Restore a snapshot",
		Args:              cobra.ExactArgs(3),
		ValidArgsFunction: cli.completeRestoreArguments,
		RunE: execute(func(command *cobra.Command, arguments []string) error {
			return cli.executeForProfile(command.Context(), arguments[0], func(ctx context.Context, runner app.ResticRunner, backupProfile profile.Profile) error {
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
