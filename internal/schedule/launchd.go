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

func (manager Manager) installLaunchd(ctx context.Context, configDir string, state State, fields []string, executable string) (string, error) {
	content, err := manager.renderLaunchd(configDir, state, executable)
	if err != nil {
		return "", err
	}
	path, err := manager.launchdJobPath(state.Profile, state.Action)
	if err != nil {
		return "", err
	}
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return "", fmt.Errorf("cannot create schedule directory: %w", err)
	}
	if err := securefile.WriteAtomic(path, content); err != nil {
		return "", fmt.Errorf("cannot write launchd job %s: %w", path, err)
	}
	if !state.Start {
		return path, nil
	}
	label := launchdLabel(state.Profile, state.Action)
	domain := "gui/" + strconv.Itoa(manager.uid)
	_, _ = manager.executor.Run(ctx, nil, "launchctl", "bootout", domain+"/"+label)
	output, err := manager.executor.Run(ctx, nil, "launchctl", "bootstrap", domain, path)
	if err != nil {
		_ = os.Remove(path)
		return "", commandError("install launchd job", output, err)
	}
	legacyPath := filepath.Join(configDir, "schedules", label+".plist")
	if state.Action == ActionBackup && legacyPath != path {
		_ = os.Remove(legacyPath)
	}
	return path, nil
}

func (manager Manager) renderLaunchd(configDir string, state State, executable string) ([]byte, error) {
	calendars := make([]string, 0, len(state.Expressions))
	for _, expression := range state.Expressions {
		calendar, err := launchdCalendar(strings.Fields(expression))
		if err != nil {
			return nil, err
		}
		calendars = append(calendars, calendar)
	}
	calendar := strings.Join(calendars, "")
	if len(calendars) > 1 {
		calendar = "<array>" + calendar + "</array>"
	}
	label := launchdLabel(state.Profile, state.Action)
	arguments := scheduledArguments(executable, configDir, state)
	var argumentXML strings.Builder
	for _, argument := range arguments {
		fmt.Fprintf(&argumentXML, "<string>%s</string>", xmlText(argument))
	}
	content := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>Label</key><string>` + xmlText(label) + `</string>
<key>ProgramArguments</key><array>` + argumentXML.String() + `</array>
` + launchdEnvironment(manager.environmentPath) + `
<key>StartCalendarInterval</key>` + calendar + `
` + launchdRunAtLoad(state.CatchUp) + `
` + launchdPolicy(state) + `
</dict></plist>
`
	return []byte(content), nil
}

func launchdPolicy(state State) string {
	var b strings.Builder
	processType := "Adaptive"
	if state.Priority == PriorityBackground {
		processType = "Background"
	}
	b.WriteString("<key>ProcessType</key><string>" + processType + "</string>\n")
	if state.Log != "" {
		b.WriteString("<key>StandardOutPath</key><string>" + xmlText(state.Log) + "</string><key>StandardErrorPath</key><string>" + xmlText(state.Log) + "</string>\n")
	}
	if state.Network {
		b.WriteString("<key>KeepAlive</key><dict><key>NetworkState</key><true/></dict>\n")
	}
	if state.Priority == PriorityBackground {
		b.WriteString("<key>LowPriorityIO</key><true/>\n")
	}
	return b.String()
}

func (manager Manager) removeLaunchd(ctx context.Context, configDir string, state State) error {
	jobFile, err := manager.launchdJobPath(state.Profile, state.Action)
	if err != nil {
		return err
	}
	domain := "gui/" + strconv.Itoa(manager.uid)
	output, err := manager.executor.Run(ctx, nil, "launchctl", "bootout", domain+"/"+launchdLabel(state.Profile, state.Action))
	if err != nil && !launchdJobNotLoaded(output) {
		return commandError("unload launchd job", output, err)
	}
	jobFiles := []string{
		jobFile,
		filepath.Join(configDir, "schedules", launchdLabel(state.Profile, state.Action)+".plist"),
	}
	for _, jobFile := range jobFiles {
		if err := os.Remove(jobFile); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("cannot remove launchd job %s: %w", jobFile, err)
		}
	}
	return nil
}

func launchdJobNotLoaded(output []byte) bool {
	message := strings.ToLower(string(output))
	return strings.Contains(message, "no such process") ||
		strings.Contains(message, "could not find specified service") ||
		strings.Contains(message, "service not found")
}

func (manager Manager) launchdJobPath(name, action string) (string, error) {
	if manager.launchAgentsDir == "" || !filepath.IsAbs(manager.launchAgentsDir) {
		return "", errors.New("cannot determine an absolute Library/LaunchAgents directory")
	}
	return filepath.Join(manager.launchAgentsDir, launchdLabel(name, action)+".plist"), nil
}

func launchdLabel(name, action string) string {
	if action == ActionBackup {
		return "io.resticctl.backup." + name
	}
	return "io.resticctl." + action + "." + name
}

func launchdEnvironment(path string) string {
	if path == "" {
		return ""
	}
	return "<key>EnvironmentVariables</key><dict><key>PATH</key><string>" + xmlText(path) + "</string></dict>"
}

func launchdRunAtLoad(enabled bool) string {
	if !enabled {
		return ""
	}
	return "<key>RunAtLoad</key><true/>"
}
