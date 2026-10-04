package securefile

import (
	"errors"
	"fmt"
	"path/filepath"
)

// WriteAtomic replaces path with private file contents from the same directory.
func WriteAtomic(path string, data []byte) (err error) {
	temporary, err := CreateTemp(filepath.Dir(path), ".resticctl-*")
	if err != nil {
		return err
	}
	published := false
	defer func() {
		if !published {
			err = errors.Join(err, temporary.Cleanup())
		}
	}()
	if _, err := temporary.Write(data); err != nil {
		return fmt.Errorf("cannot write temporary file: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("cannot sync temporary file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("cannot close temporary file: %w", err)
	}
	if err := replace(temporary.Name(), path); err != nil {
		return fmt.Errorf("cannot replace %s: %w", path, err)
	}
	published = true
	if err := syncParent(path); err != nil {
		return fmt.Errorf("cannot sync parent directory for %s: %w", path, err)
	}
	return nil
}
