package profile

import "strings"

func isReservedOption(argument string) bool {
	if argument == "--" || strings.HasPrefix(argument, "-r") {
		return true
	}
	for _, option := range []string{"--repo", "--repository-file", "--password-file", "--password-command"} {
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
