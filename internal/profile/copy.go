package profile

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func loadCopyTargets(value *Profile, base string) error {
	names := make([]string, 0, len(value.Copies))
	for name := range value.Copies {
		names = append(names, name)
	}
	sort.Strings(names)
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		if !isPortableName(name) {
			return fmt.Errorf("invalid copy target name: %s", name)
		}
		key := strings.ToLower(name)
		if _, ok := seen[key]; ok {
			return fmt.Errorf("duplicate copy target name: %s", name)
		}
		seen[key] = struct{}{}
		target := value.Copies[name]
		if target.Repository == "" || strings.ContainsRune(target.Repository, 0) {
			return fmt.Errorf("copies.%s.repository must be a non-empty string without NUL bytes", name)
		}
		if sameRepository(value.Repository, target.Repository) {
			return fmt.Errorf("copies.%s.repository must differ from the source repository", name)
		}
		if target.CredentialsFile == "" {
			return fmt.Errorf("copies.%s.credentials_file is required", name)
		}
		path, err := expandPath(target.CredentialsFile, base)
		if err != nil {
			return fmt.Errorf("invalid copies.%s.credentials_file: %w", name, err)
		}
		target.CredentialsFile = path
		target.Credentials, err = loadCopyCredentials(path)
		if err != nil {
			return fmt.Errorf("copies.%s: %w", name, err)
		}
		if target.CopyChunkerParams && !target.InitializeRepository {
			return fmt.Errorf("copies.%s.copy_chunker_params requires initialize_repository", name)
		}
		if err := validateCopyTarget(name, &target); err != nil {
			return err
		}
		if err := validateCopyEnvironment(value.Credentials.Environment, target.Credentials.Environment, name); err != nil {
			return err
		}
		value.Copies[name] = target
	}
	return nil
}

func validateCopyTarget(name string, target *CopyTarget) error {
	lists := []struct {
		field  string
		values []string
	}{
		{"args", target.Args},
		{"snapshot_ids", target.SnapshotIDs},
		{"hosts", target.Hosts},
		{"tags", target.Tags},
		{"paths", target.Paths},
	}
	for _, list := range lists {
		field, values := list.field, list.values
		for _, item := range values {
			if item == "" || strings.ContainsRune(item, 0) {
				return fmt.Errorf("copies.%s.%s must not contain empty strings or NUL bytes", name, field)
			}
			if field == "args" && IsReservedOption(item) {
				return fmt.Errorf("copies.%s.args must not override repository or password options: %s", name, item)
			}
			if field == "args" && IsDryRunOption(item) {
				return fmt.Errorf("copies.%s.args must not set workflow-owned dry-run option: %s", name, item)
			}
		}
	}
	hookLists := []struct {
		field string
		hooks []Hook
	}{
		{"run_before", target.RunBefore},
		{"run_after", target.RunAfter},
		{"run_after_fail", target.RunAfterFail},
		{"run_finally", target.RunFinally},
	}
	for _, list := range hookLists {
		if err := validateHooks("copies."+name+"."+list.field, list.hooks); err != nil {
			return err
		}
	}
	return nil
}

func validateHooks(field string, hooks []Hook) error {
	for index, hook := range hooks {
		if len(hook.Command) == 0 {
			return fmt.Errorf("%s[%d].command must contain at least one argument", field, index)
		}
		for _, part := range hook.Command {
			if part == "" || strings.ContainsRune(part, 0) {
				return fmt.Errorf("%s[%d].command must not contain empty arguments or NUL bytes", field, index)
			}
		}
		if hook.Timeout != "" {
			timeout, err := time.ParseDuration(hook.Timeout)
			if err != nil || timeout <= 0 {
				return fmt.Errorf("%s[%d].timeout must be a positive duration", field, index)
			}
		}
	}
	return nil
}

func validateCopyEnvironment(source, destination map[string]string, name string) error {
	destinationValues := make(map[string]string, len(destination))
	for key, value := range destination {
		destinationValues[strings.ToUpper(key)] = value
	}
	for _, sourceKey := range sortedMapKeys(source) {
		if destinationValue, ok := destinationValues[strings.ToUpper(sourceKey)]; ok && source[sourceKey] != destinationValue {
			return fmt.Errorf("copies.%s credentials conflict with source environment key %s", name, sourceKey)
		}
	}
	return nil
}

func (value Profile) CopyTargetNames() []string {
	return sortedMapKeys(value.Copies)
}

func sameRepository(source, destination string) bool {
	if source == destination {
		return true
	}
	a, aAbsolute, aok := localRepositoryPath(source)
	b, bAbsolute, bok := localRepositoryPath(destination)
	return aok && bok && aAbsolute == bAbsolute && a == b
}

func localRepositoryPath(repository string) (string, bool, bool) {
	if strings.HasPrefix(repository, "local:") {
		repository = strings.TrimPrefix(repository, "local:")
	} else if strings.Contains(repository, ":") && !filepath.IsAbs(repository) {
		return "", false, false
	}
	return filepath.Clean(repository), filepath.IsAbs(repository), true
}
