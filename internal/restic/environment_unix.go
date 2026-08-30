//go:build !windows

package restic

func normalizeEnvKey(key string) string { return key }
