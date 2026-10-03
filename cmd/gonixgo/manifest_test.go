package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type testManifest struct {
	ImportPath string   `json:"importPath"`
	GoFiles    []string `json:"goFiles"`
}

func TestLoadManifestStructuredAttrs(t *testing.T) {
	file := filepath.Join(t.TempDir(), ".attrs.json")
	const attrs = `{"name": "x", "manifest": {"importPath": "example.com/a", "goFiles": ["a.go"]}, "outputs": {"out": "/nix/store/abc-x"}}`
	if err := os.WriteFile(file, []byte(attrs), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NIX_ATTRS_JSON_FILE", file)

	var m testManifest
	out, err := loadManifest(&m)
	if err != nil {
		t.Fatal(err)
	}
	if out != "/nix/store/abc-x" || m.ImportPath != "example.com/a" || len(m.GoFiles) != 1 {
		t.Fatalf("out = %q, manifest = %+v", out, m)
	}
}

func TestLoadManifestEnvironment(t *testing.T) {
	t.Setenv("NIX_ATTRS_JSON_FILE", "")
	t.Setenv("manifest", `{"importPath": "example.com/b"}`)
	t.Setenv("out", "/nix/store/def-y")

	var m testManifest
	out, err := loadManifest(&m)
	if err != nil {
		t.Fatal(err)
	}
	if out != "/nix/store/def-y" || m.ImportPath != "example.com/b" {
		t.Fatalf("out = %q, manifest = %+v", out, m)
	}
}

func TestLoadManifestMissing(t *testing.T) {
	t.Setenv("NIX_ATTRS_JSON_FILE", "")
	t.Setenv("manifest", "")
	t.Setenv("out", "")
	var m testManifest
	if _, err := loadManifest(&m); err == nil || !strings.Contains(err.Error(), "NIX_ATTRS_JSON_FILE") {
		t.Fatalf("err = %v, want it to say where a manifest comes from", err)
	}
}
