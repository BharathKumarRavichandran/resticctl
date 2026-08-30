package profile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func DefaultDir() (string, error) {
	if value := os.Getenv("RESTICCTL_CONFIG_DIR"); value != "" {
		return expandPath(value, "")
	}
	if value := platformConfigDir(); value != "" {
		return expandPath(value, "")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home directory: %w", err)
	}
	return filepath.Join(home, ".config", "resticctl"), nil
}

func expandPath(value, base string) (string, error) {
	var missing string
	value = os.Expand(value, func(name string) string {
		expanded, ok := os.LookupEnv(name)
		if !ok && missing == "" {
			missing = name
		}
		return expanded
	})
	if missing != "" {
		return "", fmt.Errorf("environment variable %s is not set", missing)
	}
	if value == "~" || strings.HasPrefix(value, "~/") || strings.HasPrefix(value, `~\`) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		value = filepath.Join(home, strings.TrimLeft(value[1:], `/\`))
	} else if strings.HasPrefix(value, "~") {
		return "", errors.New("~user paths are not supported")
	}
	if base != "" && !filepath.IsAbs(value) {
		value = filepath.Join(base, value)
	}
	return filepath.Clean(value), nil
}
