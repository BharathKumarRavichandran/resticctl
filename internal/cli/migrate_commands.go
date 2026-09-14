package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"resticctl/internal/app"
	"resticctl/internal/profile"
)

func (cli *commandLine) migrateCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "migrate",
		Short: "Migrate a profile to a configured copy target",
		Args:  cobra.NoArgs,
	}
	command.AddCommand(
		cli.migratePlanCommand(),
		cli.migrateSyncCommand(),
		cli.migrateVerifyCommand(),
		cli.migrateCutoverCommand(),
	)
	return command
}

func (cli *commandLine) migratePlanCommand() *cobra.Command {
	return &cobra.Command{
		Use: "plan <profile> <target>", Short: "Validate and describe a migration", Args: cobra.ExactArgs(2),
		ValidArgsFunction: cli.completeFirstProfile,
		RunE: execute(func(command *cobra.Command, arguments []string) error {
			_, value, err := cli.loadMigration(arguments[0], arguments[1])
			if err != nil {
				return err
			}
			redacted := profile.RedactedResolvedProfile(value)
			cutover := fmt.Sprintf("Cutover keeps the source as copy target rollback-%s; it never deletes source data.", arguments[1])
			if value.PrivateFile != "" || value.CredentialsFile == "" {
				cutover = "Cutover must be completed manually because the source does not use credentials_file."
			}
			_, err = fmt.Fprintf(cli.stdout, "Migration plan for profile %s to target %s\nSource: %s\nDestination: %s\nSteps: sync, verify, cutover\n%s\n", value.Name, arguments[1], redacted.Repository, redacted.Copies[arguments[1]].Repository, cutover)
			return err
		}),
	}
}

func (cli *commandLine) migrateSyncCommand() *cobra.Command {
	return &cobra.Command{
		Use: "sync <profile> <target>", Short: "Copy selected snapshots to the migration target", Args: cobra.ExactArgs(2),
		ValidArgsFunction: cli.completeFirstProfile,
		RunE: execute(func(command *cobra.Command, arguments []string) error {
			configDir, value, err := cli.loadMigration(arguments[0], arguments[1])
			if err != nil {
				return err
			}
			return app.RunCopy(command.Context(), cli.newRunner, configDir, value, arguments[1], false, cli.stdout, cli.now)
		}),
	}
}

func (cli *commandLine) migrateVerifyCommand() *cobra.Command {
	var testRestore bool
	command := &cobra.Command{
		Use: "verify <profile> <target>", Short: "Compare snapshots and check the migration target", Args: cobra.ExactArgs(2),
		ValidArgsFunction: cli.completeFirstProfile,
		RunE: execute(func(command *cobra.Command, arguments []string) error {
			configDir, value, err := cli.loadMigration(arguments[0], arguments[1])
			if err != nil {
				return err
			}
			return app.VerifyMigration(command.Context(), cli.newRunner, configDir, value, arguments[1], testRestore, cli.stdout)
		}),
	}
	command.Flags().BoolVar(&testRestore, "test-restore", false, "restore and verify one copied snapshot in a temporary directory")
	return command
}

func (cli *commandLine) migrateCutoverCommand() *cobra.Command {
	var confirm, testRestore bool
	command := &cobra.Command{
		Use: "cutover <profile> <target>", Short: "Promote a verified migration target", Args: cobra.ExactArgs(2),
		ValidArgsFunction: cli.completeFirstProfile,
		RunE: execute(func(command *cobra.Command, arguments []string) error {
			if !confirm {
				return errors.New("cutover requires --yes; the destination will be verified again before the profile is changed")
			}
			configDir, _, err := cli.loadMigration(arguments[0], arguments[1])
			if err != nil {
				return err
			}
			rollback, err := app.CutoverMigration(command.Context(), cli.newRunner, configDir, profile.Dir(configDir), arguments[0], arguments[1], testRestore, cli.stdout)
			if err != nil {
				return err
			}
			return writeOutput(cli.stdout, "Profile %s now uses target %s; former source is copy target %s\n", arguments[0], arguments[1], rollback)
		}),
	}
	command.Flags().BoolVar(&confirm, "yes", false, "verify the destination and update the profile")
	command.Flags().BoolVar(&testRestore, "test-restore", false, "restore and verify one copied snapshot before cutover")
	return command
}

func (cli *commandLine) loadMigration(profileName, targetName string) (string, profile.Profile, error) {
	configDir, err := cli.resolveConfigDir()
	if err != nil {
		return "", profile.Profile{}, err
	}
	value, err := profile.Load(profile.Dir(configDir), profileName)
	if err != nil {
		return "", profile.Profile{}, err
	}
	if _, ok := value.Copies[targetName]; !ok {
		return "", profile.Profile{}, fmt.Errorf("profile %s has no copy target %q", profileName, targetName)
	}
	return configDir, value, nil
}
