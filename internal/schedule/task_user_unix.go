//go:build !windows

package schedule

import "strings"

func taskUserMatches(expected, actual string) bool {
	return strings.EqualFold(expected, actual)
}
