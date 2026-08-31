//go:build !windows

package process

func normalizeEnvKey(key string) string { return key }
