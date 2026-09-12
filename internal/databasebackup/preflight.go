package databasebackup

import (
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"sort"
	"strings"

	"resticctl/internal/profile"
)

// Preflight verifies that every database client needed by a profile can be
// resolved in the current process environment. Execution remains authoritative
// because the filesystem or PATH may change after this check.
func Preflight(backupProfile profile.Profile) error {
	return preflight(backupProfile, exec.LookPath)
}

func preflight(backupProfile profile.Profile, lookPath func(string) (string, error)) error {
	requested := make(map[string][]string)
	for _, provider := range Providers(backupProfile) {
		for _, executable := range provider.Executables() {
			if !slices.Contains(requested[executable.Name], executable.Purpose) {
				requested[executable.Name] = append(requested[executable.Name], executable.Purpose)
			}
		}
	}
	var result error
	executables := make([]string, 0, len(requested))
	for executable := range requested {
		executables = append(executables, executable)
	}
	sort.Strings(executables)
	for _, executable := range executables {
		if _, err := lookPath(executable); err != nil {
			purposes := requested[executable]
			sort.Strings(purposes)
			result = errors.Join(result, fmt.Errorf("required database client for %s not found: %s", strings.Join(purposes, " and "), executable))
		}
	}
	return result
}
