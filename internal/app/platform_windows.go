//go:build windows

package app

import (
	"os"
	"path/filepath"
	"strings"
)

func platformConfigDir() string {
	if value := os.Getenv("APPDATA"); value != "" {
		return filepath.Join(value, "resticctl")
	}
	return ""
}

func ensureFileSecurity(os.FileInfo, string, string) error { return nil }

func normalizeEnvKey(key string) string { return strings.ToUpper(key) }
