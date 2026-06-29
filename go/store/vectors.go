// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package store

import (
	"database/sql"
	"encoding/binary"
	"fmt"
	"math"
)

// VectorRow is one row from the symbol_vecs table.
type VectorRow struct {
	SymbolID int64
	Vec      []float32
	BodyHash string
	ModelID  string
}

// EmbedCandidate is everything the embedder needs about one symbol: its id,
// the text to embed, and the body hash that lets us skip work when nothing
// has changed.
type EmbedCandidate struct {
	SymbolID  int64
	FilePath  string
	Text      string // signature + doc, ready to feed to the embedder
	BodyHash  string
}

// EmbedCandidates returns one row per symbol that has a non-empty FTS entry
// (i.e. anything except call sites). Used by the embedding pipeline to walk
// the codebase. filePath, if non-empty, restricts to one file.
func (s *Store) EmbedCandidates(filePath string) ([]EmbedCandidate, error) {
	var (
		rows *sql.Rows
		err  error
	)
	if filePath != "" {
		rows, err = s.db.Query(`
			SELECT s.id, s.name, s.file_path,
			       COALESCE(fts.signature, ''), COALESCE(fts.doc, ''),
			       s.start_byte, s.end_byte
			  FROM symbols s
			  JOIN symbols_fts fts ON fts.rowid = s.id
			 WHERE s.file_path = ? AND s.kind != 'call'`, filePath)
	} else {
		rows, err = s.db.Query(`
			SELECT s.id, s.name, s.file_path,
			       COALESCE(fts.signature, ''), COALESCE(fts.doc, ''),
			       s.start_byte, s.end_byte
			  FROM symbols s
			  JOIN symbols_fts fts ON fts.rowid = s.id
			 WHERE s.kind != 'call'`)
	}
	if err != nil {
		return nil, fmt.Errorf("EmbedCandidates: %w", err)
	}
	defer rows.Close()
	var out []EmbedCandidate
	for rows.Next() {
		var c EmbedCandidate
		var name, sig, doc string
		var startByte, endByte int
		if err := rows.Scan(&c.SymbolID, &name, &c.FilePath, &sig, &doc, &startByte, &endByte); err != nil {
			return nil, fmt.Errorf("scanning embed candidate: %w", err)
		}
		// Build embedding text: name + signature + doc. Trimmed to a sensible
		// cap so very long signatures don't dominate the batch token budget.
		c.Text = trimText(name+"\n"+sig+"\n"+doc, 1024)
		// Body hash = the byte range itself + name; fast change-detection
		// without needing to load source. Re-index changes both fields when
		// the symbol's bytes shift, so this catches real edits.
		c.BodyHash = fmt.Sprintf("%d-%d-%s", startByte, endByte, name)
		out = append(out, c)
	}
	return out, rows.Err()
}

// trimText returns s truncated to at most maxBytes, breaking at a rune
// boundary so we never emit a partial UTF-8 sequence.
func trimText(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	// Walk back to a rune boundary.
	cut := maxBytes
	for cut > 0 && (s[cut]&0xC0) == 0x80 {
		cut--
	}
	return s[:cut]
}

// UpsertEmbedding writes the vector for a symbol. Replaces any prior row.
// Sawmill always emits float32 vectors, so we store them as 4-byte
// little-endian sequences — cheap, portable, and easy to scan back into a
// slice without parsing.
func (s *Store) UpsertEmbedding(symbolID int64, vec []float32, bodyHash, modelID string) error {
	if len(vec) == 0 {
		return fmt.Errorf("UpsertEmbedding: empty vector for symbol %d", symbolID)
	}
	blob := encodeVec(vec)
	_, err := s.db.Exec(
		`INSERT INTO symbol_vecs (symbol_id, vec, body_hash, model_id, dim)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(symbol_id) DO UPDATE SET
			vec = excluded.vec,
			body_hash = excluded.body_hash,
			model_id = excluded.model_id,
			dim = excluded.dim`,
		symbolID, blob, bodyHash, modelID, len(vec),
	)
	if err != nil {
		return fmt.Errorf("upserting vector for symbol %d: %w", symbolID, err)
	}
	return nil
}

// LookupEmbedding returns the stored body hash for a symbol under modelID,
// or "" if no fresh entry exists.
func (s *Store) LookupEmbedding(symbolID int64, modelID string) (string, error) {
	var hash string
	err := s.db.QueryRow(
		`SELECT body_hash FROM symbol_vecs WHERE symbol_id = ? AND model_id = ?`,
		symbolID, modelID,
	).Scan(&hash)
	if err != nil {
		return "", nil // treat ErrNoRows / etc as "not present"
	}
	return hash, nil
}

// LoadEmbeddings returns every stored vector for modelID, keyed by symbol
// id. Use this once at startup to populate the in-memory cosine index.
func (s *Store) LoadEmbeddings(modelID string) (map[int64][]float32, error) {
	rows, err := s.db.Query(
		`SELECT symbol_id, vec, dim FROM symbol_vecs WHERE model_id = ?`,
		modelID,
	)
	if err != nil {
		return nil, fmt.Errorf("loading embeddings: %w", err)
	}
	defer rows.Close()
	out := make(map[int64][]float32)
	for rows.Next() {
		var id int64
		var blob []byte
		var dim int
		if err := rows.Scan(&id, &blob, &dim); err != nil {
			return nil, fmt.Errorf("scanning embedding: %w", err)
		}
		out[id] = decodeVec(blob, dim)
	}
	return out, rows.Err()
}

// DeleteEmbeddingsForModel removes every vector belonging to a model id.
// Used when the user changes embedding models — old vectors are no longer
// comparable and must be regenerated.
func (s *Store) DeleteEmbeddingsForModel(modelID string) error {
	_, err := s.db.Exec(`DELETE FROM symbol_vecs WHERE model_id = ?`, modelID)
	return err
}

// EmbeddingCount returns the number of stored vectors for modelID.
func (s *Store) EmbeddingCount(modelID string) (int, error) {
	var n int
	err := s.db.QueryRow(
		`SELECT COUNT(*) FROM symbol_vecs WHERE model_id = ?`,
		modelID,
	).Scan(&n)
	if err != nil {
		return 0, err
	}
	return n, nil
}

func encodeVec(v []float32) []byte {
	buf := make([]byte, 4*len(v))
	for i, f := range v {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(f))
	}
	return buf
}

func decodeVec(buf []byte, dim int) []float32 {
	if dim*4 > len(buf) {
		dim = len(buf) / 4
	}
	v := make([]float32, dim)
	for i := range v {
		v[i] = math.Float32frombits(binary.LittleEndian.Uint32(buf[i*4 : i*4+4]))
	}
	return v
}
