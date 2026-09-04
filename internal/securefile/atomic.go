package securefile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// WriteAtomic replaces path with private file contents from the same directory.
func WriteAtomic(path string, data []byte) (err error) {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".resticctl-*")
	if err != nil {
		return fmt.Errorf("cannot create temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	closed := false
	keep := false
	defer func() {
		if !closed {
			err = errors.Join(err, temporary.Close())
		}
		if !keep {
			if removeErr := os.Remove(temporaryPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
				err = errors.Join(err, fmt.Errorf("cannot remove temporary file: %w", removeErr))
			}
		}
	}()
	if err := Protect(temporaryPath); err != nil {
		return fmt.Errorf("cannot protect temporary file: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		return fmt.Errorf("cannot write temporary file: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("cannot sync temporary file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		closed = true
		return fmt.Errorf("cannot close temporary file: %w", err)
	}
	closed = true
	if err := replace(temporaryPath, path); err != nil {
		return fmt.Errorf("cannot replace %s: %w", path, err)
	}
	keep = true
	return nil
}
