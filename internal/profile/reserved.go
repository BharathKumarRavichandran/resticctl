package profile

import (
	"strconv"
	"strings"
)

// IsReservedOption reports whether an argument could override the repository
// or password source managed by resticctl.
func IsReservedOption(argument string) bool {
	return IsReservedCommandOption(argument, "")
}

// IsReservedCommandOption accounts for command-specific shorthand values.
func IsReservedCommandOption(argument, command string) bool {
	if argument == "--" {
		return true
	}
	if strings.HasPrefix(argument, "-") && !strings.HasPrefix(argument, "--") {
		for _, flag := range argument[1:] {
			switch flag {
			case 'r', 'p':
				return true
			default:
				if !shortFlagHasNoValue(flag, command) {
					return false
				}
			}
		}
	}
	for _, option := range []string{"--repo", "--repository", "--repository-file", "--password", "--password-file", "--password-command", "--insecure-no-password", "--from-repo", "--from-repository-file", "--from-password-file", "--from-password-command", "--from-insecure-no-password"} {
		if argument == option || strings.HasPrefix(argument, option+"=") {
			return true
		}
	}
	return false
}

// IsDryRunOption reports whether argument enables Restic's dry-run mode.
func IsDryRunOption(argument string) bool {
	enabled, _ := dryRunValue(argument)
	return enabled
}

func dryRunValue(argument string) (bool, bool) {
	if argument == "--dry-run" {
		return true, true
	}
	if value, ok := strings.CutPrefix(argument, "--dry-run="); ok {
		enabled, err := strconv.ParseBool(value)
		return enabled, err == nil
	}
	if !strings.HasPrefix(argument, "-") || strings.HasPrefix(argument, "--") {
		return false, false
	}
	enabled, found := false, false
	for index, flag := range argument[1:] {
		if flag == 'n' {
			if value, ok := strings.CutPrefix(argument[index+2:], "="); ok {
				enabled, err := strconv.ParseBool(value)
				return enabled, err == nil
			}
			enabled, found = true, true
			continue
		}
		if !shortFlagHasNoValue(flag, "") {
			break
		}
	}
	return enabled, found
}

func shortFlagHasNoValue(flag rune, command string) bool {
	switch flag {
	case 'q', 'v', 'h':
		return true
	case 'n':
		return command == "" || command == "backup" || command == "forget" || command == "prune"
	case 'f', 'x':
		return command == "" || command == "backup"
	case 'l':
		return command == "" || command == "ls" || command == "find"
	case 'i', 'R':
		return command == "" || command == "find"
	case 'c':
		return command == "" || command == "snapshots"
	}
	return false
}

// DryRunEnabled follows Restic's last-value-wins boolean flag semantics.
func DryRunEnabled(arguments []string) bool {
	enabled := false
	for _, argument := range arguments {
		if argument == "--" {
			break
		}
		if value, found := dryRunValue(argument); found {
			enabled = value
		}
	}
	return enabled
}

// IsStreamingOption reports whether argument selects Restic's stdin backup
// mode, which is owned by the stream profile section.
func IsStreamingOption(argument string) bool {
	for _, option := range []string{"--stdin", "--stdin-filename", "--stdin-from-command"} {
		if argument == option || strings.HasPrefix(argument, option+"=") {
			return true
		}
	}
	return false
}

// IsReservedEnvironment reports whether resticctl, rather than profile
// credentials, must control the environment variable.
func IsReservedEnvironment(key string) bool {
	switch strings.ToUpper(key) {
	case "RESTIC_REPOSITORY", "RESTIC_REPOSITORY_FILE", "RESTIC_PASSWORD", "RESTIC_PASSWORD_FILE", "RESTIC_PASSWORD_COMMAND",
		"RESTIC_FROM_REPOSITORY", "RESTIC_FROM_REPOSITORY_FILE", "RESTIC_FROM_PASSWORD", "RESTIC_FROM_PASSWORD_FILE", "RESTIC_FROM_PASSWORD_COMMAND":
		return true
	default:
		return false
	}
}

// CommandDryRun reports the effective dry-run flag across global and command arguments.
func (value Profile) CommandDryRun(command string) bool {
	arguments := append([]string(nil), value.ResticArgs...)
	switch command {
	case "backup":
		arguments = append(arguments, value.BackupArgs...)
	case "forget":
		arguments = append(arguments, value.ForgetArgs...)
	case "check":
		arguments = append(arguments, value.CheckArgs...)
	}
	arguments = append(arguments, value.Commands[command].Args...)
	return DryRunEnabled(arguments)
}
