//go:build windows

package profile

import (
	"os"
	"path/filepath"
)

func platformConfigDir() string {
	if value := os.Getenv("APPDATA"); value != "" {
		return filepath.Join(value, "resticctl")
	}
	return ""
}

func ensureFileSecurity(os.FileInfo, string, string) error { return nil }
