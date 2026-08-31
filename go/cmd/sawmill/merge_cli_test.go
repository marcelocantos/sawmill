// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testdataRoot = "../../merge/testdata/python"

func readFixture(t *testing.T, dir, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(testdataRoot, dir, name))
	if err != nil {
		t.Fatalf("reading fixture %s/%s: %v", dir, name, err)
	}
	return data
}

func writeTmp(t *testing.T, dir, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
	return path
}

// TestMerge_Success exercises `sawmill merge` on the parallel_methods fixture,
// which should produce a clean merge (exit 0) with both new methods present.
func TestMerge_Success(t *testing.T) {
	tmp := t.TempDir()
	basePath := writeTmp(t, tmp, "base.py", readFixture(t, "parallel_methods", "base.py"))
	localPath := writeTmp(t, tmp, "local.py", readFixture(t, "parallel_methods", "ours.py"))
	remotePath := writeTmp(t, tmp, "remote.py", readFixture(t, "parallel_methods", "theirs.py"))
	outputPath := filepath.Join(tmp, "merged.py")

	var stderr bytes.Buffer
	code := runMerge([]string{
		"--base", basePath,
		"--local", localPath,
		"--remote", remotePath,
		"--output", outputPath,
	}, &stderr)

	if code != exitOK {
		t.Fatalf("expected exit %d, got %d; stderr: %s", exitOK, code, stderr.String())
	}

	got, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("reading output: %v", err)
	}

	expected := readFixture(t, "parallel_methods", "expected.py")
	if !bytes.Equal(got, expected) {
		t.Errorf("merged output mismatch\ngot:\n%s\nwant:\n%s", got, expected)
	}
}

// TestMerge_Conflict exercises `sawmill merge` on the delete_vs_modify fixture,
// which cannot be cleanly resolved and should exit 1 with conflict markers.
func TestMerge_Conflict(t *testing.T) {
	tmp := t.TempDir()
	basePath := writeTmp(t, tmp, "base.py", readFixture(t, "delete_vs_modify", "base.py"))
	localPath := writeTmp(t, tmp, "local.py", readFixture(t, "delete_vs_modify", "ours.py"))
	remotePath := writeTmp(t, tmp, "remote.py", readFixture(t, "delete_vs_modify", "theirs.py"))
	outputPath := filepath.Join(tmp, "merged.py")

	var stderr bytes.Buffer
	code := runMerge([]string{
		"--base", basePath,
		"--local", localPath,
		"--remote", remotePath,
		"--output", outputPath,
	}, &stderr)

	if code != exitConflict {
		t.Fatalf("expected exit %d, got %d; stderr: %s", exitConflict, code, stderr.String())
	}

	got, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("reading output: %v", err)
	}
	if !strings.Contains(string(got), "<<<<<<<") {
		t.Errorf("expected conflict markers in output, got:\n%s", got)
	}
}

// TestMergeDriver_Success exercises `sawmill merge-driver` on the
// parallel_methods fixture. The driver writes merged output back to the local
// (%A) file in place and exits 0 on a clean merge.
func TestMergeDriver_Success(t *testing.T) {
	tmp := t.TempDir()
	basePath := writeTmp(t, tmp, "base.py", readFixture(t, "parallel_methods", "base.py"))
	localPath := writeTmp(t, tmp, "local.py", readFixture(t, "parallel_methods", "ours.py"))
	remotePath := writeTmp(t, tmp, "remote.py", readFixture(t, "parallel_methods", "theirs.py"))

	var stderr bytes.Buffer
	code := runMergeDriver([]string{basePath, localPath, remotePath, "calc.py"}, &stderr)

	if code != exitOK {
		t.Fatalf("expected exit %d, got %d; stderr: %s", exitOK, code, stderr.String())
	}

	got, err := os.ReadFile(localPath)
	if err != nil {
		t.Fatalf("reading local after merge: %v", err)
	}

	expected := readFixture(t, "parallel_methods", "expected.py")
	if !bytes.Equal(got, expected) {
		t.Errorf("merged output mismatch\ngot:\n%s\nwant:\n%s", got, expected)
	}
}

// TestMergeDriver_Conflict exercises `sawmill merge-driver` on the
// delete_vs_modify fixture. The driver exits 1 and the local file contains
// conflict markers.
func TestMergeDriver_Conflict(t *testing.T) {
	tmp := t.TempDir()
	basePath := writeTmp(t, tmp, "base.py", readFixture(t, "delete_vs_modify", "base.py"))
	localPath := writeTmp(t, tmp, "local.py", readFixture(t, "delete_vs_modify", "ours.py"))
	remotePath := writeTmp(t, tmp, "remote.py", readFixture(t, "delete_vs_modify", "theirs.py"))

	var stderr bytes.Buffer
	code := runMergeDriver([]string{basePath, localPath, remotePath, "funcs.py"}, &stderr)

	if code != exitConflict {
		t.Fatalf("expected exit %d, got %d; stderr: %s", exitConflict, code, stderr.String())
	}

	got, err := os.ReadFile(localPath)
	if err != nil {
		t.Fatalf("reading local after merge: %v", err)
	}
	if !strings.Contains(string(got), "<<<<<<<") {
		t.Errorf("expected conflict markers in output, got:\n%s", got)
	}
}
