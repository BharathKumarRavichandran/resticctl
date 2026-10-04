//go:build windows

package process

import "strings"

// NormalizeEnvironmentKey follows platform environment key semantics.
func NormalizeEnvironmentKey(key string) string { return strings.ToUpper(key) }
