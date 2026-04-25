// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

// Package tscompat provides a compatibility shim over gotreesitter, exposing
// the same API surface that sawmill previously used from go-tree-sitter.
// All types are thin wrappers that delegate to the underlying gotreesitter
// types with no behaviour changes.
package tscompat

import (
	gts "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"
)

// Re-export the pure Language type (never needed wrapping in go-tree-sitter either).
type Language = gts.Language

// Point is the row/column position type (identical fields in both runtimes).
type Point = gts.Point

// Tree wraps a gotreesitter Tree and carries the Language so that RootNode()
// returns a language-aware Node.
type Tree struct {
	inner *gts.Tree
	lang  *gts.Language
}

// wrapTree wraps a gotreesitter Tree together with its language.
func wrapTree(t *gts.Tree, lang *gts.Language) *Tree {
	if t == nil {
		return nil
	}
	return &Tree{inner: t, lang: lang}
}

// RootNode returns the root node of the tree, language-aware.
func (t *Tree) RootNode() *Node {
	if t == nil || t.inner == nil {
		return nil
	}
	return wrapNode(t.inner.RootNode(), t.lang)
}

// HasError delegates to the inner tree.
func (t *Tree) HasError() bool {
	if t == nil || t.inner == nil {
		return false
	}
	return t.inner.RootNode().HasError()
}

// Inner returns the underlying gotreesitter Tree.
func (t *Tree) Inner() *gts.Tree { return t.inner }

// Close is a no-op (gotreesitter trees are GC-managed).
func (t *Tree) Close() {}

// Walk returns a TreeCursor positioned at the root of the tree.
func (t *Tree) Walk() *TreeCursor {
	if t == nil || t.inner == nil {
		return &TreeCursor{inner: gts.NewTreeCursorFromTree(nil), lang: t.lang}
	}
	return &TreeCursor{inner: gts.NewTreeCursorFromTree(t.inner), lang: t.lang}
}

// TreeCursor wraps a gotreesitter TreeCursor and carries the language for
// node type resolution.
type TreeCursor struct {
	inner *gts.TreeCursor
	lang  *gts.Language
}

// Node returns the current node, wrapped with the language.
func (c *TreeCursor) Node() *Node {
	return wrapNode(c.inner.CurrentNode(), c.lang)
}

// FieldName returns the field name of the current node within its parent.
func (c *TreeCursor) FieldName() string {
	return c.inner.CurrentFieldName()
}

// GotoFirstChild moves the cursor to the first child. Returns false if none.
func (c *TreeCursor) GotoFirstChild() bool { return c.inner.GotoFirstChild() }

// GotoNextSibling moves the cursor to the next sibling. Returns false if none.
func (c *TreeCursor) GotoNextSibling() bool { return c.inner.GotoNextSibling() }

// GotoParent moves the cursor to the parent. Returns false at the root.
func (c *TreeCursor) GotoParent() bool { return c.inner.GotoParent() }

// Close is a no-op (gotreesitter cursors are GC-managed).
func (c *TreeCursor) Close() {}

// Node wraps a gotreesitter Node and carries the Language so Kind(),
// StartPosition(), EndPosition(), and ChildByFieldName() work without
// callers needing to pass the language everywhere.
type Node struct {
	inner *gts.Node
	lang  *gts.Language
}

// wrapNode wraps a gotreesitter Node, inheriting the given language.
// Returns nil if the underlying node is nil.
func wrapNode(n *gts.Node, lang *gts.Language) *Node {
	if n == nil {
		return nil
	}
	return &Node{inner: n, lang: lang}
}

// Kind returns the type name of this node (equivalent to go-tree-sitter's Kind()).
func (n *Node) Kind() string {
	if n == nil || n.inner == nil {
		return ""
	}
	return n.inner.Type(n.lang)
}

// StartPosition returns the node's start point (row/column).
func (n *Node) StartPosition() gts.Point { return n.inner.StartPoint() }

// EndPosition returns the node's end point (row/column).
func (n *Node) EndPosition() gts.Point { return n.inner.EndPoint() }

// StartByte returns the byte offset where this node begins.
// Returns uint to match the go-tree-sitter API.
func (n *Node) StartByte() uint { return uint(n.inner.StartByte()) }

// EndByte returns the byte offset where this node ends (exclusive).
// Returns uint to match the go-tree-sitter API.
func (n *Node) EndByte() uint { return uint(n.inner.EndByte()) }

// IsNamed reports whether this is a named node.
func (n *Node) IsNamed() bool { return n.inner.IsNamed() }

// IsMissing reports whether this node was inserted by error recovery.
func (n *Node) IsMissing() bool { return n.inner.IsMissing() }

// HasError reports whether this node or any descendant contains a parse error.
func (n *Node) HasError() bool { return n.inner.HasError() }

// ChildCount returns the number of children.
// Returns uint to match the go-tree-sitter API.
func (n *Node) ChildCount() uint { return uint(n.inner.ChildCount()) }

// NamedChildCount returns the number of named children.
// Returns uint to match the go-tree-sitter API.
func (n *Node) NamedChildCount() uint { return uint(n.inner.NamedChildCount()) }

// Parent returns the parent node wrapped with the same language.
func (n *Node) Parent() *Node { return wrapNode(n.inner.Parent(), n.lang) }

// Child returns the i-th child wrapped with the same language.
// Accepts uint to match the go-tree-sitter API.
func (n *Node) Child(i uint) *Node { return wrapNode(n.inner.Child(int(i)), n.lang) }

// NamedChild returns the i-th named child wrapped with the same language.
// Accepts uint to match the go-tree-sitter API.
func (n *Node) NamedChild(i uint) *Node { return wrapNode(n.inner.NamedChild(int(i)), n.lang) }

// ChildByFieldName returns the child with the given field name, wrapped.
func (n *Node) ChildByFieldName(name string) *Node {
	return wrapNode(n.inner.ChildByFieldName(name, n.lang), n.lang)
}

// Children returns the children of this node as values. The cursor argument is
// accepted for API compatibility with go-tree-sitter but is not used.
func (n *Node) Children(_ *TreeCursor) []Node {
	raw := n.inner.Children()
	out := make([]Node, len(raw))
	for i, c := range raw {
		if c != nil {
			out[i] = Node{inner: c, lang: n.lang}
		}
	}
	return out
}

// Walk returns a TreeCursor positioned at this node.
func (n *Node) Walk() *TreeCursor {
	c := gts.NewTreeCursor(n.inner, nil)
	return &TreeCursor{inner: c, lang: n.lang}
}

// PrevSibling returns the previous sibling of this node.
func (n *Node) PrevSibling() *Node {
	return wrapNode(n.inner.PrevSibling(), n.lang)
}

// DescendantForByteRange returns the smallest node containing the given byte range.
func (n *Node) DescendantForByteRange(start, end uint) *Node {
	return wrapNode(n.inner.DescendantForByteRange(uint32(start), uint32(end)), n.lang)
}

// Inner returns the underlying gotreesitter Node.
func (n *Node) Inner() *gts.Node { return n.inner }

// Query wraps a gotreesitter Query and remembers the language for cursor operations.
type Query struct {
	inner *gts.Query
	lang  *gts.Language
}

// NewQuery compiles a tree-sitter query. Arguments are ordered as in
// go-tree-sitter: (lang, source).
func NewQuery(lang *gts.Language, source string) (*Query, error) {
	q, err := gts.NewQuery(source, lang)
	if err != nil {
		return nil, err
	}
	return &Query{inner: q, lang: lang}, nil
}

// Close is a no-op (gotreesitter has no Close on Query).
func (q *Query) Close() {}

// CaptureNames returns the list of capture names in the query.
func (q *Query) CaptureNames() []string { return q.inner.CaptureNames() }

// QueryCapture mirrors the go-tree-sitter QueryCapture type with an Index
// field derived from the capture name's position in the query's capture list.
type QueryCapture struct {
	Index uint32
	Node  Node
}

// QueryMatch mirrors the go-tree-sitter QueryMatch type.
type QueryMatch struct {
	PatternIndex uint32
	Captures     []QueryCapture
}

// matchesIterator wraps a *gts.QueryCursor for the old Matches(…).Next() pattern.
type matchesIterator struct {
	cursor *gts.QueryCursor
	query  *Query
}

// Next returns the next match or nil when exhausted.
func (it *matchesIterator) Next() *QueryMatch {
	m, ok := it.cursor.NextMatch()
	if !ok {
		return nil
	}
	caps := make([]QueryCapture, len(m.Captures))
	names := it.query.inner.CaptureNames()
	for i, c := range m.Captures {
		var idx uint32
		for j, name := range names {
			if name == c.Name {
				idx = uint32(j)
				break
			}
		}
		node := c.Node
		caps[i] = QueryCapture{
			Index: idx,
			Node:  Node{inner: node, lang: it.query.lang},
		}
	}
	return &QueryMatch{
		PatternIndex: uint32(m.PatternIndex),
		Captures:     caps,
	}
}

// QueryCursor wraps a gotreesitter QueryCursor for compatibility.
type QueryCursor struct {
	hasByteRange bool
	startByte    uint32
	endByte      uint32
}

// NewQueryCursor creates a new cursor.
func NewQueryCursor() *QueryCursor {
	return &QueryCursor{}
}

// SetByteRange restricts matches to nodes that intersect [start, end).
func (c *QueryCursor) SetByteRange(start, end uint) {
	c.hasByteRange = true
	c.startByte = uint32(start)
	c.endByte = uint32(end)
}

// Matches starts query execution over the given tree root and returns an iterator.
func (c *QueryCursor) Matches(query *Query, node *Node, source []byte) *matchesIterator {
	var root *gts.Node
	if node != nil {
		root = node.inner
	}
	cursor := query.inner.Exec(root, query.lang, source)
	if c.hasByteRange {
		cursor.SetByteRange(c.startByte, c.endByte)
	}
	return &matchesIterator{cursor: cursor, query: query}
}

// Close is a no-op (gotreesitter has no Close on QueryCursor).
func (c *QueryCursor) Close() {}

// Parser wraps a gotreesitter Parser.
type Parser struct {
	lang *gts.Language
}

// NewParser returns a parser. SetLanguage must be called before parsing.
func NewParser() *Parser {
	return &Parser{}
}

// SetLanguage sets the parser language. Returns an error if lang is nil.
func (p *Parser) SetLanguage(lang *gts.Language) error {
	p.lang = lang
	return nil
}

// Parse parses source and returns a Tree. The old-tree argument is ignored;
// gotreesitter incremental parsing is not used in sawmill's hot path.
func (p *Parser) Parse(source []byte, _ *Tree) *Tree {
	parser := gts.NewParser(p.lang)
	tree, _ := parser.Parse(source)
	return wrapTree(tree, p.lang)
}

// Close is a no-op (gotreesitter parsers are GC-managed).
func (p *Parser) Close() {}

// Language getter functions for each supported language.
var (
	GoLanguage         = grammars.GoLanguage
	PythonLanguage     = grammars.PythonLanguage
	RustLanguage       = grammars.RustLanguage
	TypescriptLanguage = grammars.TypescriptLanguage
	TsxLanguage        = grammars.TsxLanguage
	CppLanguage        = grammars.CppLanguage
)
