package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
)

const Version = "0.1.0"

func Run(ctx context.Context, arguments []string, stdout, stderr io.Writer) (int, error) {
	global := flag.NewFlagSet("resticctl", flag.ContinueOnError)
	global.SetOutput(io.Discard)
	configDir := global.String("config-dir", "", "profile directory (default: platform config directory)")
	version := global.Bool("version", false, "show version")
	global.Usage = func() {
		fmt.Fprintln(stderr, "usage: resticctl [--config-dir DIR] COMMAND [OPTIONS]")
		fmt.Fprintln(stderr, "commands: create, list, init, backup, snapshots, check, forget, restore")
	}
	if err := global.Parse(arguments); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0, nil
		}
		return 2, err
	}
	if *version {
		fmt.Fprintf(stdout, "resticctl %s\n", Version)
		return 0, nil
	}
	remaining := global.Args()
	if len(remaining) == 0 {
		global.Usage()
		return 2, errors.New("command is required")
	}
	action := remaining[0]
	commandArgs := remaining[1:]
	if *configDir == "" {
		var err error
		*configDir, err = DefaultConfigDir()
		if err != nil {
			return 1, err
		}
	}

	switch action {
	case "create":
		if err := requireArguments(action, commandArgs, 1); err != nil {
			return 2, err
		}
		profile, credentials, err := CreateProfile(*configDir, commandArgs[0])
		if err != nil {
			return 1, err
		}
		fmt.Fprintf(stdout, "Created profile:\n  %s\n  %s\n", profile, credentials)
		fmt.Fprintf(stdout, "Edit both files before running: resticctl init %s\n", commandArgs[0])
		return 0, nil
	case "list":
		if err := requireArguments(action, commandArgs, 0); err != nil {
			return 2, err
		}
		profiles, err := ListProfiles(*configDir)
		if err != nil {
			return 1, err
		}
		if len(profiles) == 0 {
			return 1, fmt.Errorf("no profiles found in %s", *configDir)
		}
		fmt.Fprintln(stdout, strings.Join(profiles, "\n"))
		return 0, nil
	}
	expected, allowed, ok := commandShape(action)
	if !ok {
		return 2, fmt.Errorf("unknown command: %s", action)
	}

	positionals, options, err := parseCommandOptions(commandArgs, allowed)
	if err != nil {
		return 2, err
	}
	if err := requireArguments(action, positionals, expected); err != nil {
		return 2, err
	}
	profile, err := LoadProfile(*configDir, positionals[0])
	if err != nil {
		return 1, err
	}
	restic, err := NewRestic(os.Stdin, stdout, stderr)
	if err != nil {
		return 1, err
	}

	switch action {
	case "init":
		err = restic.Run(ctx, profile, []string{"init"}, "")
	case "backup":
		err = Backup(ctx, restic, profile, options["--dry-run"], stdout)
	case "snapshots":
		err = Snapshots(ctx, restic, profile)
	case "check":
		err = Check(ctx, restic, profile)
	case "forget":
		err = Forget(ctx, restic, profile, options["--dry-run"], options["--prune"])
	case "restore":
		err = Restore(ctx, restic, profile, positionals[1], positionals[2], options["--dry-run"])
	default:
		return 2, fmt.Errorf("unknown command: %s", action)
	}
	if err != nil {
		return 1, err
	}
	return 0, nil
}

func commandShape(action string) (int, map[string]bool, bool) {
	switch action {
	case "backup", "restore":
		arguments := 1
		if action == "restore" {
			arguments = 3
		}
		return arguments, map[string]bool{"--dry-run": true}, true
	case "forget":
		return 1, map[string]bool{"--dry-run": true, "--prune": true}, true
	case "init", "snapshots", "check":
		return 1, nil, true
	default:
		return 0, nil, false
	}
}

func parseCommandOptions(arguments []string, allowed map[string]bool) ([]string, map[string]bool, error) {
	positionals := make([]string, 0, len(arguments))
	options := make(map[string]bool)
	for _, argument := range arguments {
		if strings.HasPrefix(argument, "-") {
			if !allowed[argument] {
				return nil, nil, fmt.Errorf("unknown option: %s", argument)
			}
			options[argument] = true
		} else {
			positionals = append(positionals, argument)
		}
	}
	return positionals, options, nil
}

func requireArguments(action string, arguments []string, count int) error {
	if len(arguments) != count {
		return fmt.Errorf("%s requires %d argument(s), got %d", action, count, len(arguments))
	}
	return nil
}
