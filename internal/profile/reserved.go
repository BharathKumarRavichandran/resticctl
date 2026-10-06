package profile

import (
	"strconv"
	"strings"
)

// IsReservedOption reports whether an argument could override the repository
// or password source managed by resticctl.
func IsReservedOption(argument string) bool {
	if argument == "--" || strings.HasPrefix(argument, "-r") || strings.HasPrefix(argument, "-p") {
		return true
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
	if argument == "--dry-run" || argument == "-n" {
		return true, true
	}
	for _, prefix := range []string{"--dry-run=", "-n="} {
		if value, found := strings.CutPrefix(argument, prefix); found {
			enabled, err := strconv.ParseBool(value)
			return enabled, err == nil
		}
	}
	return false, false
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
