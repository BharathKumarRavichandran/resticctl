package app

import (
	"context"
	"fmt"

	"resticctl/internal/profile"
)

// RunRestic executes a supported restic command with profile-independent
// arguments. Repository and credential flags remain controlled by the client.
func RunRestic(ctx context.Context, runner ResticRunner, backupProfile profile.Profile, command string, arguments []string) error {
	if !supportedResticCommand(command) {
		return fmt.Errorf("unsupported restic command %q", command)
	}
	if err := validateResticArguments(arguments); err != nil {
		return err
	}
	return runner.Run(ctx, backupProfile, append([]string{command}, arguments...), "")
}

func supportedResticCommand(command string) bool {
	switch command {
	case "backup", "cache", "cat", "check", "copy", "diff", "dump", "find", "forget", "init", "key", "list", "ls", "migrate", "prune", "rebuild-index", "recover", "repair", "restore", "self-update", "snapshots", "stats", "tag", "unlock":
		return true
	default:
		return false
	}
}

func validateResticArguments(arguments []string) error {
	for _, argument := range arguments {
		if argument == "--" {
			break
		}
		if profile.IsReservedOption(argument) {
			return fmt.Errorf("restic argument %q is reserved by resticctl", argument)
		}
	}
	return nil
}
