package nar

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// goldenTree builds a tree with a regular file, an executable, a symlink
// and an empty directory.
func goldenTree(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "tree")
	for _, dir := range []string{"bin", "empty"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "bin", "run"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("a.txt", filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestHashGolden(t *testing.T) {
	// From `nix hash path --type sha256 --sri` on the same tree.
	const want = "sha256-YOfEjnDTXtQTPwBh/5K+yBGqcQGBNHSs8lSqesPd/S8="
	sum, err := Hash(goldenTree(t))
	if err != nil {
		t.Fatal(err)
	}
	if got := SRI(sum); got != want {
		t.Fatalf("SRI = %s, want %s", got, want)
	}
}

func TestHashMatchesNix(t *testing.T) {
	nix, err := exec.LookPath("nix")
	if err != nil {
		t.Skip("nix not on PATH")
	}
	root := goldenTree(t)
	// File sizes on both sides of the 8-byte padding boundary.
	for _, n := range []int{0, 1, 7, 8, 9} {
		name := filepath.Join(root, "pad"+strconv.Itoa(n))
		if err := os.WriteFile(name, bytes.Repeat([]byte("x"), n), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	out, err := exec.Command(nix, "--extra-experimental-features", "nix-command",
		"hash", "path", "--type", "sha256", "--sri", root).Output()
	if err != nil {
		t.Fatalf("nix hash path: %v", err)
	}
	sum, err := Hash(root)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := SRI(sum), strings.TrimSpace(string(out)); got != want {
		t.Fatalf("SRI = %s, nix says %s", got, want)
	}
}

func TestSRIRoundTrip(t *testing.T) {
	sum, err := Hash(goldenTree(t))
	if err != nil {
		t.Fatal(err)
	}
	back, err := ParseSRI(SRI(sum))
	if err != nil {
		t.Fatal(err)
	}
	if back != sum {
		t.Fatalf("ParseSRI(SRI(sum)) = %x, want %x", back, sum)
	}
}

func TestParseSRIRejectsOtherForms(t *testing.T) {
	for _, s := range []string{"", "sha512-AAAA", "sha256-not base64", "sha256-AAAA"} {
		if _, err := ParseSRI(s); err == nil {
			t.Errorf("ParseSRI(%q) succeeded, want an error", s)
		}
	}
}

func TestHashMissingPath(t *testing.T) {
	if _, err := Hash(filepath.Join(t.TempDir(), "absent")); err == nil {
		t.Fatal("Hash of a missing path succeeded")
	}
}
