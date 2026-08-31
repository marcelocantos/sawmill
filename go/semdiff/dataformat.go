// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package semdiff

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// isDataFormat reports whether the file path has a data format extension
// that supports key-level diffing.
func isDataFormat(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".json", ".yaml", ".yml", ".toml", ".xml":
		return true
	}
	return false
}

// dataFormatDiff computes key-level changes between two versions of a data
// file. Returns SymbolChanges where each "symbol" is a dotted key path.
func dataFormatDiff(path string, baseSource, headSource []byte) []SymbolChange {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".json":
		return jsonDiff(baseSource, headSource)
	case ".yaml", ".yml":
		return yamlDiff(baseSource, headSource)
	default:
		return nil
	}
}

func jsonDiff(baseSource, headSource []byte) []SymbolChange {
	var baseVal, headVal any
	if err := json.Unmarshal(baseSource, &baseVal); err != nil {
		return nil
	}
	if err := json.Unmarshal(headSource, &headVal); err != nil {
		return nil
	}
	return diffValues("", baseVal, headVal)
}

func yamlDiff(baseSource, headSource []byte) []SymbolChange {
	var baseVal, headVal any
	if err := yaml.Unmarshal(baseSource, &baseVal); err != nil {
		return nil
	}
	if err := yaml.Unmarshal(headSource, &headVal); err != nil {
		return nil
	}
	return diffValues("", baseVal, headVal)
}

// diffValues recursively compares two parsed data structures, reporting
// key-level adds, removes, and modifications as SymbolChanges.
func diffValues(prefix string, base, head any) []SymbolChange {
	// Normalise YAML int keys to string for comparison.
	base = normalise(base)
	head = normalise(head)

	baseMap, baseIsMap := base.(map[string]any)
	headMap, headIsMap := head.(map[string]any)

	if baseIsMap && headIsMap {
		return diffMaps(prefix, baseMap, headMap)
	}

	baseSlice, baseIsSlice := base.([]any)
	headSlice, headIsSlice := head.([]any)

	if baseIsSlice && headIsSlice {
		return diffSlices(prefix, baseSlice, headSlice)
	}

	// Leaf value comparison.
	if fmt.Sprintf("%v", base) != fmt.Sprintf("%v", head) {
		return []SymbolChange{{
			Op:   OpModify,
			Name: prefix,
			Kind: "key",
		}}
	}
	return nil
}

func diffMaps(prefix string, base, head map[string]any) []SymbolChange {
	var changes []SymbolChange

	allKeys := make(map[string]bool)
	for k := range base {
		allKeys[k] = true
	}
	for k := range head {
		allKeys[k] = true
	}

	sorted := make([]string, 0, len(allKeys))
	for k := range allKeys {
		sorted = append(sorted, k)
	}
	sort.Strings(sorted)

	for _, k := range sorted {
		keyPath := joinKeyPath(prefix, k)
		bv, inBase := base[k]
		hv, inHead := head[k]

		switch {
		case inBase && inHead:
			changes = append(changes, diffValues(keyPath, bv, hv)...)
		case inHead:
			changes = append(changes, SymbolChange{Op: OpAdd, Name: keyPath, Kind: "key"})
		default:
			changes = append(changes, SymbolChange{Op: OpRemove, Name: keyPath, Kind: "key"})
		}
	}
	return changes
}

func diffSlices(prefix string, base, head []any) []SymbolChange {
	var changes []SymbolChange
	maxLen := len(base)
	if len(head) > maxLen {
		maxLen = len(head)
	}
	for i := 0; i < maxLen; i++ {
		keyPath := fmt.Sprintf("%s[%d]", prefix, i)
		switch {
		case i < len(base) && i < len(head):
			changes = append(changes, diffValues(keyPath, base[i], head[i])...)
		case i >= len(base):
			changes = append(changes, SymbolChange{Op: OpAdd, Name: keyPath, Kind: "element"})
		default:
			changes = append(changes, SymbolChange{Op: OpRemove, Name: keyPath, Kind: "element"})
		}
	}
	return changes
}

func joinKeyPath(prefix, key string) string {
	if prefix == "" {
		return key
	}
	return prefix + "." + key
}

// normalise converts YAML-parsed maps (map[string]any is already normal for
// yaml.v3) and ensures consistent types for comparison.
func normalise(v any) any {
	switch val := v.(type) {
	case map[any]any:
		m := make(map[string]any, len(val))
		for k, v := range val {
			m[fmt.Sprintf("%v", k)] = normalise(v)
		}
		return m
	case map[string]any:
		m := make(map[string]any, len(val))
		for k, v := range val {
			m[k] = normalise(v)
		}
		return m
	case []any:
		s := make([]any, len(val))
		for i, v := range val {
			s[i] = normalise(v)
		}
		return s
	default:
		return v
	}
}
