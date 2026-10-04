//go:build !windows

package process

// NormalizeEnvironmentKey follows platform environment key semantics.
func NormalizeEnvironmentKey(key string) string { return key }
