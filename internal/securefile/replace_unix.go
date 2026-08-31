//go:build !windows

package securefile

import "os"

func replace(source, destination string) error {
	return os.Rename(source, destination)
}
