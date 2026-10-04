package securefile

import (
	"errors"
	"fmt"
	"os"
)

// Temporary owns a private temporary file. Cleanup closes it and removes its path.
// Callers must clean up unpublished files on every exit.
type Temporary struct {
	file     *os.File
	closed   bool
	closeErr error
	removed  bool
}

func CreateTemp(directory, pattern string) (*Temporary, error) {
	file, err := os.CreateTemp(directory, pattern)
	if err != nil {
		return nil, fmt.Errorf("cannot create temporary file: %w", err)
	}
	temporary := &Temporary{file: file}
	if err := Protect(file.Name()); err != nil {
		return nil, errors.Join(fmt.Errorf("cannot protect temporary file: %w", err), temporary.Cleanup())
	}
	return temporary, nil
}

func (file *Temporary) Name() string                   { return file.file.Name() }
func (file *Temporary) Write(data []byte) (int, error) { return file.file.Write(data) }
func (file *Temporary) Sync() error                    { return file.file.Sync() }

func (file *Temporary) Close() error {
	if !file.closed {
		file.closed = true
		file.closeErr = file.file.Close()
	}
	return file.closeErr
}

// Remove is idempotent, but preserves permission and other material failures.
func Remove(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("cannot remove temporary file: %w", err)
	}
	return nil
}

func (file *Temporary) Cleanup() error {
	closeErr := file.Close()
	if file.removed {
		return closeErr
	}
	removeErr := Remove(file.Name())
	file.removed = removeErr == nil
	return errors.Join(closeErr, removeErr)
}

// WriteTemporary writes and closes a private file. The caller must remove a
// successful result. A positive limit bounds data; zero permits any size.
// The caller retains ownership of data and is responsible for clearing secrets.
func WriteTemporary(directory, pattern string, data []byte, limit int) (path string, err error) {
	if limit < 0 || (limit > 0 && len(data) > limit) {
		return "", errors.New("temporary file data exceeds size limit")
	}
	file, err := CreateTemp(directory, pattern)
	if err != nil {
		return "", err
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, file.Cleanup())
		}
	}()
	if _, err = file.Write(data); err != nil {
		return "", fmt.Errorf("cannot write temporary file: %w", err)
	}
	if err = file.Close(); err != nil {
		return "", fmt.Errorf("cannot close temporary file: %w", err)
	}
	return file.Name(), nil
}
