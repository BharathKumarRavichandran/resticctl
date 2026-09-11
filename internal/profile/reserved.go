package profile

import "strings"

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
	return argument == "--dry-run" || argument == "-n" ||
		strings.EqualFold(argument, "--dry-run=true") || strings.EqualFold(argument, "-n=true")
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
