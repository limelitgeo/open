// Copyright 2026 Limelit. Licensed under the Apache License, Version 2.0.
// See the LICENSE file in the repository root for the full terms.

// Package licensecheck holds the guard that keeps the copyright header on
// every source file.
//
// The header is not what makes the license valid; LICENSE and NOTICE do that.
// It is there so a single file lifted out of this repository carries its own
// provenance, which is the case a root-level LICENSE cannot cover.
package licensecheck

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const marker = "Copyright 2026 Limelit"

// TestEveryGoFileCarriesTheHeader fails on a new file that skipped it.
func TestEveryGoFileCarriesTheHeader(t *testing.T) {
	root := repoRoot(t)
	var missing []string

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name := d.Name(); name == ".git" || name == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		// Only the opening lines: a stray mention further down the file is
		// not a header.
		head := b
		if len(head) > 400 {
			head = head[:400]
		}
		if !strings.Contains(string(head), marker) {
			rel, _ := filepath.Rel(root, path)
			missing = append(missing, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(missing) > 0 {
		t.Errorf("these files are missing the copyright header:\n  %s\n\nAdd:\n\n// %s. Licensed under the Apache License, Version 2.0.\n// See the LICENSE file in the repository root for the full terms.\n",
			strings.Join(missing, "\n  "), marker)
	}
}

// TestLicenseAndNoticeNameTheHolder guards the two files that carry the claim.
// Apache-2.0 ships its appendix with a [yyyy] [name of copyright owner]
// placeholder, and a repository that never fills it asserts no holder at all.
func TestLicenseAndNoticeNameTheHolder(t *testing.T) {
	root := repoRoot(t)
	for _, name := range []string{"LICENSE", "NOTICE"} {
		b, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		s := string(b)
		if !strings.Contains(s, marker) {
			t.Errorf("%s does not name the copyright holder", name)
		}
		if strings.Contains(s, "[yyyy]") || strings.Contains(s, "[name of copyright owner]") {
			t.Errorf("%s still has the Apache template placeholder", name)
		}
	}
}

// repoRoot walks up to the directory holding go.mod.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found above the working directory")
		}
		dir = parent
	}
}
