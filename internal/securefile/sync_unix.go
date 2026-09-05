//go:build !windows

package securefile

import (
	"errors"
	"os"
	"path/filepath"
)

func syncParent(path string) error {
	directory, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	return errors.Join(directory.Sync(), directory.Close())
}
