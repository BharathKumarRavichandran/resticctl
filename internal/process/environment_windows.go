//go:build windows

package process

import "strings"

func normalizeEnvKey(key string) string { return strings.ToUpper(key) }
