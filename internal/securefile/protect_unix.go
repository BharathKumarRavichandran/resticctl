//go:build !windows

package securefile

import "os"

// Protect restricts a file or directory to its owner.
func Protect(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return os.Chmod(path, 0o700)
	}
	return os.Chmod(path, 0o600)
}
