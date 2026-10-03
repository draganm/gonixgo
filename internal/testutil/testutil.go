// Package testutil holds helpers shared by gonixgo's tests.
package testutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Go returns the go binary on PATH, skipping the test when there is none.
func Go(t *testing.T) string {
	t.Helper()
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go not on PATH")
	}
	return goBin
}

// WriteTree writes files, keyed by slash-separated relative path, under a
// new temporary directory and returns it with symlinks resolved.
func WriteTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for name, content := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// StdImportcfg builds the standard library without cgo and returns an
// importcfg file listing its archives.
func StdImportcfg(t *testing.T, goBin string) string {
	t.Helper()
	cmd := exec.Command(goBin, "list", "-export", "-f",
		"{{if .Export}}packagefile {{.ImportPath}}={{.Export}}{{end}}", "std")
	cmd.Dir = t.TempDir()
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOFLAGS=", "GOWORK=off")
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list -export std: %v", err)
	}
	file := filepath.Join(t.TempDir(), "importcfg.std")
	if err := os.WriteFile(file, out, 0o644); err != nil {
		t.Fatal(err)
	}
	return file
}
