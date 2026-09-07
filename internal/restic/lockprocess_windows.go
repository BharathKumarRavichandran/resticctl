//go:build windows

package restic

import (
	"errors"

	"golang.org/x/sys/windows"
)

func processActive(pid int) (bool, error) {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	windows.CloseHandle(handle)
	return true, nil
}
