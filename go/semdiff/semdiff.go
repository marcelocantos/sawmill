// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

// Package semdiff provides structural AST-level diffing between git commits.
// It detects moves, renames, parameter changes, and generates API surface
// changelogs by comparing Tree-sitter parse trees stored in the gitindex.
package semdiff

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"github.com/marcelocantos/sawmill/gitindex"
	"github.com/marcelocantos/sawmill/gitrepo"
)

// EditOp classifies a semantic change.
type EditOp string

const (
	OpAdd    EditOp = "add"
	OpRemove EditOp = "remove"
	OpModify EditOp = "modify"
	OpMove   EditOp = "move"
	OpRename EditOp = "rename"
)

// SignatureChange describes what changed in a function's signature.
type SignatureChange struct {
	ParamsAdded   []string `json:"params_added,omitempty"`
	ParamsRemoved []string `json:"params_removed,omitempty"`
	ReturnChanged bool     `json:"return_changed,omitempty"`
}

// SymbolChange describes a change to a single symbol.
type SymbolChange struct {
	Op        EditOp           `json:"op"`
	Name      string           `json:"name"`
	NewName   string           `json:"new_name,omitempty"`
	OldPath   string           `json:"old_path,omitempty"`
	NewPath   string           `json:"new_path,omitempty"`
	Kind      string           `json:"kind"`
	Signature *SignatureChange `json:"signature,omitempty"`
}

// FileDiff describes changes to a single file.
type FileDiff struct {
	Path    string         `json:"path"`
	OldPath string         `json:"old_path,omitempty"`
	Status  string         `json:"status"` // added, removed, modified, moved
	Symbols []SymbolChange `json:"symbols,omitempty"`
}

// DiffResult is the complete semantic diff between two commits.
type DiffResult struct {
	Base  string     `json:"base"`
	Head  string     `json:"head"`
	Files []FileDiff `json:"files"`
}

// declInfo holds a declaration with enough context for structural comparison.
type declInfo struct {
	Symbol      gitindex.SymbolInfo
	FilePath    string
	BlobSHA     string
	Source      []byte // full source of the blob
	BodyHash    string // hash of declaration source minus identifier name
	Fingerprint string // structural fingerprint from AST
}

// Diff computes a structural diff between two commits. It detects file-level
// changes (add/remove/move) and symbol-level changes including renames, moves,
// and signature modifications.
func Diff(store *gitindex.Store, repo *gitrepo.Repo, baseSHA, headSHA string) (*DiffResult, error) {
	baseFiles, err := store.CommitFiles(baseSHA)
	if err != nil {
		return nil, fmt.Errorf("getting base files: %w", err)
	}
	headFiles, err := store.CommitFiles(headSHA)
	if err != nil {
		return nil, fmt.Errorf("getting head files: %w", err)
	}

	baseMap := make(map[string]string, len(baseFiles))
	for _, f := range baseFiles {
		baseMap[f.FilePath] = f.BlobSHA
	}
	headMap := make(map[string]string, len(headFiles))
	for _, f := range headFiles {
		headMap[f.FilePath] = f.BlobSHA
	}

	// Detect file-level moves: same blob SHA at different paths.
	baseBlobToPath := make(map[string]string)
	for _, f := range baseFiles {
		baseBlobToPath[f.BlobSHA] = f.FilePath
	}

	var diffs []FileDiff

	// Track which base paths have been accounted for (by move detection).
	accountedBase := make(map[string]bool)

	// Pass 1: Process head files — detect adds, modifies, and file-level moves.
	for _, hf := range headFiles {
		baseBlobSHA, inBase := baseMap[hf.FilePath]

		switch {
		case inBase && baseBlobSHA == hf.BlobSHA:
			// Unchanged file — skip.
			continue

		case inBase:
			// Same path, different blob — modified.
			symbols, err := diffFileSymbols(store, repo, baseBlobSHA, hf.BlobSHA, hf.FilePath)
			if err != nil {
				return nil, err
			}
			diffs = append(diffs, FileDiff{
				Path:    hf.FilePath,
				Status:  "modified",
				Symbols: symbols,
			})

		default:
			// New path. Check if the blob existed at a different path (file move).
			if oldPath, moved := baseBlobToPath[hf.BlobSHA]; moved && !headPathExists(headMap, oldPath) {
				diffs = append(diffs, FileDiff{
					Path:    hf.FilePath,
					OldPath: oldPath,
					Status:  "moved",
				})
				accountedBase[oldPath] = true
			} else {
				// Truly new file.
				symbols, err := fileSymbolsAsAdded(store, repo, hf.BlobSHA, hf.FilePath)
				if err != nil {
					return nil, err
				}
				diffs = append(diffs, FileDiff{
					Path:    hf.FilePath,
					Status:  "added",
					Symbols: symbols,
				})
			}
		}
	}

	// Pass 2: Process base-only files (removed).
	for _, bf := range baseFiles {
		if _, inHead := headMap[bf.FilePath]; !inHead && !accountedBase[bf.FilePath] {
			symbols, err := fileSymbolsAsRemoved(store, repo, bf.BlobSHA, bf.FilePath)
			if err != nil {
				return nil, err
			}
			diffs = append(diffs, FileDiff{
				Path:    bf.FilePath,
				Status:  "removed",
				Symbols: symbols,
			})
		}
	}

	// Pass 3: Cross-file move/rename detection for symbols.
	diffs = detectCrossFileMovesAndRenames(store, repo, diffs)

	sort.Slice(diffs, func(i, j int) bool { return diffs[i].Path < diffs[j].Path })

	return &DiffResult{
		Base:  baseSHA,
		Head:  headSHA,
		Files: diffs,
	}, nil
}

func headPathExists(headMap map[string]string, path string) bool {
	_, ok := headMap[path]
	return ok
}

// diffFileSymbols compares symbols between two blob versions of the same file.
// For data format files (JSON, YAML), it produces key-level diffs instead.
func diffFileSymbols(store *gitindex.Store, repo *gitrepo.Repo, baseBlobSHA, headBlobSHA, path string) ([]SymbolChange, error) {
	if isDataFormat(path) {
		baseSource, err := repo.ReadBlob(baseBlobSHA)
		if err != nil {
			return nil, err
		}
		headSource, err := repo.ReadBlob(headBlobSHA)
		if err != nil {
			return nil, err
		}
		return dataFormatDiff(path, baseSource, headSource), nil
	}

	baseDecls, err := loadDeclarations(store, repo, baseBlobSHA, path)
	if err != nil {
		return nil, err
	}
	headDecls, err := loadDeclarations(store, repo, headBlobSHA, path)
	if err != nil {
		return nil, err
	}

	baseByName := make(map[string]*declInfo, len(baseDecls))
	for i := range baseDecls {
		baseByName[baseDecls[i].Symbol.Name] = &baseDecls[i]
	}
	headByName := make(map[string]*declInfo, len(headDecls))
	for i := range headDecls {
		headByName[headDecls[i].Symbol.Name] = &headDecls[i]
	}

	var changes []SymbolChange

	// Match by name.
	for name, hd := range headByName {
		bd, inBase := baseByName[name]
		if !inBase {
			continue // handled below as potential rename
		}
		if bd.BodyHash == hd.BodyHash {
			// Unchanged.
			delete(baseByName, name)
			delete(headByName, name)
			continue
		}
		// Modified — check for signature changes.
		change := SymbolChange{
			Op:   OpModify,
			Name: name,
			Kind: bd.Symbol.Kind,
		}
		if bd.Symbol.Kind == "function" {
			sig := compareSignatures(store, bd, hd)
			if sig != nil {
				change.Signature = sig
			}
		}
		changes = append(changes, change)
		delete(baseByName, name)
		delete(headByName, name)
	}

	// Unmatched symbols — try rename detection via structural fingerprint.
	var unmatchedBase []*declInfo
	for _, bd := range baseByName {
		unmatchedBase = append(unmatchedBase, bd)
	}
	var unmatchedHead []*declInfo
	for _, hd := range headByName {
		unmatchedHead = append(unmatchedHead, hd)
	}

	matchedRenames := matchByFingerprint(unmatchedBase, unmatchedHead)
	matchedBaseNames := make(map[string]bool)
	matchedHeadNames := make(map[string]bool)
	for _, m := range matchedRenames {
		changes = append(changes, SymbolChange{
			Op:      OpRename,
			Name:    m.base.Symbol.Name,
			NewName: m.head.Symbol.Name,
			Kind:    m.base.Symbol.Kind,
		})
		matchedBaseNames[m.base.Symbol.Name] = true
		matchedHeadNames[m.head.Symbol.Name] = true
	}

	// Remaining unmatched are pure adds/removes.
	for _, bd := range unmatchedBase {
		if !matchedBaseNames[bd.Symbol.Name] {
			changes = append(changes, SymbolChange{
				Op:   OpRemove,
				Name: bd.Symbol.Name,
				Kind: bd.Symbol.Kind,
			})
		}
	}
	for _, hd := range unmatchedHead {
		if !matchedHeadNames[hd.Symbol.Name] {
			changes = append(changes, SymbolChange{
				Op:   OpAdd,
				Name: hd.Symbol.Name,
				Kind: hd.Symbol.Kind,
			})
		}
	}

	sort.Slice(changes, func(i, j int) bool {
		if changes[i].Op != changes[j].Op {
			return changes[i].Op < changes[j].Op
		}
		return changes[i].Name < changes[j].Name
	})

	return changes, nil
}

// fileSymbolsAsAdded returns all symbols in a blob as "add" changes.
func fileSymbolsAsAdded(store *gitindex.Store, repo *gitrepo.Repo, blobSHA, path string) ([]SymbolChange, error) {
	decls, err := loadDeclarations(store, repo, blobSHA, path)
	if err != nil {
		return nil, err
	}
	changes := make([]SymbolChange, len(decls))
	for i, d := range decls {
		changes[i] = SymbolChange{Op: OpAdd, Name: d.Symbol.Name, Kind: d.Symbol.Kind}
	}
	return changes, nil
}

// fileSymbolsAsRemoved returns all symbols in a blob as "remove" changes.
func fileSymbolsAsRemoved(store *gitindex.Store, repo *gitrepo.Repo, blobSHA, path string) ([]SymbolChange, error) {
	decls, err := loadDeclarations(store, repo, blobSHA, path)
	if err != nil {
		return nil, err
	}
	changes := make([]SymbolChange, len(decls))
	for i, d := range decls {
		changes[i] = SymbolChange{Op: OpRemove, Name: d.Symbol.Name, Kind: d.Symbol.Kind}
	}
	return changes, nil
}

// loadDeclarations loads all declarations from a blob with their body hashes
// and structural fingerprints.
func loadDeclarations(store *gitindex.Store, repo *gitrepo.Repo, blobSHA, path string) ([]declInfo, error) {
	indexed, err := store.IsIndexed(blobSHA)
	if err != nil {
		return nil, err
	}
	if !indexed {
		return nil, nil
	}

	source, err := repo.ReadBlob(blobSHA)
	if err != nil {
		return nil, err
	}

	symbols, err := store.SymbolNames(blobSHA, source)
	if err != nil {
		return nil, err
	}

	// Load all nodes for fingerprinting.
	allNodes, err := store.AllNodes(blobSHA)
	if err != nil {
		return nil, err
	}
	nodeChildren := buildChildrenMap(allNodes)

	decls := make([]declInfo, 0, len(symbols))
	for _, sym := range symbols {
		bodyHash := computeBodyHash(source, sym)
		fp := computeFingerprint(allNodes, nodeChildren, sym.NodeID)

		decls = append(decls, declInfo{
			Symbol:      sym,
			FilePath:    path,
			BlobSHA:     blobSHA,
			Source:      source,
			BodyHash:    bodyHash,
			Fingerprint: fp,
		})
	}
	return decls, nil
}

// computeBodyHash hashes the declaration source minus the identifier name.
// This detects when only the name changed but the body is identical (rename).
func computeBodyHash(source []byte, sym gitindex.SymbolInfo) string {
	if sym.DeclStartByte < 0 || sym.DeclEndByte > len(source) {
		return ""
	}
	declSource := source[sym.DeclStartByte:sym.DeclEndByte]
	// Remove the identifier from the declaration source.
	nameStart := sym.StartByte - sym.DeclStartByte
	nameEnd := sym.EndByte - sym.DeclStartByte
	if nameStart >= 0 && nameEnd <= len(declSource) && nameStart < nameEnd {
		body := make([]byte, 0, len(declSource)-(nameEnd-nameStart))
		body = append(body, declSource[:nameStart]...)
		body = append(body, declSource[nameEnd:]...)
		h := sha256.Sum256(body)
		return hex.EncodeToString(h[:8])
	}
	h := sha256.Sum256(declSource)
	return hex.EncodeToString(h[:8])
}

// buildChildrenMap groups nodes by parent ID.
func buildChildrenMap(nodes []NodeRecord) map[int64][]NodeRecord {
	m := make(map[int64][]NodeRecord)
	for _, n := range nodes {
		if n.ParentID != nil {
			m[*n.ParentID] = append(m[*n.ParentID], n)
		}
	}
	return m
}

// NodeRecord is a local alias to avoid circular imports — we accept
// gitindex.NodeRecord directly.
type NodeRecord = gitindex.NodeRecord

// computeFingerprint builds a structural fingerprint from a declaration's
// subtree. It hashes the tree of node types (ignoring source text) to detect
// structurally equivalent declarations with different names.
func computeFingerprint(_ []NodeRecord, children map[int64][]NodeRecord, rootID int64) string {
	var buf strings.Builder
	fingerprintWalk(&buf, children, rootID, 0)
	h := sha256.Sum256([]byte(buf.String()))
	return hex.EncodeToString(h[:8])
}

func fingerprintWalk(buf *strings.Builder, children map[int64][]NodeRecord, nodeID int64, depth int) {
	// Guard against pathological depth.
	if depth > 100 {
		return
	}
	kids := children[nodeID]
	for _, kid := range kids {
		// Skip identifier nodes (they carry the name which we want to ignore).
		switch kid.NodeType {
		case "identifier", "type_identifier", "field_identifier":
			continue
		}
		fmt.Fprintf(buf, "(%s", kid.NodeType)
		if kid.FieldName != "" {
			fmt.Fprintf(buf, ":%s", kid.FieldName)
		}
		fingerprintWalk(buf, children, kid.ID, depth+1)
		buf.WriteByte(')')
	}
}

type matchPair struct {
	base *declInfo
	head *declInfo
}

// matchByFingerprint pairs unmatched base and head declarations that share the
// same kind and structural fingerprint, indicating a rename.
func matchByFingerprint(base, head []*declInfo) []matchPair {
	type key struct {
		kind        string
		fingerprint string
	}

	// Build a map from (kind, fingerprint) → base declarations.
	baseByFP := make(map[key][]*declInfo)
	for _, b := range base {
		if b.Fingerprint == "" {
			continue
		}
		k := key{b.Symbol.Kind, b.Fingerprint}
		baseByFP[k] = append(baseByFP[k], b)
	}

	var matches []matchPair
	for _, h := range head {
		if h.Fingerprint == "" {
			continue
		}
		k := key{h.Symbol.Kind, h.Fingerprint}
		candidates := baseByFP[k]
		if len(candidates) == 0 {
			continue
		}
		// Also check body hash for extra confidence.
		var bestIdx int
		found := false
		for i, c := range candidates {
			if c.BodyHash == h.BodyHash {
				bestIdx = i
				found = true
				break
			}
		}
		if !found {
			// Use first candidate even without body match — the structural
			// fingerprint alone is a strong signal.
			bestIdx = 0
		}
		matches = append(matches, matchPair{base: candidates[bestIdx], head: h})
		// Remove matched candidate.
		candidates[bestIdx] = candidates[len(candidates)-1]
		baseByFP[k] = candidates[:len(candidates)-1]
	}
	return matches
}

// detectCrossFileMovesAndRenames looks at symbols that were removed from one
// file and added to another with the same structural fingerprint.
func detectCrossFileMovesAndRenames(_ *gitindex.Store, _ *gitrepo.Repo, diffs []FileDiff) []FileDiff {
	// Collect all removed and added symbols across files.
	type symbolRef struct {
		diffIdx   int
		changeIdx int
		decl      SymbolChange
		path      string
	}

	var removed, added []symbolRef
	for i, d := range diffs {
		for j, c := range d.Symbols {
			switch c.Op {
			case OpRemove:
				removed = append(removed, symbolRef{i, j, c, d.Path})
			case OpAdd:
				added = append(added, symbolRef{i, j, c, d.Path})
			}
		}
	}

	if len(removed) == 0 || len(added) == 0 {
		return diffs
	}

	// Match removed→added by (kind, name) across different files → move.
	addedByKey := make(map[string][]int) // "kind:name" → indices into added
	for i, a := range added {
		key := a.decl.Kind + ":" + a.decl.Name
		addedByKey[key] = append(addedByKey[key], i)
	}

	movedRemoved := make(map[int]bool)
	movedAdded := make(map[int]bool)

	for ri, r := range removed {
		key := r.decl.Kind + ":" + r.decl.Name
		candidates := addedByKey[key]
		for _, ai := range candidates {
			a := added[ai]
			if a.path == r.path || movedAdded[ai] {
				continue
			}
			// Same name, different file → move.
			diffs[a.diffIdx].Symbols[a.changeIdx] = SymbolChange{
				Op:      OpMove,
				Name:    r.decl.Name,
				Kind:    r.decl.Kind,
				OldPath: r.path,
				NewPath: a.path,
			}
			movedRemoved[ri] = true
			movedAdded[ai] = true
			break
		}
	}

	// Remove the corresponding "remove" entries for moved symbols.
	for ri := range movedRemoved {
		r := removed[ri]
		diffs[r.diffIdx].Symbols[r.changeIdx].Op = "" // mark for removal
	}

	// Clean up empty-op symbols.
	for i := range diffs {
		var cleaned []SymbolChange
		for _, c := range diffs[i].Symbols {
			if c.Op != "" {
				cleaned = append(cleaned, c)
			}
		}
		diffs[i].Symbols = cleaned
	}

	return diffs
}

// compareSignatures compares the parameter lists and return types of two
// function declarations.
func compareSignatures(store *gitindex.Store, base, head *declInfo) *SignatureChange {
	baseParams := extractParams(store, base)
	headParams := extractParams(store, head)
	baseReturn := extractReturn(base)
	headReturn := extractReturn(head)

	baseParamSet := make(map[string]bool, len(baseParams))
	for _, p := range baseParams {
		baseParamSet[p] = true
	}
	headParamSet := make(map[string]bool, len(headParams))
	for _, p := range headParams {
		headParamSet[p] = true
	}

	var added, removed []string
	for _, p := range headParams {
		if !baseParamSet[p] {
			added = append(added, p)
		}
	}
	for _, p := range baseParams {
		if !headParamSet[p] {
			removed = append(removed, p)
		}
	}

	returnChanged := baseReturn != headReturn

	if len(added) == 0 && len(removed) == 0 && !returnChanged {
		return nil
	}

	return &SignatureChange{
		ParamsAdded:   added,
		ParamsRemoved: removed,
		ReturnChanged: returnChanged,
	}
}

// extractParams extracts parameter text from a function declaration by querying
// the parameter_list children of the declaration node.
func extractParams(store *gitindex.Store, d *declInfo) []string {
	children, err := store.QueryChildren(d.Symbol.NodeID)
	if err != nil {
		return nil
	}

	for _, child := range children {
		if child.FieldName == "parameters" || child.NodeType == "parameter_list" {
			return extractParamNames(store, d.Source, child.ID)
		}
	}
	return nil
}

// extractParamNames extracts individual parameter declarations from a
// parameter_list node.
func extractParamNames(store *gitindex.Store, source []byte, paramListID int64) []string {
	children, err := store.QueryChildren(paramListID)
	if err != nil {
		return nil
	}

	var params []string
	for _, child := range children {
		// Parameter declaration nodes contain the param text.
		if child.StartByte >= 0 && child.EndByte <= len(source) {
			text := strings.TrimSpace(string(source[child.StartByte:child.EndByte]))
			if text != "" && text != "(" && text != ")" && text != "," {
				params = append(params, text)
			}
		}
	}
	return params
}

// extractReturn extracts the return type text from a function declaration.
func extractReturn(d *declInfo) string {
	source := d.Source
	sym := d.Symbol
	if sym.DeclStartByte < 0 || sym.DeclEndByte > len(source) {
		return ""
	}

	// For Go functions, look for "result" field in declaration children.
	// We use a heuristic: the text between the closing ')' of params and the
	// opening '{' of the body is the return type.
	declText := string(source[sym.DeclStartByte:sym.DeclEndByte])

	// Find the last ')' before '{' — that's the end of parameters.
	braceIdx := strings.Index(declText, "{")
	if braceIdx < 0 {
		return ""
	}
	parenIdx := strings.LastIndex(declText[:braceIdx], ")")
	if parenIdx < 0 {
		return ""
	}

	returnText := strings.TrimSpace(declText[parenIdx+1 : braceIdx])
	return returnText
}

// Changelog formats a DiffResult as a markdown API surface changelog.
func Changelog(result *DiffResult) string {
	var buf strings.Builder

	fmt.Fprintf(&buf, "# API Changelog\n\n")
	fmt.Fprintf(&buf, "Comparing %s → %s\n\n", shortSHA(result.Base), shortSHA(result.Head))

	var addedSyms, removedSyms, modifiedSyms, movedSyms, renamedSyms []string

	for _, f := range result.Files {
		for _, s := range f.Symbols {
			loc := f.Path
			switch s.Op {
			case OpAdd:
				addedSyms = append(addedSyms, fmt.Sprintf("- `%s` %s (%s)", s.Name, s.Kind, loc))
			case OpRemove:
				removedSyms = append(removedSyms, fmt.Sprintf("- `%s` %s (%s)", s.Name, s.Kind, loc))
			case OpModify:
				detail := ""
				if s.Signature != nil {
					var parts []string
					if len(s.Signature.ParamsAdded) > 0 {
						parts = append(parts, fmt.Sprintf("params added: %s", strings.Join(s.Signature.ParamsAdded, ", ")))
					}
					if len(s.Signature.ParamsRemoved) > 0 {
						parts = append(parts, fmt.Sprintf("params removed: %s", strings.Join(s.Signature.ParamsRemoved, ", ")))
					}
					if s.Signature.ReturnChanged {
						parts = append(parts, "return type changed")
					}
					if len(parts) > 0 {
						detail = " — " + strings.Join(parts, "; ")
					}
				}
				modifiedSyms = append(modifiedSyms, fmt.Sprintf("- `%s` %s (%s)%s", s.Name, s.Kind, loc, detail))
			case OpMove:
				movedSyms = append(movedSyms, fmt.Sprintf("- `%s` %s: %s → %s", s.Name, s.Kind, s.OldPath, s.NewPath))
			case OpRename:
				renamedSyms = append(renamedSyms, fmt.Sprintf("- `%s` → `%s` %s (%s)", s.Name, s.NewName, s.Kind, loc))
			}
		}
	}

	if len(addedSyms) > 0 {
		fmt.Fprintf(&buf, "## Added\n\n%s\n\n", strings.Join(addedSyms, "\n"))
	}
	if len(removedSyms) > 0 {
		fmt.Fprintf(&buf, "## Removed\n\n%s\n\n", strings.Join(removedSyms, "\n"))
	}
	if len(modifiedSyms) > 0 {
		fmt.Fprintf(&buf, "## Modified\n\n%s\n\n", strings.Join(modifiedSyms, "\n"))
	}
	if len(movedSyms) > 0 {
		fmt.Fprintf(&buf, "## Moved\n\n%s\n\n", strings.Join(movedSyms, "\n"))
	}
	if len(renamedSyms) > 0 {
		fmt.Fprintf(&buf, "## Renamed\n\n%s\n\n", strings.Join(renamedSyms, "\n"))
	}

	if len(addedSyms)+len(removedSyms)+len(modifiedSyms)+len(movedSyms)+len(renamedSyms) == 0 {
		buf.WriteString("No API surface changes detected.\n")
	}

	return buf.String()
}

func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}
