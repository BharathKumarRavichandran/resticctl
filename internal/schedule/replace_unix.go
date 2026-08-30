//go:build !windows

package schedule

import "os"

func replaceFile(source, destination string) error {
	return os.Rename(source, destination)
}
