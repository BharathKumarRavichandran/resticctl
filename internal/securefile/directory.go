package securefile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// MakePrivateDir creates and protects a directory tree below root without
// following symbolic links within the tree.
func MakePrivateDir(root, directory string) error {
	relative, err := filepath.Rel(root, directory)
	if err != nil {
		return err
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("directory %s is outside %s", directory, root)
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return err
	}
	paths := []string{root}
	if relative != "." {
		path := root
		for _, part := range strings.Split(relative, string(filepath.Separator)) {
			path = filepath.Join(path, part)
			paths = append(paths, path)
		}
	}
	for _, path := range paths {
		if err := os.Mkdir(path, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
			return err
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return fmt.Errorf("private directory %s must be a directory, not a symbolic link or file", path)
		}
		if err := Protect(path); err != nil {
			return err
		}
	}
	return nil
}
