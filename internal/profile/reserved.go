package profile

import "strings"

// IsReservedOption reports whether an argument could override the repository
// or password source managed by resticctl.
func IsReservedOption(argument string) bool {
	if argument == "--" || strings.HasPrefix(argument, "-r") || strings.HasPrefix(argument, "-p") {
		return true
	}
	for _, option := range []string{"--repo", "--repository", "--repository-file", "--password", "--password-file", "--password-command"} {
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
	case "RESTIC_REPOSITORY", "RESTIC_REPOSITORY_FILE", "RESTIC_PASSWORD", "RESTIC_PASSWORD_FILE", "RESTIC_PASSWORD_COMMAND":
		return true
	default:
		return false
	}
}
