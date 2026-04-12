// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package gitindex

import (
	"database/sql"
	"fmt"
)

// NodeRecord is a row from the nodes table with type and field name strings
// resolved from their interning tables.
type NodeRecord struct {
	ID        int64
	BlobSHA   string
	ParentID  *int64 // nil for root
	NodeType  string
	FieldName string // empty if the node has no field name from its parent
	StartByte int
	EndByte   int
}

// QueryNodes returns all nodes of the given type within a blob.
func (s *Store) QueryNodes(blobSHA string, nodeType string) ([]NodeRecord, error) {
	rows, err := s.db.Query(`
		SELECT n.id, n.blob_sha, n.parent_id, nt.name, COALESCE(fn.name, ''), n.start_byte, n.end_byte
		FROM nodes n
		JOIN node_types nt ON nt.id = n.node_type_id
		LEFT JOIN field_names fn ON fn.id = n.field_name_id
		WHERE n.blob_sha = ? AND nt.name = ?
		ORDER BY n.start_byte
	`, blobSHA, nodeType)
	if err != nil {
		return nil, fmt.Errorf("querying nodes by type %q in blob %s: %w", nodeType, blobSHA, err)
	}
	defer rows.Close()
	return scanNodeRows(rows)
}

// QueryChildren returns all direct children of the given node.
func (s *Store) QueryChildren(nodeID int64) ([]NodeRecord, error) {
	rows, err := s.db.Query(`
		SELECT n.id, n.blob_sha, n.parent_id, nt.name, COALESCE(fn.name, ''), n.start_byte, n.end_byte
		FROM nodes n
		JOIN node_types nt ON nt.id = n.node_type_id
		LEFT JOIN field_names fn ON fn.id = n.field_name_id
		WHERE n.parent_id = ?
		ORDER BY n.start_byte
	`, nodeID)
	if err != nil {
		return nil, fmt.Errorf("querying children of node %d: %w", nodeID, err)
	}
	defer rows.Close()
	return scanNodeRows(rows)
}

// QueryAncestors walks up the parent chain from nodeID and returns each
// ancestor in order from nearest (parent) to farthest (root).
func (s *Store) QueryAncestors(nodeID int64) ([]NodeRecord, error) {
	// Walk the parent chain iteratively to avoid unbounded recursion.
	var ancestors []NodeRecord
	current := nodeID
	for {
		var parentID sql.NullInt64
		err := s.db.QueryRow("SELECT parent_id FROM nodes WHERE id = ?", current).Scan(&parentID)
		if err == sql.ErrNoRows {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("looking up parent of node %d: %w", current, err)
		}
		if !parentID.Valid {
			break
		}
		current = parentID.Int64

		rec, err := s.nodeByID(current)
		if err != nil {
			return nil, err
		}
		ancestors = append(ancestors, *rec)
	}
	return ancestors, nil
}

// NodesByTypeAndField returns nodes of the given type that are also attached
// to their parent via the given field name.
func (s *Store) NodesByTypeAndField(blobSHA, nodeType, fieldName string) ([]NodeRecord, error) {
	rows, err := s.db.Query(`
		SELECT n.id, n.blob_sha, n.parent_id, nt.name, fn.name, n.start_byte, n.end_byte
		FROM nodes n
		JOIN node_types nt ON nt.id = n.node_type_id
		JOIN field_names fn ON fn.id = n.field_name_id
		WHERE n.blob_sha = ? AND nt.name = ? AND fn.name = ?
		ORDER BY n.start_byte
	`, blobSHA, nodeType, fieldName)
	if err != nil {
		return nil, fmt.Errorf("querying nodes by type %q and field %q in blob %s: %w",
			nodeType, fieldName, blobSHA, err)
	}
	defer rows.Close()
	return scanNodeRows(rows)
}

// nodeByID fetches a single NodeRecord by its primary key.
func (s *Store) nodeByID(id int64) (*NodeRecord, error) {
	var rec NodeRecord
	var parentID sql.NullInt64
	err := s.db.QueryRow(`
		SELECT n.id, n.blob_sha, n.parent_id, nt.name, COALESCE(fn.name, ''), n.start_byte, n.end_byte
		FROM nodes n
		JOIN node_types nt ON nt.id = n.node_type_id
		LEFT JOIN field_names fn ON fn.id = n.field_name_id
		WHERE n.id = ?
	`, id).Scan(&rec.ID, &rec.BlobSHA, &parentID, &rec.NodeType, &rec.FieldName, &rec.StartByte, &rec.EndByte)
	if err != nil {
		return nil, fmt.Errorf("fetching node %d: %w", id, err)
	}
	if parentID.Valid {
		rec.ParentID = &parentID.Int64
	}
	return &rec, nil
}

func scanNodeRows(rows *sql.Rows) ([]NodeRecord, error) {
	var records []NodeRecord
	for rows.Next() {
		var rec NodeRecord
		var parentID sql.NullInt64
		if err := rows.Scan(
			&rec.ID, &rec.BlobSHA, &parentID,
			&rec.NodeType, &rec.FieldName,
			&rec.StartByte, &rec.EndByte,
		); err != nil {
			return nil, fmt.Errorf("scanning node row: %w", err)
		}
		if parentID.Valid {
			rec.ParentID = &parentID.Int64
		}
		records = append(records, rec)
	}
	return records, rows.Err()
}
