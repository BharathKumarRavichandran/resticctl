package restic

import (
	"sort"
	"strings"

	"resticctl/internal/profile"
)

func mergeEnvironment(base []string, overrides map[string]string) []string {
	type variable struct{ key, value string }
	values := make(map[string]variable, len(base)+len(overrides))
	for _, entry := range base {
		if index := strings.IndexByte(entry, '='); index >= 0 {
			key := entry[:index]
			if !profile.IsReservedEnvironment(key) {
				values[normalizeEnvKey(key)] = variable{key, entry[index+1:]}
			}
		}
	}
	for key, value := range overrides {
		if !profile.IsReservedEnvironment(key) {
			values[normalizeEnvKey(key)] = variable{key, value}
		}
	}
	result := make([]string, 0, len(values))
	for _, item := range values {
		result = append(result, item.key+"="+item.value)
	}
	sort.Strings(result)
	return result
}
