package app

import (
	"context"
	"fmt"
	"strings"

	"resticctl/internal/profile"
)

// RunRestic executes a supported restic command with profile-independent
// arguments. Repository and credential flags remain controlled by the client.
func RunRestic(ctx context.Context, runner ResticRunner, backupProfile profile.Profile, command string, arguments []string) error {
	if !profile.IsSupportedResticCommand(command) || strings.Contains(command, " ") {
		return fmt.Errorf("unsupported restic command %q", command)
	}
	if err := validateResticArguments(arguments); err != nil {
		return err
	}
	if err := validateResticSubcommand(command, arguments); err != nil {
		return err
	}
	return invokeRestic(ctx, runner, backupProfile, configuredResticArguments(backupProfile, command, arguments), "")
}

func validateResticSubcommand(command string, arguments []string) error {
	if (command != "key" && command != "repair") || len(arguments) == 0 || strings.HasPrefix(arguments[0], "-") {
		return nil
	}
	path := command + " " + arguments[0]
	if !profile.IsSupportedResticCommand(path) {
		return fmt.Errorf("unsupported restic command %q", path)
	}
	return nil
}

func configuredResticArguments(backupProfile profile.Profile, command string, arguments []string) []string {
	result := []string{command}
	if configured, ok := backupProfile.Commands[command]; ok {
		result = append(result, configured.Args...)
	}
	if (command == "key" || command == "repair") && len(arguments) > 0 {
		path := command + " " + arguments[0]
		if profile.IsSupportedResticCommand(path) {
			result = append(result, arguments[0])
			if configured, ok := backupProfile.Commands[path]; ok {
				result = append(result, configured.Args...)
			}
			return append(result, arguments[1:]...)
		}
	}
	return append(result, arguments...)
}

func appendConfiguredCommandArgs(arguments []string, backupProfile profile.Profile, command string) []string {
	if configured, ok := backupProfile.Commands[command]; ok {
		arguments = append(arguments, configured.Args...)
	}
	return arguments
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
