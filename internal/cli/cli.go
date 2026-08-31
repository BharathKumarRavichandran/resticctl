package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"resticctl/internal/app"
	"resticctl/internal/profile"
	"resticctl/internal/restic"
	"resticctl/internal/schedule"
)

const Version = "0.1.0"

type commandLine struct {
	configDir          string
	stdout             io.Writer
	stderr             io.Writer
	newRunner          func() (app.Runner, error)
	newScheduleManager func() schedule.Manager
	executable         func() (string, error)
	now                func() time.Time
}

type profileAction func(context.Context, app.Runner, profile.Profile) error

func Run(ctx context.Context, arguments []string, stdout, stderr io.Writer) (int, error) {
	return newCommandLine(os.Stdin, stdout, stderr).run(ctx, arguments)
}

func newCommandLine(stdin io.Reader, stdout, stderr io.Writer) *commandLine {
	return &commandLine{
		stdout: stdout,
		stderr: stderr,
		newRunner: func() (app.Runner, error) {
			return restic.New(stdin, stdout, stderr)
		},
		newScheduleManager: func() schedule.Manager { return schedule.NewManager() },
		executable:         os.Executable,
		now:                time.Now,
	}
}

func (cli *commandLine) run(ctx context.Context, arguments []string) (int, error) {
	root := cli.rootCommand()
	root.SetArgs(arguments)
	if err := root.ExecuteContext(ctx); err != nil {
		var executionErr *executionError
		if errors.As(err, &executionErr) {
			return 1, executionErr.cause
		}
		if usageErr := cli.writeUsage(root, arguments); usageErr != nil {
			return 2, errors.Join(err, usageErr)
		}
		return 2, err
	}
	return 0, nil
}

func (cli *commandLine) rootCommand() *cobra.Command {
	root := &cobra.Command{
		Use:           "resticctl",
		Short:         "Manage profile-based restic backups",
		Version:       Version,
		SilenceErrors: true,
		SilenceUsage:  true,
		Args:          cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if err := command.Help(); err != nil {
				return err
			}
			return errors.New("command is required")
		},
	}
	root.SetOut(cli.stdout)
	root.SetErr(cli.stderr)
	root.SetVersionTemplate("resticctl {{.Version}}\n")
	root.PersistentFlags().StringVar(
		&cli.configDir,
		"config-dir",
		"",
		"profile directory (default: platform config directory)",
	)
	root.AddCommand(
		cli.createCommand(),
		cli.listCommand(),
		cli.initCommand(),
		cli.backupCommand(),
		cli.validateCommand(),
		cli.snapshotsCommand(),
		cli.statsCommand(),
		cli.lsCommand(),
		cli.findCommand(),
		cli.diffCommand(),
		cli.dumpCommand(),
		cli.keyCommand(),
		cli.checkCommand(),
		cli.forgetCommand(),
		cli.restoreCommand(),
		cli.statusCommand(),
		cli.scheduleCommand(),
	)
	return root
}

func (cli *commandLine) executeForProfile(ctx context.Context, name string, action profileAction) error {
	configDir, err := cli.resolveConfigDir()
	if err != nil {
		return err
	}
	backupProfile, err := profile.Load(configDir, name)
	if err != nil {
		return err
	}
	runner, err := cli.newRunner()
	if err != nil {
		return err
	}
	return action(ctx, runner, backupProfile)
}

func (cli *commandLine) completeProfiles(
	_ *cobra.Command,
	arguments []string,
	toComplete string,
) ([]string, cobra.ShellCompDirective) {
	if len(arguments) != 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	configDir, err := cli.resolveConfigDir()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	names, err := profile.List(configDir)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	matching := make([]string, 0, len(names))
	for _, name := range names {
		if strings.HasPrefix(name, toComplete) {
			matching = append(matching, name)
		}
	}
	return matching, cobra.ShellCompDirectiveNoFileComp
}

func (cli *commandLine) completeRestoreArguments(
	command *cobra.Command,
	arguments []string,
	toComplete string,
) ([]string, cobra.ShellCompDirective) {
	switch len(arguments) {
	case 0:
		return cli.completeProfiles(command, arguments, toComplete)
	case 2:
		return nil, cobra.ShellCompDirectiveFilterDirs
	default:
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
}

func (cli *commandLine) completeFirstProfile(
	command *cobra.Command,
	arguments []string,
	toComplete string,
) ([]string, cobra.ShellCompDirective) {
	if len(arguments) == 0 {
		return cli.completeProfiles(command, arguments, toComplete)
	}
	return nil, cobra.ShellCompDirectiveNoFileComp
}

func (cli *commandLine) resolveConfigDir() (string, error) {
	if cli.configDir != "" {
		return cli.configDir, nil
	}
	return profile.DefaultDir()
}

type executionError struct{ cause error }

func (err *executionError) Error() string { return err.cause.Error() }
func (err *executionError) Unwrap() error { return err.cause }

func execute(action func(*cobra.Command, []string) error) func(*cobra.Command, []string) error {
	return func(command *cobra.Command, arguments []string) error {
		if err := action(command, arguments); err != nil {
			return &executionError{cause: err}
		}
		return nil
	}
}

func (cli *commandLine) writeUsage(root *cobra.Command, arguments []string) error {
	command, _, err := root.Find(arguments)
	if err != nil || command == nil {
		command = root
	}
	return writeOutput(cli.stderr, "%s", command.UsageString())
}

func writeOutput(writer io.Writer, format string, arguments ...any) error {
	if _, err := fmt.Fprintf(writer, format, arguments...); err != nil {
		return fmt.Errorf("cannot write command output: %w", err)
	}
	return nil
}
