// Copyright 2026 Marcelo Cantos
// SPDX-License-Identifier: Apache-2.0

package paths

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// ErrImplausibleRoot is returned by ValidateRoot for a path that is a
// container of projects rather than a project.
var ErrImplausibleRoot = errors.New("paths: implausible project root")

// AllowAnyRootEnv opts out of the ValidateRoot check for a caller that really
// does mean it. It exists so the rule is a guard rail rather than a wall.
const AllowAnyRootEnv = "SAWMILL_ALLOW_ANY_ROOT"

// ValidateRoot reports whether absRoot is a plausible project root.
//
// Parsing a root recursively indexes and watches it, and on macOS every
// watched file costs a file descriptor, so the cost of a mistake here is not
// proportional to the mistake. A daemon once held stores for "/" (2.9 GB) and
// for the home directory (25 GB), and sat pinned at the per-process
// descriptor ceiling with /Applications in its watch set. Nothing rejected
// those roots because nothing was looking.
//
// The rule is deliberately crude — reject the handful of paths that are
// containers of projects rather than projects — because a cleverer test would
// have to guess, and guessing wrong in the permissive direction is what this
// exists to prevent. Setting SAWMILL_ALLOW_ANY_ROOT=1 skips the check.
func ValidateRoot(absRoot string) error {
	if os.Getenv(AllowAnyRootEnv) != "" {
		return nil
	}

	clean := filepath.Clean(absRoot)

	// The filesystem root, and on Windows any volume root.
	if clean == string(filepath.Separator) || clean == filepath.VolumeName(clean)+string(filepath.Separator) {
		return fmt.Errorf("%w: %q is the filesystem root", ErrImplausibleRoot, absRoot)
	}

	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if clean == filepath.Clean(home) {
			return fmt.Errorf("%w: %q is the home directory", ErrImplausibleRoot, absRoot)
		}
	}

	// The directories that hold home directories, and the macOS application
	// and volume roots. Each is a container of unrelated trees.
	containers := []string{"/Users", "/home", "/Applications", "/Volumes", "/System", "/Library", "/opt", "/usr", "/var", "/etc", "/private"}
	if runtime.GOOS == "windows" {
		containers = []string{`C:\Users`, `C:\Program Files`}
	}
	for _, c := range containers {
		if strings.EqualFold(clean, filepath.Clean(c)) {
			return fmt.Errorf("%w: %q holds unrelated trees, not one project", ErrImplausibleRoot, absRoot)
		}
	}

	return nil
}
