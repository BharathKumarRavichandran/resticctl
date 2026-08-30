//go:build !windows

package runstatus

import "os"

func replaceFile(source, destination string) error {
	return os.Rename(source, destination)
}
