// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"fmt"
	"sort"
	"strings"
)

// ConceptMatch is a single symbol surfaced by FindByConcept, with the
// matched-evidence breakdown that produced its rank.
type ConceptMatch struct {
	Symbol          SymbolRecord
	MatchedAliases  []string // aliases that hit the evidence
	NameHitAliases  []string // subset of MatchedAliases that also hit the name tokens
	Score           int      // sort key (descending)
}

// FindByConcept returns symbols whose evidence (or name) matches one or more
// of the given aliases, ranked by how many distinct aliases hit. Name-token
// hits are weighted more heavily than body-evidence hits.
//
// Aliases should be lowercased; callers may pass the original query as part
// of the alias set so it acts as its own anchor when no concept dictionary
// entry expands it.
//
// scopes restricts results to files whose scope is in the given set
// ("owned", "library", "ignored"); an empty set means no scope filter.
// limit caps the result count after ranking; 0 means no limit.
func (s *Store) FindByConcept(aliases []string, scopes []string, limit int) ([]ConceptMatch, error) {
	aliases = normalizeAliases(aliases)
	if len(aliases) == 0 {
		return nil, nil
	}

	likeArgs := make([]any, 0, len(aliases))
	likeClauses := make([]string, 0, len(aliases))
	for _, a := range aliases {
		likeArgs = append(likeArgs, "% "+a+" %")
		likeClauses = append(likeClauses, "s.evidence LIKE ?")
	}

	scopeClause := ""
	args := append([]any{}, likeArgs...)
	if len(scopes) > 0 {
		placeholders := strings.TrimRight(strings.Repeat("?,", len(scopes)), ",")
		scopeClause = fmt.Sprintf(" AND f.scope IN (%s)", placeholders)
		for _, sc := range scopes {
			args = append(args, sc)
		}
	}

	q := fmt.Sprintf(`
		SELECT s.name, s.kind, s.file_path,
		       s.start_line, s.start_col, s.end_line, s.end_col,
		       s.start_byte, s.end_byte, s.evidence
		  FROM symbols s
		  JOIN files   f ON f.path = s.file_path
		 WHERE (%s)%s`,
		strings.Join(likeClauses, " OR "), scopeClause)

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("finding by concept: %w", err)
	}
	defer rows.Close()

	var matches []ConceptMatch
	for rows.Next() {
		var r SymbolRecord
		if err := rows.Scan(
			&r.Name, &r.Kind, &r.FilePath,
			&r.StartLine, &r.StartCol, &r.EndLine, &r.EndCol,
			&r.StartByte, &r.EndByte, &r.Evidence,
		); err != nil {
			return nil, fmt.Errorf("scanning concept match: %w", err)
		}
		m := scoreMatch(r, aliases)
		if m.Score > 0 {
			matches = append(matches, m)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].Score != matches[j].Score {
			return matches[i].Score > matches[j].Score
		}
		if matches[i].Symbol.FilePath != matches[j].Symbol.FilePath {
			return matches[i].Symbol.FilePath < matches[j].Symbol.FilePath
		}
		return matches[i].Symbol.StartLine < matches[j].Symbol.StartLine
	})

	if limit > 0 && len(matches) > limit {
		matches = matches[:limit]
	}
	return matches, nil
}

// NameHitBonus is the per-alias score added when the alias matches a token
// of the symbol name itself, rather than only body evidence. The factor is
// chosen so that a single name-hit alias outranks two body-only hits.
const NameHitBonus = 3

// scoreMatch computes the score and evidence breakdown for a single symbol
// row against the alias set.
func scoreMatch(r SymbolRecord, aliases []string) ConceptMatch {
	out := ConceptMatch{Symbol: r}
	// Lowercased, space-padded name-token bag for symmetric matching.
	nameEvidence := " " + tokenize(r.Name) + " "
	for _, a := range aliases {
		needle := " " + a + " "
		bodyHit := strings.Contains(r.Evidence, needle)
		nameHit := strings.Contains(nameEvidence, needle)
		if !bodyHit && !nameHit {
			continue
		}
		out.MatchedAliases = append(out.MatchedAliases, a)
		if nameHit {
			out.NameHitAliases = append(out.NameHitAliases, a)
			out.Score += NameHitBonus
		} else {
			out.Score++
		}
	}
	return out
}

// tokenize lowercases an identifier and splits CamelCase + snake_case into
// space-separated tokens. Used to derive a name-only evidence string for
// scoring purposes. The returned string contains tokens separated by single
// spaces, no leading or trailing space.
func tokenize(name string) string {
	var parts []string
	var buf []byte
	flush := func() {
		if len(buf) > 0 {
			parts = append(parts, strings.ToLower(string(buf)))
			buf = buf[:0]
		}
	}
	prevLower := false
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c >= 'A' && c <= 'Z':
			if prevLower {
				flush()
			}
			buf = append(buf, c)
			prevLower = false
		case (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9'):
			buf = append(buf, c)
			prevLower = c >= 'a' && c <= 'z'
		default:
			flush()
			prevLower = false
		}
	}
	flush()
	// Also include the whole lowercased identifier as a token so an alias
	// equal to the full name still hits via the name path.
	if low := strings.ToLower(name); low != "" {
		parts = append(parts, low)
	}
	return strings.Join(parts, " ")
}
