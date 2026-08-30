//go:build !windows

package schedule

import "os"

func platformUID() int { return os.Getuid() }
