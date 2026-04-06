// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

// Package handshake defines the wire types and binary-hash validation used
// in the initial exchange between the sawmill stdio process and the daemon.
//
// The protocol is:
//  1. Client sends a JSON Handshake line (with project root and binary hash)
//  2. Server validates the binary hash, loads the model, and sends a Response line
//  3. On success, the connection is used for MCP JSON-RPC
package handshake

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"sync"
)

// Handshake is the first message sent by the client to the daemon.
type Handshake struct {
	Root       string `json:"root"`
	BinaryHash string `json:"binary_hash"`
}

// Response is the daemon's reply to a Handshake.
type Response struct {
	Status string `json:"status"`
	Root   string `json:"root,omitempty"`
	Files  int    `json:"files,omitempty"`
	Error  string `json:"error,omitempty"`
}

var (
	binaryHashOnce  sync.Once
	binaryHashValue string
)

// BinaryHash returns the SHA-256 hex digest of the running executable.
// The result is cached after the first call. Returns "" if the hash
// cannot be computed (e.g. the executable is not readable).
func BinaryHash() string {
	binaryHashOnce.Do(func() {
		exe, err := os.Executable()
		if err != nil {
			return
		}
		data, err := os.ReadFile(exe)
		if err != nil {
			return
		}
		h := sha256.Sum256(data)
		binaryHashValue = hex.EncodeToString(h[:])
	})
	return binaryHashValue
}
