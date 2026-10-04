package process

import (
	"sort"
	"strings"
)

// MergeEnvironment applies overrides and removes blocked keys from both sources.
// Keys are case-insensitive on Windows; the output order is deterministic.
func MergeEnvironment(base []string, overrides map[string]string, blocked func(string) bool) []string {
	type variable struct{ key, value string }
	values := make(map[string]variable, len(base)+len(overrides))
	for _, entry := range base {
		index := strings.IndexByte(entry, '=')
		// Windows drive-directory keys start with '=', for example '=C:'.
		if index == 0 {
			next := strings.IndexByte(entry[1:], '=')
			if next < 0 {
				continue
			}
			index = next + 1
		}
		if index > 0 {
			key := entry[:index]
			if blocked == nil || !blocked(key) {
				values[NormalizeEnvironmentKey(key)] = variable{key, entry[index+1:]}
			}
		}
	}
	for key, value := range overrides {
		if blocked == nil || !blocked(key) {
			values[NormalizeEnvironmentKey(key)] = variable{key, value}
		}
	}
	result := make([]string, 0, len(values))
	for _, item := range values {
		result = append(result, item.key+"="+item.value)
	}
	sort.Strings(result)
	return result
}
