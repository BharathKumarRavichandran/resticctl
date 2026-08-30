//go:build !windows

package app

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

func platformConfigDir() string {
	if value := os.Getenv("XDG_CONFIG_HOME"); value != "" {
		return filepath.Join(value, "resticctl")
	}
	return ""
}

func normalizeEnvKey(key string) string { return key }

func ensureFileSecurity(info os.FileInfo, path, label string) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("cannot inspect owner of %s file: %s", label, path)
	}
	if stat.Uid != uint32(os.Getuid()) {
		return fmt.Errorf("%s file is not owned by the current user: %s", label, path)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("%s file must not be accessible by group or others: %s", label, path)
	}
	return nil
}
