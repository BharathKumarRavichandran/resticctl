package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"

	"resticctl/internal/app"
	"resticctl/internal/cronexpr"
	"resticctl/internal/profile"
	"resticctl/internal/runstatus"
	"resticctl/internal/schedule"
)

func (cli *commandLine) scheduleCommand() *cobra.Command {
	command := &cobra.Command{Use: "schedule", Short: "Manage scheduled backups", Args: cobra.NoArgs}
	command.AddCommand(cli.scheduleInstallCommand(), cli.scheduleRemoveCommand(), cli.scheduleStatusCommand(), cli.scheduleRunCommand())
	return command
}

func (cli *commandLine) scheduleInstallCommand() *cobra.Command {
	var expression, backend string
	var catchUp, prune bool
	command := &cobra.Command{
		Use:   "install <profile> [backup|forget]",
		Short: "Install a cron or launchd backup schedule",
		Example: `  resticctl schedule install personal
  resticctl schedule install personal --cron "0 2 * * *" --catch-up
  resticctl schedule install personal forget`,
		Args:              cobra.RangeArgs(1, 2),
		ValidArgsFunction: cli.completeProfiles,
		RunE: execute(func(command *cobra.Command, arguments []string) error {
			action, err := scheduledAction(arguments)
			if err != nil {
				return err
			}
			configDir, err := cli.resolveConfigDir()
			if err != nil {
				return err
			}
			backupProfile, err := profile.Load(configDir, arguments[0])
			if err != nil {
				return err
			}
			if action == schedule.ActionBackup {
				if err := app.ValidateDatabaseTools(backupProfile); err != nil {
					return err
				}
			}
			if action == schedule.ActionBackup && backupProfile.Schedule != nil {
				if !command.Flags().Changed("cron") {
					expression = backupProfile.Schedule.Cron
				}
				if !command.Flags().Changed("backend") {
					backend = backupProfile.Schedule.Backend
				}
				if !command.Flags().Changed("catch-up") {
					catchUp = backupProfile.Schedule.CatchUp
				}
			}
			if action == schedule.ActionForget && backupProfile.Forget != nil {
				if !command.Flags().Changed("cron") {
					expression = backupProfile.Forget.Cron
				}
				if !command.Flags().Changed("backend") {
					backend = backupProfile.Forget.Backend
				}
				if !command.Flags().Changed("catch-up") {
					catchUp = backupProfile.Forget.CatchUp
				}
				if !command.Flags().Changed("prune") {
					prune = backupProfile.Forget.Prune
				}
			}
			if expression == "" {
				return errors.New("schedule cron expression is required in the profile or with --cron")
			}
			if backend == "" {
				backend = schedule.BackendAuto
			}
			executable, err := cli.executable()
			if err != nil {
				return fmt.Errorf("cannot find resticctl executable: %w", err)
			}
			state, err := cli.newScheduleManager().InstallAction(command.Context(), configDir, arguments[0], action, expression, backend, executable, catchUp, prune)
			if err != nil {
				return err
			}
			return writeOutput(cli.stdout, "Installed %s %s schedule for %s: %s (catch-up: %t)\n", state.Backend, state.Action, state.Profile, state.Expression, state.CatchUp)
		}),
	}
	command.Flags().StringVar(&expression, "cron", "", "five-field cron expression")
	command.Flags().StringVar(&backend, "backend", "", "scheduler backend: auto, cron, or launchd")
	command.Flags().BoolVar(&catchUp, "catch-up", false, "run once after a missed schedule")
	command.Flags().BoolVar(&prune, "prune", false, "prune unreferenced data after scheduled forget")
	return command
}

func (cli *commandLine) scheduleRunCommand() *cobra.Command {
	var action string
	command := &cobra.Command{
		Use:               "run <profile>",
		Short:             "Run an overdue scheduled backup",
		Hidden:            true,
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
			state, err := schedule.LoadAction(configDir, arguments[0], action)
			if err != nil {
				return err
			}
			var lastSuccess *time.Time
			status, statusErr := runstatus.LoadAction(configDir, arguments[0], action)
			if statusErr == nil {
				lastSuccess = status.LastSuccessAt
				if lastSuccess == nil && status.State == "succeeded" {
					lastSuccess = status.FinishedAt
				}
			} else if !errors.Is(statusErr, runstatus.ErrNotRecorded) {
				return statusErr
			}
			if lastSuccess == nil {
				lastSuccess = &state.Installed
			}
			due, err := cronexpr.Due(state.Expression, lastSuccess, cli.now())
			if err != nil {
				return err
			}
			if !due {
				return writeOutput(cli.stdout, "Scheduled backup for %s is not due\n", arguments[0])
			}
			if action == schedule.ActionForget {
				return cli.runForget(command.Context(), configDir, backupProfile, false, state.Prune)
			}
			return cli.runBackup(command.Context(), configDir, backupProfile, false)
		}),
	}
	command.Flags().StringVar(&action, "action", schedule.ActionBackup, "scheduled action")
	return command
}

func (cli *commandLine) scheduleRemoveCommand() *cobra.Command {
	return &cobra.Command{
		Use:               "remove <profile> [backup|forget]",
		Short:             "Remove an installed backup schedule",
		Args:              cobra.RangeArgs(1, 2),
		ValidArgsFunction: cli.completeProfiles,
		RunE: execute(func(command *cobra.Command, arguments []string) error {
			action, err := scheduledAction(arguments)
			if err != nil {
				return err
			}
			configDir, err := cli.resolveConfigDir()
			if err != nil {
				return err
			}
			if err := cli.newScheduleManager().RemoveAction(command.Context(), configDir, arguments[0], action); err != nil {
				return err
			}
			return writeOutput(cli.stdout, "Removed %s schedule for %s\n", action, arguments[0])
		}),
	}
}

func (cli *commandLine) scheduleStatusCommand() *cobra.Command {
	var jsonOutput bool
	command := &cobra.Command{
		Use:               "status <profile> [backup|forget]",
		Short:             "Show schedule and latest backup status",
		Args:              cobra.RangeArgs(1, 2),
		ValidArgsFunction: cli.completeProfiles,
		RunE: execute(func(_ *cobra.Command, arguments []string) error {
			action, err := scheduledAction(arguments)
			if err != nil {
				return err
			}
			configDir, err := cli.resolveConfigDir()
			if err != nil {
				return err
			}
			state, err := schedule.LoadAction(configDir, arguments[0], action)
			if err != nil {
				return err
			}
			status, statusErr := runstatus.LoadAction(configDir, arguments[0], action)
			if statusErr != nil && !errors.Is(statusErr, runstatus.ErrNotRecorded) {
				return statusErr
			}
			result := scheduleStatusOutput{Schedule: state}
			if statusErr == nil {
				result.LastRun = &status
			}
			return cli.writeScheduleStatus(result, jsonOutput)
		}),
	}
	command.Flags().BoolVar(&jsonOutput, "json", false, "write machine-readable JSON")
	return command
}

func (cli *commandLine) statusCommand() *cobra.Command {
	var jsonOutput bool
	var action string
	command := &cobra.Command{
		Use:               "status <profile>",
		Short:             "Show the latest backup status",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: cli.completeProfiles,
		RunE: execute(func(_ *cobra.Command, arguments []string) error {
			configDir, err := cli.resolveConfigDir()
			if err != nil {
				return err
			}
			if err := profile.ValidateName(arguments[0]); err != nil {
				return err
			}
			status, err := runstatus.LoadAction(configDir, arguments[0], action)
			if err != nil {
				return err
			}
			if jsonOutput {
				return writeJSON(cli.stdout, status)
			}
			return writeRunStatus(cli, status)
		}),
	}
	command.Flags().BoolVar(&jsonOutput, "json", false, "write machine-readable JSON")
	command.Flags().StringVar(&action, "action", schedule.ActionBackup, "status action: backup or forget")
	return command
}

type scheduleStatusOutput struct {
	Schedule schedule.State    `json:"schedule"`
	LastRun  *runstatus.Status `json:"last_run,omitempty"`
}

func (cli *commandLine) writeScheduleStatus(result scheduleStatusOutput, jsonOutput bool) error {
	if jsonOutput {
		return writeJSON(cli.stdout, result)
	}
	if err := writeOutput(cli.stdout, "Profile: %s\nAction: %s\nBackend: %s\nSchedule: %s\nCatch up: %t\nInstalled: %s\n", result.Schedule.Profile, result.Schedule.Action, result.Schedule.Backend, result.Schedule.Expression, result.Schedule.CatchUp, result.Schedule.Installed.Format(time.RFC3339)); err != nil {
		return err
	}
	if result.LastRun == nil {
		return writeOutput(cli.stdout, "Last run: never\n")
	}
	return writeRunStatus(cli, *result.LastRun)
}

func scheduledAction(arguments []string) (string, error) {
	action := schedule.ActionBackup
	if len(arguments) == 2 {
		action = arguments[1]
	}
	if action != schedule.ActionBackup && action != schedule.ActionForget {
		return "", fmt.Errorf("unsupported scheduled action %q", action)
	}
	return action, nil
}

func writeRunStatus(cli *commandLine, status runstatus.Status) error {
	if err := writeOutput(cli.stdout, "State: %s\nStarted: %s\n", status.State, status.StartedAt.Format(time.RFC3339)); err != nil {
		return err
	}
	if status.FinishedAt != nil {
		if err := writeOutput(cli.stdout, "Finished: %s\nDuration: %s\n", status.FinishedAt.Format(time.RFC3339), time.Duration(status.DurationMS)*time.Millisecond); err != nil {
			return err
		}
	}
	if status.LastSuccessAt != nil {
		return writeOutput(cli.stdout, "Last success: %s\n", status.LastSuccessAt.Format(time.RFC3339))
	}
	return nil
}

func writeJSON(writer io.Writer, value any) error {
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return fmt.Errorf("cannot write command output: %w", err)
	}
	return nil
}
