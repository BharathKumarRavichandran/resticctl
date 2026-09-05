package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"resticctl/internal/app"
	"resticctl/internal/profile"
	"resticctl/internal/runstatus"
	"resticctl/internal/schedule"
)

func (cli *commandLine) scheduleCommand() *cobra.Command {
	command := &cobra.Command{Use: "schedule", Short: "Manage scheduled actions", Args: cobra.NoArgs}
	command.AddCommand(cli.scheduleInstallCommand(), cli.scheduleReconcileCommand(), cli.scheduleRemoveCommand(), cli.scheduleListCommand(), cli.scheduleStatusCommand(), cli.scheduleRunCommand())
	return command
}

func (cli *commandLine) scheduleInstallCommand() *cobra.Command {
	var expression, backend string
	var calendars []string
	var catchUp, prune, dryRun, noStart, noEnable, network, acPower bool
	var permission, cronFile, user, priority, logPath, lockMode, lockWait string
	command := &cobra.Command{
		Use:   "install <profile> [backup|check|forget|prune|copy]",
		Short: "Render or install a scheduled action",
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
				if len(calendars) == 0 {
					return errors.New("schedule calendar expression is required in the profile or with --cron or --calendar")
				}
			}
			if expression != "" {
				calendars = append([]string{expression}, calendars...)
			}
			if backend == "" {
				backend = schedule.BackendAuto
			}
			executable, err := cli.executable()
			if err != nil {
				return fmt.Errorf("cannot find resticctl executable: %w", err)
			}
			state, err := cli.newScheduleManager().InstallSpec(command.Context(), schedule.Spec{
				Name: arguments[0], Action: action, Expressions: calendars, Backend: backend, Executable: executable, ConfigDir: configDir,
				CatchUp: catchUp, Prune: prune, DryRun: dryRun, Permission: permission, CronFile: cronFile, User: user,
				Priority: priority, Log: logPath, LockMode: lockMode, LockWait: lockWait, Enabled: !noEnable, Start: !noStart,
				Network: network, ACPower: acPower,
			})
			if err != nil {
				return err
			}
			if dryRun {
				return writeOutput(cli.stdout, "%s", state.Rendered)
			}
			return writeOutput(cli.stdout, "Installed %s %s schedule for %s: %s (catch-up: %t)\n", state.Backend, state.Action, state.Profile, strings.Join(state.Expressions, ", "), state.CatchUp)
		}),
	}
	command.Flags().StringVar(&expression, "cron", "", "five-field cron expression")
	command.Flags().StringArrayVar(&calendars, "calendar", nil, "portable five-field calendar expression (repeatable)")
	command.Flags().StringVar(&backend, "backend", "", "scheduler backend: auto, cron, launchd, systemd, or windows")
	command.Flags().BoolVar(&catchUp, "catch-up", false, "run once after a missed schedule")
	command.Flags().BoolVar(&prune, "prune", false, "prune unreferenced data after scheduled forget")
	command.Flags().StringVar(&permission, "permission", schedule.PermissionUser, "permission mode: user, logged-on-user, or system")
	command.Flags().StringVar(&cronFile, "crontab-file", "", "write an explicit crontab file")
	command.Flags().StringVar(&user, "user", "", "account for a system schedule")
	command.Flags().StringVar(&priority, "priority", schedule.PriorityNormal, "process priority: normal or background")
	command.Flags().StringVar(&logPath, "log", "", "append scheduler output to this path")
	command.Flags().StringVar(&lockMode, "lock-mode", schedule.LockFail, "lock contention mode: fail or wait")
	command.Flags().StringVar(&lockWait, "lock-wait", "", "maximum lock wait duration")
	command.Flags().BoolVar(&noStart, "no-start", false, "install without starting the schedule")
	command.Flags().BoolVar(&noEnable, "no-enable", false, "install without enabling the schedule")
	command.Flags().BoolVar(&network, "require-network", false, "run only when network is available where supported")
	command.Flags().BoolVar(&acPower, "require-ac-power", false, "run only on AC power where supported")
	command.Flags().BoolVar(&dryRun, "dry-run", false, "render scheduler changes without installing")
	return command
}

func (cli *commandLine) scheduleReconcileCommand() *cobra.Command {
	var all, dryRun bool
	command := &cobra.Command{
		Use:   "reconcile [profile]",
		Short: "Reconcile profile-declared schedules",
		Args: func(command *cobra.Command, arguments []string) error {
			if err := cobra.MaximumNArgs(1)(command, arguments); err != nil {
				return err
			}
			if len(arguments) == 0 && !all {
				return errors.New("provide exactly one profile or --all")
			}
			if len(arguments) == 1 && all {
				return errors.New("profile and --all cannot be used together")
			}
			return nil
		},
		ValidArgsFunction: cli.completeProfiles,
		RunE: execute(func(command *cobra.Command, arguments []string) error {
			configDir, err := cli.resolveConfigDir()
			if err != nil {
				return err
			}
			names := arguments
			if all {
				names, err = profile.List(configDir)
				if err != nil {
					return err
				}
			}
			executable, err := cli.executable()
			if err != nil {
				return fmt.Errorf("cannot find resticctl executable: %w", err)
			}
			manager := cli.newScheduleManager()
			for _, name := range names {
				backupProfile, err := profile.Load(configDir, name)
				if err != nil {
					return err
				}
				if err := cli.reconcileProfileSchedules(command.Context(), manager, configDir, executable, backupProfile, dryRun); err != nil {
					return err
				}
			}
			return nil
		}),
	}
	command.Flags().BoolVar(&all, "all", false, "reconcile schedules for every profile")
	command.Flags().BoolVar(&dryRun, "dry-run", false, "show scheduler changes without applying them")
	return command
}

func (cli *commandLine) reconcileProfileSchedules(ctx context.Context, manager schedule.Manager, configDir, executable string, backupProfile profile.Profile, dryRun bool) error {
	if backupProfile.Schedule != nil {
		if err := app.ValidateDatabaseTools(backupProfile); err != nil {
			return err
		}
	}
	specs, err := declaredScheduleSpecs(configDir, executable, backupProfile, dryRun)
	if err != nil {
		return err
	}
	for _, spec := range specs {
		state, err := manager.InstallSpec(ctx, spec)
		if err != nil {
			return fmt.Errorf("cannot reconcile %s %s schedule: %w", backupProfile.Name, spec.Action, err)
		}
		if dryRun {
			if err := writeOutput(cli.stdout, "# %s %s\n%s", backupProfile.Name, spec.Action, state.Rendered); err != nil {
				return err
			}
		} else if err := writeOutput(cli.stdout, "Reconciled %s schedule for %s\n", spec.Action, backupProfile.Name); err != nil {
			return err
		}
	}
	var undeclared []string
	if backupProfile.Schedule == nil {
		undeclared = append(undeclared, schedule.ActionBackup)
	}
	if backupProfile.Forget == nil {
		undeclared = append(undeclared, schedule.ActionForget)
	}
	for _, action := range undeclared {
		if _, err := schedule.LoadAction(configDir, backupProfile.Name, action); errors.Is(err, schedule.ErrNotInstalled) {
			continue
		} else if err != nil {
			return err
		}
		if dryRun {
			if err := writeOutput(cli.stdout, "Would remove %s schedule for %s\n", action, backupProfile.Name); err != nil {
				return err
			}
			continue
		}
		if err := manager.RemoveAction(ctx, configDir, backupProfile.Name, action); err != nil {
			return err
		}
		if err := writeOutput(cli.stdout, "Removed undeclared %s schedule for %s\n", action, backupProfile.Name); err != nil {
			return err
		}
	}
	return nil
}

func declaredScheduleSpecs(configDir, executable string, backupProfile profile.Profile, dryRun bool) ([]schedule.Spec, error) {
	var specs []schedule.Spec
	if configured := backupProfile.Schedule; configured != nil {
		specs = append(specs, schedule.Spec{
			Name: backupProfile.Name, Action: schedule.ActionBackup, Backend: configured.Backend,
			Executable: executable, ConfigDir: configDir, Expressions: []string{configured.Cron},
			CatchUp: configured.CatchUp,
		})
	}
	if configured := backupProfile.Forget; configured != nil {
		specs = append(specs, schedule.Spec{
			Name: backupProfile.Name, Action: schedule.ActionForget, Backend: configured.Backend,
			Executable: executable, ConfigDir: configDir, Expressions: []string{configured.Cron},
			CatchUp: configured.CatchUp, Prune: configured.Prune,
		})
	}
	for i := range specs {
		specs[i].Permission = schedule.PermissionUser
		specs[i].Enabled = true
		specs[i].Start = true
		specs[i].DryRun = dryRun
		installed, err := schedule.LoadAction(configDir, backupProfile.Name, specs[i].Action)
		if errors.Is(err, schedule.ErrNotInstalled) {
			continue
		}
		if err != nil {
			return nil, err
		}
		preserveSchedulePolicy(&specs[i], installed)
	}
	return specs, nil
}

func preserveSchedulePolicy(spec *schedule.Spec, installed schedule.State) {
	spec.Permission = installed.Permission
	spec.CronFile = installed.CronFile
	spec.User = installed.User
	spec.Priority = installed.Priority
	spec.Log = installed.Log
	spec.LockMode = installed.LockMode
	spec.LockWait = installed.LockWait
	spec.Enabled = installed.Enabled
	spec.Start = installed.Start
	spec.Network = installed.Network
	spec.ACPower = installed.ACPower
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
			due, err := app.ScheduledRun(command.Context(), cli.newRunner, cli.newScheduleManager(), configDir, backupProfile, action, cli.now, cli.stdout)
			if err != nil {
				return err
			}
			if !due {
				return writeOutput(cli.stdout, "Scheduled %s for %s is not due\n", action, arguments[0])
			}
			return nil
		}),
	}
	command.Flags().StringVar(&action, "action", schedule.ActionBackup, "scheduled action")
	return command
}

func (cli *commandLine) scheduleRemoveCommand() *cobra.Command {
	var dryRun bool
	command := &cobra.Command{
		Use:               "remove <profile> [backup|check|forget|prune|copy]",
		Aliases:           []string{"uninstall"},
		Short:             "Remove an installed schedule",
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
			if dryRun {
				if _, err := schedule.LoadAction(configDir, arguments[0], action); err != nil {
					return err
				}
				return writeOutput(cli.stdout, "Would remove %s schedule for %s\n", action, arguments[0])
			}
			if err := cli.newScheduleManager().RemoveAction(command.Context(), configDir, arguments[0], action); err != nil {
				return err
			}
			return writeOutput(cli.stdout, "Removed %s schedule for %s\n", action, arguments[0])
		}),
	}
	command.Flags().BoolVar(&dryRun, "dry-run", false, "show the removal without applying it")
	return command
}

type scheduleListItem struct {
	Schedule schedule.State `json:"schedule"`
	Status   string         `json:"status"`
}

func (cli *commandLine) scheduleListCommand() *cobra.Command {
	var jsonOutput bool
	command := &cobra.Command{
		Use:               "list [profile]",
		Short:             "List installed schedules",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: cli.completeProfiles,
		RunE: execute(func(command *cobra.Command, arguments []string) error {
			configDir, err := cli.resolveConfigDir()
			if err != nil {
				return err
			}
			name := ""
			if len(arguments) == 1 {
				name = arguments[0]
			}
			states, err := schedule.List(configDir, name)
			if err != nil {
				return err
			}
			manager := cli.newScheduleManager()
			items := make([]scheduleListItem, 0, len(states))
			for _, state := range states {
				verification := "ok"
				if err := manager.Verify(command.Context(), state); errors.Is(err, schedule.ErrDrift) {
					verification = "drift"
				} else if err != nil {
					return err
				}
				items = append(items, scheduleListItem{Schedule: state, Status: verification})
			}
			if jsonOutput {
				return writeJSON(cli.stdout, items)
			}
			if len(items) == 0 {
				return writeOutput(cli.stdout, "No installed schedules.\n")
			}
			for _, item := range items {
				if err := writeOutput(cli.stdout, "%s\t%s\t%s\t%s\t%s\n", item.Schedule.Profile, item.Schedule.Action, item.Schedule.Backend, item.Schedule.Expression, item.Status); err != nil {
					return err
				}
			}
			return nil
		}),
	}
	command.Flags().BoolVar(&jsonOutput, "json", false, "write machine-readable JSON")
	return command
}

func (cli *commandLine) scheduleStatusCommand() *cobra.Command {
	var jsonOutput bool
	command := &cobra.Command{
		Use:               "status <profile> [backup|check|forget|prune|copy]",
		Short:             "Show a schedule and its latest run status",
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
			state, err := schedule.LoadAction(configDir, arguments[0], action)
			if err != nil {
				return err
			}
			if err := cli.newScheduleManager().Verify(command.Context(), state); err != nil {
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
	var history int
	command := &cobra.Command{
		Use:               "status <profile>",
		Short:             "Show the latest run status",
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
			if history > 0 {
				statuses, err := runstatus.LoadHistory(configDir, arguments[0], action)
				if err != nil {
					return err
				}
				if len(statuses) > history {
					statuses = statuses[:history]
				}
				if jsonOutput {
					return writeJSON(cli.stdout, statuses)
				}
				for index, status := range statuses {
					if index > 0 {
						if err := writeOutput(cli.stdout, "---\n"); err != nil {
							return err
						}
					}
					if err := writeRunStatus(cli, status); err != nil {
						return err
					}
				}
				return nil
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
	command.Flags().StringVar(&action, "action", schedule.ActionBackup, "status action: backup, check, forget, prune, or copy")
	command.Flags().IntVar(&history, "history", 0, "show the newest N completed runs")
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
	if action != schedule.ActionBackup && action != schedule.ActionForget && action != schedule.ActionCheck && action != schedule.ActionPrune && action != schedule.ActionCopy {
		return "", fmt.Errorf("unsupported scheduled action %q", action)
	}
	return action, nil
}

func writeRunStatus(cli *commandLine, status runstatus.Status) error {
	if err := writeOutput(cli.stdout, "Command: %s\nState: %s\nStarted: %s\n", status.Command, status.State, status.StartedAt.Format(time.RFC3339)); err != nil {
		return err
	}
	if status.FinishedAt != nil {
		if err := writeOutput(cli.stdout, "Finished: %s\nDuration: %s\n", status.FinishedAt.Format(time.RFC3339), time.Duration(status.DurationMS)*time.Millisecond); err != nil {
			return err
		}
	}
	if status.LastSuccessAt != nil {
		if err := writeOutput(cli.stdout, "Last success: %s\n", status.LastSuccessAt.Format(time.RFC3339)); err != nil {
			return err
		}
	}
	if status.ExitCode != nil {
		if err := writeOutput(cli.stdout, "Exit code: %d\n", *status.ExitCode); err != nil {
			return err
		}
	}
	if status.Warning {
		return writeOutput(cli.stdout, "Restic warning: true\n")
	}
	return nil
}

func writeJSON(writer io.Writer, value any) error {
	encoder := json.NewEncoder(writer)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return fmt.Errorf("cannot write command output: %w", err)
	}
	return nil
}
