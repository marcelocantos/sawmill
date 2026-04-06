// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

// Package exemplar implements teach-by-example: extracting reusable templates
// from exemplar code by replacing concrete values with parameter placeholders,
// including case-variant awareness.
package exemplar

import (
	"sort"
	"strings"
	"unicode"
)

// Templatize replaces all occurrences of parameter values (and their case
// variants) in source with $param_name placeholders. Longer values are
// replaced before shorter ones to avoid partial-match issues.
func Templatize(source string, params map[string]string) string {
	result := source

	// Build a sorted slice of (name, value) pairs, longest value first.
	type kv struct{ name, value string }
	pairs := make([]kv, 0, len(params))
	for name, value := range params {
		pairs = append(pairs, kv{name, value})
	}
	sort.Slice(pairs, func(i, j int) bool {
		return len(pairs[i].value) > len(pairs[j].value)
	})

	for _, p := range pairs {
		if p.value == "" {
			continue
		}

		placeholder := "$" + p.name

		// Replace exact value first.
		result = strings.ReplaceAll(result, p.value, placeholder)

		// Replace case variants.
		variantsValue := caseVariants(p.value)
		variantsName := caseVariants(p.name)

		for i, variant := range variantsValue {
			if variant == p.value {
				continue
			}
			if !strings.Contains(result, variant) {
				continue
			}
			ph := placeholder
			if i < len(variantsName) {
				ph = "$" + variantsName[i]
			}
			result = strings.ReplaceAll(result, variant, ph)
		}
	}

	return result
}

// Substitute replaces $param_name placeholders (and their case-variant
// counterparts) in template with the corresponding parameter values.
func Substitute(template string, params map[string]string) string {
	result := template

	for name, value := range params {
		variantsName := caseVariants(name)
		variantsValue := caseVariants(value)

		// Replace case-variant placeholders.
		for i, nv := range variantsName {
			placeholder := "$" + nv
			replacement := value
			if i < len(variantsValue) {
				replacement = variantsValue[i]
			}
			result = strings.ReplaceAll(result, placeholder, replacement)
		}

		// Replace the base placeholder (in case it survived the loop above,
		// e.g. when variantsName did not include name itself as a variant).
		result = strings.ReplaceAll(result, "$"+name, value)
	}

	return result
}

// caseVariants returns a slice of case variants for s:
//
//	[original, UPPER, lower, Capitalized, uncapitalized]
//
// Duplicates relative to the original are omitted.
func caseVariants(s string) []string {
	variants := []string{s}
	seen := map[string]bool{s: true}

	add := func(v string) {
		if !seen[v] {
			variants = append(variants, v)
			seen[v] = true
		}
	}

	add(strings.ToUpper(s))
	add(strings.ToLower(s))
	add(capitalize(s))
	add(uncapitalize(s))

	return variants
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	runes := []rune(s)
	runes[0] = unicode.ToUpper(runes[0])
	return string(runes)
}

func uncapitalize(s string) string {
	if s == "" {
		return s
	}
	runes := []rune(s)
	runes[0] = unicode.ToLower(runes[0])
	return string(runes)
}
