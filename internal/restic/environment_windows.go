//go:build windows

package restic

import "strings"

func normalizeEnvKey(key string) string { return strings.ToUpper(key) }
