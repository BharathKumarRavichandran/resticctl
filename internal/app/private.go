package app

import (
	"errors"
	"fmt"
	"os"
)

func ensurePrivateFile(path, label string) error {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%s file not found: %s", label, path)
	}
	if err != nil {
		return fmt.Errorf("cannot inspect %s file %s: %w", label, path, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s file is not a regular file: %s", label, path)
	}
	return ensureFileSecurity(info, path, label)
}
