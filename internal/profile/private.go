package profile

import (
	"errors"
	"fmt"
	"io"
	"os"
)

func ensurePrivateFile(path, label string) error {
	info, err := ensureRegularFile(path, label)
	if err != nil {
		return err
	}
	return ensureFileSecurity(info, path, label)
}

func ensureRegularFile(path, label string) (os.FileInfo, error) {
	file, info, err := openRegularFile(path, label)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return info, nil
}

func readRegularFile(path, label string) ([]byte, os.FileInfo, error) {
	file, info, err := openRegularFile(path, label)
	if err != nil {
		return nil, nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil {
		return nil, nil, fmt.Errorf("cannot load %s %s: %w", label, path, err)
	}
	return data, info, nil
}

func openRegularFile(path, label string) (*os.File, os.FileInfo, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, fmt.Errorf("%s file not found: %s", label, path)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("cannot open %s file %s: %w", label, path, err)
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, nil, fmt.Errorf("cannot inspect %s file %s: %w", label, path, err)
	}
	if !info.Mode().IsRegular() {
		file.Close()
		return nil, nil, fmt.Errorf("%s file is not a regular file: %s", label, path)
	}
	return file, info, nil
}
