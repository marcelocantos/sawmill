// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package paths_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/marcelocantos/sawmill/paths"
)

func TestValidateRootRejectsFilesystemRoot(t *testing.T) {
	if err := paths.ValidateRoot(string(filepath.Separator)); !errors.Is(err, paths.ErrImplausibleRoot) {
		t.Fatalf("ValidateRoot(/) = %v, want ErrImplausibleRoot", err)
	}
}

func TestValidateRootRejectsHomeDirectory(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("no home directory")
	}
	if err := paths.ValidateRoot(home); !errors.Is(err, paths.ErrImplausibleRoot) {
		t.Fatalf("ValidateRoot(%q) = %v, want ErrImplausibleRoot", home, err)
	}
	// A trailing separator must not sneak past the comparison.
	if err := paths.ValidateRoot(home + string(filepath.Separator)); !errors.Is(err, paths.ErrImplausibleRoot) {
		t.Fatalf("ValidateRoot(%q/) = %v, want ErrImplausibleRoot", home, err)
	}
}

func TestValidateRootRejectsContainerDirectories(t *testing.T) {
	for _, dir := range []string{"/Users", "/Applications", "/Volumes", "/home"} {
		if err := paths.ValidateRoot(dir); !errors.Is(err, paths.ErrImplausibleRoot) {
			t.Errorf("ValidateRoot(%q) = %v, want ErrImplausibleRoot", dir, err)
		}
	}
}

func TestValidateRootAcceptsAProject(t *testing.T) {
	dir := t.TempDir()
	if err := paths.ValidateRoot(dir); err != nil {
		t.Fatalf("ValidateRoot(%q) = %v, want nil", dir, err)
	}
}

// The guard is a rail, not a wall: a caller that means it can opt out.
func TestValidateRootHonoursTheOptOut(t *testing.T) {
	t.Setenv(paths.AllowAnyRootEnv, "1")
	if err := paths.ValidateRoot(string(filepath.Separator)); err != nil {
		t.Fatalf("with %s set, ValidateRoot(/) = %v, want nil", paths.AllowAnyRootEnv, err)
	}
}
