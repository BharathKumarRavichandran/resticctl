package profile

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// FieldExplanation describes how a configured field reached the resolved profile.
// It intentionally contains provenance only, never configuration values.
type FieldExplanation struct {
	Path   string `json:"path"`
	Source string `json:"source"`
	Action string `json:"action"`
}

type profileDocument struct {
	name   string
	fields map[string]json.RawMessage
}

// ExplainInheritance returns field-level provenance for a profile's public configuration.
func ExplainInheritance(configDir, name string) ([]FieldExplanation, error) {
	if err := ValidateName(name); err != nil {
		return nil, err
	}
	documents, err := inheritanceDocuments(configDir, name, nil)
	if err != nil {
		return nil, err
	}

	fields := make(map[string]FieldExplanation)
	types := make(map[string]jsonFieldType)
	for index, document := range documents {
		if index > 0 {
			removeExplanationTree(fields, types, "credentials")
			removeExplanationTree(fields, types, "credentials_file")
			removeExplanationTree(fields, types, "private_file")
		}
		applyExplanationObject(fields, types, nil, document.fields, document.name)
	}

	result := make([]FieldExplanation, 0, len(fields))
	for _, explanation := range fields {
		if explanation.Source != name {
			explanation.Action = "inherited"
		}
		result = append(result, explanation)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Path < result[j].Path })
	return result, nil
}

func inheritanceDocuments(configDir, name string, chain []string) ([]profileDocument, error) {
	if err := ValidateName(name); err != nil {
		return nil, fmt.Errorf("invalid parent profile %q: %w", name, err)
	}
	for _, ancestor := range chain {
		if strings.EqualFold(ancestor, name) {
			return nil, fmt.Errorf("profile inheritance cycle: %s", strings.Join(append(chain, name), " -> "))
		}
	}

	path := filepath.Join(configDir, name+".json")
	data, _, err := readStrictJSONFile(path, "profile")
	if err != nil {
		return nil, err
	}
	var configured profileConfig
	if err := decodeStrictJSON(data, &configured); err != nil {
		return nil, fmt.Errorf("cannot load profile %s: %w", path, err)
	}
	var fields map[string]json.RawMessage
	if err := decodeStrictJSON(data, &fields); err != nil {
		return nil, fmt.Errorf("cannot load profile %s: %w", path, err)
	}
	deleteJSONField(fields, "parent")

	document := profileDocument{name: name, fields: fields}
	if configured.Parent == "" {
		return []profileDocument{document}, nil
	}
	parents, err := inheritanceDocuments(configDir, configured.Parent, append(chain, name))
	if err != nil {
		return nil, err
	}
	return append(parents, document), nil
}

type jsonFieldType uint8

const (
	jsonScalar jsonFieldType = iota
	jsonObject
	jsonArray
	jsonNull
)

func applyExplanationObject(fields map[string]FieldExplanation, types map[string]jsonFieldType, prefix []string, object map[string]json.RawMessage, source string) {
	for key, value := range object {
		path := strings.Join(append(prefix, key), ".")
		fieldType, child := classifyJSONField(value)
		previousPath := matchingExplanationPath(types, path)
		previousType, existed := types[previousPath]
		if fieldType == jsonObject && !passwordObjectIsAtomic(prefix, key) && (!existed || previousType == jsonObject) {
			if previousPath != path {
				delete(types, previousPath)
			}
			types[path] = jsonObject
			applyExplanationObject(fields, types, append(prefix, key), child, source)
			continue
		}

		removeExplanationTree(fields, types, previousPath)
		action := "defined"
		if existed {
			switch fieldType {
			case jsonArray:
				action = "replaced"
			case jsonNull:
				action = "cleared"
			default:
				action = "overridden"
			}
		}
		fields[path] = FieldExplanation{Path: path, Source: source, Action: action}
		types[path] = fieldType
	}
}

// Password source objects replace one another in the resolver instead of
// merging command, file, and value fields from different sources.
func passwordObjectIsAtomic(prefix []string, key string) bool {
	if !strings.EqualFold(key, "password") {
		return false
	}
	return len(prefix) == 1 && strings.EqualFold(prefix[0], "credentials") ||
		len(prefix) == 4 && strings.EqualFold(prefix[3], "connection")
}

func matchingExplanationPath(types map[string]jsonFieldType, path string) string {
	for candidate := range types {
		if strings.EqualFold(candidate, path) {
			return candidate
		}
	}
	return path
}

func classifyJSONField(value json.RawMessage) (jsonFieldType, map[string]json.RawMessage) {
	var object map[string]json.RawMessage
	if json.Unmarshal(value, &object) == nil && object != nil {
		return jsonObject, object
	}
	var array []json.RawMessage
	if json.Unmarshal(value, &array) == nil && array != nil {
		return jsonArray, nil
	}
	if string(value) == "null" {
		return jsonNull, nil
	}
	return jsonScalar, nil
}

func removeExplanationTree(fields map[string]FieldExplanation, types map[string]jsonFieldType, path string) {
	for candidate := range types {
		if candidate == path || strings.HasPrefix(candidate, path+".") {
			delete(types, candidate)
			delete(fields, candidate)
		}
	}
}
