// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Concept is a saved concept-search dictionary entry: a concept name plus a
// list of aliases that should expand into search terms. Concepts are matched
// against symbol evidence (identifier tokens, doc/literal/comment tokens,
// imported types, path tokens) to surface code related to a domain idea
// expressed in many lexical forms (e.g. swipe / gesture / fling / pan).
type Concept struct {
	Name        string
	Description string
	Aliases     []string
}

// SaveConcept saves or updates a concept entry.
func (s *Store) SaveConcept(name, description string, aliases []string) error {
	aliases = normalizeAliases(aliases)
	aliasesJSON, err := json.Marshal(aliases)
	if err != nil {
		return fmt.Errorf("serialising concept aliases: %w", err)
	}
	_, err = s.db.Exec(
		`INSERT INTO concepts (name, description, aliases_json)
		 VALUES (?, ?, ?)
		 ON CONFLICT(name) DO UPDATE SET
			description = excluded.description,
			aliases_json = excluded.aliases_json`,
		name, description, string(aliasesJSON),
	)
	if err != nil {
		return fmt.Errorf("saving concept %q: %w", name, err)
	}
	return nil
}

// LoadConcept loads a concept by name. Returns nil if not found.
func (s *Store) LoadConcept(name string) (*Concept, error) {
	var description, aliasesJSON string
	err := s.db.QueryRow(
		"SELECT description, aliases_json FROM concepts WHERE name = ?",
		name,
	).Scan(&description, &aliasesJSON)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("loading concept %q: %w", name, err)
	}
	var aliases []string
	if err := json.Unmarshal([]byte(aliasesJSON), &aliases); err != nil {
		return nil, fmt.Errorf("decoding concept %q aliases: %w", name, err)
	}
	return &Concept{Name: name, Description: description, Aliases: aliases}, nil
}

// ListConcepts returns all concept entries, ordered by name.
func (s *Store) ListConcepts() ([]Concept, error) {
	rows, err := s.db.Query("SELECT name, description, aliases_json FROM concepts ORDER BY name")
	if err != nil {
		return nil, fmt.Errorf("listing concepts: %w", err)
	}
	defer rows.Close()
	var out []Concept
	for rows.Next() {
		var c Concept
		var aliasesJSON string
		if err := rows.Scan(&c.Name, &c.Description, &aliasesJSON); err != nil {
			return nil, fmt.Errorf("reading concept row: %w", err)
		}
		if err := json.Unmarshal([]byte(aliasesJSON), &c.Aliases); err != nil {
			return nil, fmt.Errorf("decoding concept %q aliases: %w", c.Name, err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// DeleteConcept deletes a concept by name. Returns true if a row was removed.
func (s *Store) DeleteConcept(name string) (bool, error) {
	res, err := s.db.Exec("DELETE FROM concepts WHERE name = ?", name)
	if err != nil {
		return false, fmt.Errorf("deleting concept %q: %w", name, err)
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// normalizeAliases lowercases, trims, and deduplicates an alias list. Empty
// entries are dropped. Order is sorted for stable storage.
func normalizeAliases(aliases []string) []string {
	seen := make(map[string]struct{}, len(aliases))
	out := make([]string, 0, len(aliases))
	for _, a := range aliases {
		a = strings.ToLower(strings.TrimSpace(a))
		if a == "" {
			continue
		}
		if _, dup := seen[a]; dup {
			continue
		}
		seen[a] = struct{}{}
		out = append(out, a)
	}
	sort.Strings(out)
	return out
}
