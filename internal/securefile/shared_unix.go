//go:build !windows

package securefile

import (
	"errors"
	"os"
)

func ValidateSharedDirectory(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errors.New("path is not a directory")
	}
	if info.Mode().Perm()&0o007 != 0 {
		return errors.New("directory must not be accessible by other users")
	}
	return nil
}
