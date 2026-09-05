package profile

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
)

var validName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

const maxNameLength = 128

func ValidateName(name string) error {
	if !isPortableName(name) {
		return fmt.Errorf("invalid profile name: %s", name)
	}
	return nil
}

func List(configDir string) ([]string, error) {
	entries, err := os.ReadDir(configDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("cannot list profiles in %s: %w", configDir, err)
	}
	var profiles []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.Type().IsRegular() && strings.HasSuffix(name, ".json") &&
			!strings.HasSuffix(name, ".credentials.json") && !strings.HasSuffix(name, ".private.json") {
			profileName := strings.TrimSuffix(name, ".json")
			if isPortableName(profileName) {
				profiles = append(profiles, profileName)
			}
		}
	}
	sort.Strings(profiles)
	return profiles, nil
}

func isPortableName(name string) bool {
	if len(name) == 0 || len(name) > maxNameLength || !validName.MatchString(name) || strings.HasSuffix(name, ".") {
		return false
	}
	base := strings.ToUpper(strings.SplitN(name, ".", 2)[0])
	if base == "CON" || base == "PRN" || base == "AUX" || base == "NUL" {
		return false
	}
	if len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && base[3] >= '1' && base[3] <= '9' {
		return false
	}
	return true
}
