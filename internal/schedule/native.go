package schedule

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"resticctl/internal/securefile"
)

func nativeID(state State) string {
	return "resticctl-" + state.Action + "-" + state.Profile
}

func (manager Manager) render(configDir string, state State, executable string) ([]byte, error) {
	backend, err := manager.backend(state.Backend)
	if err != nil {
		return nil, err
	}
	return backend.render(configDir, state, executable)
}

func (manager Manager) systemdDir(state State) string {
	if state.Permission == PermissionSystem {
		return manager.systemdSystemDir
	}
	return manager.systemdUserDir
}

func systemdEscape(value string) string {
	return strconv.Quote(strings.ReplaceAll(value, "%", "%%"))
}

func cronToOnCalendar(expression string) string {
	f := strings.Fields(expression)
	// systemd order is weekday year-month-day hour:minute:second.
	weekday := "*"
	if f[4] != "*" {
		weekday = f[4]
	}
	return weekday + " *-" + f[3] + "-" + f[2] + " " + f[1] + ":" + f[0] + ":00"
}

func (manager Manager) renderSystemd(configDir string, state State, executable string) ([]byte, []byte, error) {
	args := scheduledArguments(executable, configDir, state)
	quoted := make([]string, len(args))
	for i, arg := range args {
		quoted[i] = systemdEscape(arg)
	}
	var service strings.Builder
	service.WriteString("[Unit]\nDescription=resticctl " + state.Action + " for " + state.Profile + "\n")
	if state.Network {
		service.WriteString("Wants=network-online.target\nAfter=network-online.target\n")
	}
	if state.ACPower {
		service.WriteString("ConditionACPower=true\n")
	}
	service.WriteString("[Service]\nType=oneshot\nExecStart=" + strings.Join(quoted, " ") + "\n")
	if manager.environmentPath != "" {
		service.WriteString("Environment=" + systemdEscape("PATH="+manager.environmentPath) + "\n")
	}
	if state.User != "" && state.Permission == PermissionSystem {
		service.WriteString("User=" + strings.ReplaceAll(state.User, "%", "%%") + "\n")
	}
	if state.Priority == PriorityBackground {
		service.WriteString("Nice=10\nIOSchedulingClass=idle\n")
	}
	if state.Log != "" {
		logPath := strings.ReplaceAll(state.Log, "%", "%%")
		service.WriteString("StandardOutput=append:" + logPath + "\nStandardError=append:" + logPath + "\n")
	}
	var timer strings.Builder
	timer.WriteString("[Unit]\nDescription=Timer for " + nativeID(state) + "\n[Timer]\n")
	for _, expression := range state.Expressions {
		timer.WriteString("OnCalendar=" + cronToOnCalendar(expression) + "\n")
	}
	if state.CatchUp {
		timer.WriteString("Persistent=true\n")
	}
	timer.WriteString("Unit=" + nativeID(state) + ".service\n[Install]\nWantedBy=timers.target\n")
	return []byte(service.String()), []byte(timer.String()), nil
}

func (manager Manager) installSystemd(ctx context.Context, configDir string, state *State, executable string) error {
	service, timer, err := manager.renderSystemd(configDir, *state, executable)
	if err != nil {
		return err
	}
	dir := manager.systemdDir(*state)
	if !filepath.IsAbs(dir) {
		return errors.New("systemd unit directory must be absolute")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	base := strings.TrimSuffix(state.JobFile, ".timer")
	servicePath, timerPath := base+".service", base+".timer"
	if err := securefile.WriteAtomic(servicePath, service); err != nil {
		return err
	}
	if err := securefile.WriteAtomic(timerPath, timer); err != nil {
		_ = os.Remove(servicePath)
		return err
	}
	state.JobFile = timerPath
	args := systemctlArguments(*state)
	if _, err := manager.executor.Run(ctx, nil, "systemctl", append(args, "daemon-reload")...); err != nil {
		return err
	}
	if state.Enabled {
		if state.Start {
			args = append(args, "enable", "--now", filepath.Base(timerPath))
		} else {
			args = append(args, "enable", filepath.Base(timerPath))
		}
		_, err = manager.executor.Run(ctx, nil, "systemctl", args...)
		return err
	}
	if state.Start {
		_, err = manager.executor.Run(ctx, nil, "systemctl", append(args, "start", filepath.Base(timerPath))...)
		return err
	}
	return nil
}

func (manager Manager) removeSystemd(ctx context.Context, state *State) error {
	dir := manager.systemdDir(*state)
	base := filepath.Join(dir, nativeID(*state))
	args := systemctlArguments(*state)
	var cleanupErrors []error
	output, err := manager.executor.Run(ctx, nil, "systemctl", append(args, "disable", "--now", filepath.Base(base)+".timer")...)
	if err != nil && !systemdUnitNotFound(output) {
		cleanupErrors = append(cleanupErrors, commandError("disable systemd timer", output, err))
	}
	for _, suffix := range []string{".timer", ".service"} {
		if err := os.Remove(base + suffix); err != nil && !errors.Is(err, os.ErrNotExist) {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("cannot remove systemd unit %s: %w", base+suffix, err))
		}
	}
	output, err = manager.executor.Run(ctx, nil, "systemctl", append(args, "daemon-reload")...)
	if err != nil {
		cleanupErrors = append(cleanupErrors, commandError("reload systemd units", output, err))
	}
	return errors.Join(cleanupErrors...)
}

func systemdUnitNotFound(output []byte) bool {
	message := strings.ToLower(string(output))
	return strings.Contains(message, "not loaded") || strings.Contains(message, "not found") ||
		strings.Contains(message, "does not exist") || strings.Contains(message, "no such file")
}

func systemctlArguments(state State) []string {
	if state.Permission == PermissionSystem {
		return nil
	}
	return []string{"--user"}
}

func (manager Manager) renderWindows(configDir string, state State, executable string) ([]byte, error) {
	args := scheduledArguments(executable, configDir, state)
	triggers, err := windowsTriggers(state.Expressions)
	if err != nil {
		return nil, err
	}
	runLevel := "LeastPrivilege"
	logonType := "InteractiveToken"
	user := state.User
	if state.Permission == PermissionSystem {
		user = "SYSTEM"
		runLevel = "HighestAvailable"
		logonType = "ServiceAccount"
	}
	priority := "5"
	if state.Priority == PriorityBackground {
		priority = "7"
	}
	command, arguments := executable, windowsJoin(args[1:])
	if state.Log != "" {
		command = "powershell.exe"
		parts := make([]string, len(args))
		for i, arg := range args {
			parts[i] = "'" + strings.ReplaceAll(arg, "'", "''") + "'"
		}
		arguments = "-NoProfile -NonInteractive -Command & " + strings.Join(parts, " ") + " *>> '" + strings.ReplaceAll(state.Log, "'", "''") + "'"
	}
	content := fmt.Sprintf(`<?xml version="1.0"?>
<Task version="1.4"><Principals><Principal><UserId>%s</UserId><LogonType>%s</LogonType><RunLevel>%s</RunLevel></Principal></Principals>
<Triggers>%s</Triggers>
<Settings><Priority>%s</Priority><DisallowStartIfOnBatteries>%t</DisallowStartIfOnBatteries><RunOnlyIfNetworkAvailable>%t</RunOnlyIfNetworkAvailable><StartWhenAvailable>%t</StartWhenAvailable><Enabled>%t</Enabled></Settings>
<Actions><Exec><Command>%s</Command><Arguments>%s</Arguments></Exec></Actions></Task>
`, xmlText(user), logonType, runLevel, triggers, priority, state.ACPower, state.Network, state.CatchUp, state.Enabled, xmlText(command), xmlText(arguments))
	return []byte(content), nil
}

func windowsTriggers(expressions []string) (string, error) {
	var triggers strings.Builder
	for _, expression := range expressions {
		fields := strings.Fields(expression)
		if len(fields) != 5 {
			return "", errors.New("Windows backend requires five-field cron expressions")
		}
		if fields[2] != "*" || fields[3] != "*" || fields[4] != "*" {
			return "", errors.New("Windows backend currently supports portable daily/hourly calendar fields only")
		}
		minute, err := strconv.Atoi(fields[0])
		if err != nil {
			return "", errors.New("Windows backend requires a numeric minute")
		}
		hour, repetition, err := windowsHour(fields[1])
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&triggers, "<CalendarTrigger><StartBoundary>2000-01-01T%02d:%02d:00</StartBoundary>%s<ScheduleByDay><DaysInterval>1</DaysInterval></ScheduleByDay></CalendarTrigger>", hour, minute, repetition)
	}
	return triggers.String(), nil
}

func windowsHour(field string) (int, string, error) {
	if field == "*" {
		return 0, "<Repetition><Interval>PT1H</Interval></Repetition>", nil
	}
	hour, err := strconv.Atoi(field)
	if err != nil {
		return 0, "", errors.New("Windows backend requires a numeric hour or *")
	}
	return hour, "", nil
}

func windowsJoin(arguments []string) string {
	quoted := make([]string, len(arguments))
	for i, argument := range arguments {
		quoted[i] = windowsQuote(argument)
	}
	return strings.Join(quoted, " ")
}

// windowsQuote applies the CommandLineToArgvW quoting rules used by task
// scheduler when it starts an executable. strconv.Quote uses Go string
// escaping, which would pass literal backslashes to the Windows process.
func windowsQuote(argument string) string {
	if argument != "" && !strings.ContainsAny(argument, " \t\"") {
		return argument
	}
	var output strings.Builder
	output.WriteByte('"')
	backslashes := 0
	for _, character := range argument {
		if character == '\\' {
			backslashes++
			continue
		}
		if character == '"' {
			output.WriteString(strings.Repeat("\\", backslashes*2+1))
			output.WriteRune(character)
			backslashes = 0
			continue
		}
		output.WriteString(strings.Repeat("\\", backslashes))
		output.WriteRune(character)
		backslashes = 0
	}
	output.WriteString(strings.Repeat("\\", backslashes*2))
	output.WriteByte('"')
	return output.String()
}

func (manager Manager) installWindows(ctx context.Context, configDir string, state *State, executable string) error {
	definition, err := manager.renderWindows(configDir, *state, executable)
	if err != nil {
		return err
	}
	path := state.JobFile
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := securefile.WriteAtomic(path, definition); err != nil {
		return err
	}
	state.JobFile = path
	output, err := manager.executor.Run(ctx, nil, "schtasks", "/Create", "/TN", `\resticctl\`+nativeID(*state), "/XML", path, "/F")
	if err != nil {
		return commandError("install Windows task", output, err)
	}
	return nil
}

func (manager Manager) removeWindows(ctx context.Context, state *State) error {
	output, err := manager.executor.Run(ctx, nil, "schtasks", "/Delete", "/TN", `\resticctl\`+nativeID(*state), "/F")
	var cleanupErrors []error
	if err != nil && !windowsTaskNotFound(output) {
		cleanupErrors = append(cleanupErrors, commandError("remove Windows task", output, err))
	}
	if state.JobFile != "" {
		if err := os.Remove(state.JobFile); err != nil && !errors.Is(err, os.ErrNotExist) {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("cannot remove Windows task definition %s: %w", state.JobFile, err))
		}
	}
	return errors.Join(cleanupErrors...)
}

func windowsTaskNotFound(output []byte) bool {
	message := strings.ToLower(string(output))
	return strings.Contains(message, "cannot find the file specified") ||
		strings.Contains(message, "cannot find the task") || strings.Contains(message, "does not exist")
}

func (manager Manager) nativeDefinition(state State) ([]byte, error) {
	if state.JobFile == "" {
		return nil, fmt.Errorf("%w: job file is missing", errDefinitionDrift)
	}
	definition, err := os.ReadFile(state.JobFile)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%w: job file is missing: %s", errDefinitionDrift, state.JobFile)
		}
		return nil, err
	}
	if state.Backend == BackendSystemd {
		servicePath := strings.TrimSuffix(state.JobFile, ".timer") + ".service"
		service, serviceErr := os.ReadFile(servicePath)
		if serviceErr != nil {
			if errors.Is(serviceErr, os.ErrNotExist) {
				return nil, fmt.Errorf("%w: service file is missing: %s", errDefinitionDrift, servicePath)
			}
			return nil, serviceErr
		}
		definition = append(service, definition...)
	}
	return definition, nil
}

func (manager Manager) verifyNative(ctx context.Context, state State) error {
	if state.Backend == BackendSystemd {
		args := systemctlArguments(state)
		unit := filepath.Base(state.JobFile)
		if state.Enabled {
			output, err := manager.executor.Run(ctx, nil, "systemctl", append(args, "is-enabled", unit)...)
			if err != nil {
				return fmt.Errorf("%w: %v", ErrDrift, commandError("inspect enabled systemd timer", output, err))
			}
		}
		if state.Start {
			output, err := manager.executor.Run(ctx, nil, "systemctl", append(args, "is-active", unit)...)
			if err != nil {
				return fmt.Errorf("%w: %v", ErrDrift, commandError("inspect active systemd timer", output, err))
			}
		}
		return nil
	}
	output, err := manager.executor.Run(ctx, nil, "schtasks", "/Query", "/TN", `\resticctl\`+nativeID(state))
	if err != nil {
		return fmt.Errorf("%w: %v", ErrDrift, commandError("inspect Windows task", output, err))
	}
	return nil
}
