package restic

import (
	"resticctl/internal/process"
	"resticctl/internal/profile"
)

func mergeEnvironment(base []string, overrides map[string]string) []string {
	return process.MergeEnvironment(base, overrides, profile.IsReservedEnvironment)
}
