//go:build windows

package schedule

import (
	"strings"

	"golang.org/x/sys/windows"
)

func taskUserMatches(expected, actual string) bool {
	if strings.EqualFold(expected, actual) {
		return true
	}
	sid, _, _, err := windows.LookupSID("", expected)
	return err == nil && strings.EqualFold(sid.String(), actual)
}
