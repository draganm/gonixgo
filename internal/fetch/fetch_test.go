package fetch

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/draganm/gonixgo/internal/nar"
	"github.com/draganm/gonixgo/internal/testutil"
)

func TestEscape(t *testing.T) {
	tests := []struct{ in, want string }{
		{"github.com/fatih/color", "github.com/fatih/color"},
		{"github.com/BurntSushi/toml", "github.com/!burnt!sushi/toml"},
		{"github.com/Azure/azure-sdk-for-go", "github.com/!azure/azure-sdk-for-go"},
		{"v1.0.0-RC1", "v1.0.0-!r!c1"},
		{"v0.0.0-20240101000000-abcdef123456", "v0.0.0-20240101000000-abcdef123456"},
	}
	for _, tt := range tests {
		if got := Escape(tt.in); got != tt.want {
			t.Errorf("Escape(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestCopyTreeKeepsTheNARHash(t *testing.T) {
	src := testutil.WriteTree(t, map[string]string{
		"go.mod":       "module example.com/m\n",
		"m.go":         "package m\n",
		"sub/deep/x.s": "// asm\n",
	})
	if err := os.WriteFile(filepath.Join(src, "run.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("m.go", filepath.Join(src, "link.go")); err != nil {
		t.Fatal(err)
	}
	// The module cache is read-only; the copy must still work.
	if err := os.Chmod(filepath.Join(src, "sub", "deep"), 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(filepath.Join(src, "sub", "deep"), 0o755) })

	dst := filepath.Join(t.TempDir(), "out")
	if err := copyTree(src, dst); err != nil {
		t.Fatal(err)
	}
	want, err := nar.Hash(src)
	if err != nil {
		t.Fatal(err)
	}
	got, err := nar.Hash(dst)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("copy has NAR hash %s, source has %s", nar.SRI(got), nar.SRI(want))
	}
	// The copy is writable, so Nix can move and clean it up.
	if err := os.WriteFile(filepath.Join(dst, "sub", "deep", "new"), nil, 0o644); err != nil {
		t.Fatalf("copied directories are not writable: %v", err)
	}
}

func TestRunDownloadsAModule(t *testing.T) {
	if os.Getenv("GONIXGO_NETWORK_TESTS") == "" {
		t.Skip("set GONIXGO_NETWORK_TESTS=1 to run tests that download modules")
	}
	out := filepath.Join(t.TempDir(), "out")
	work := t.TempDir()
	// The module cache is read-only; let the temp directory cleanup remove it.
	t.Cleanup(func() {
		filepath.WalkDir(work, func(path string, d fs.DirEntry, err error) error {
			if err == nil && d.IsDir() {
				os.Chmod(path, 0o755)
			}
			return nil
		})
	})
	m := Manifest{Go: testutil.Go(t), Path: "rsc.io/quote", Version: "v1.5.2"}
	if err := Run(m, out, work); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"go.mod", "quote.go"} {
		if _, err := os.Stat(filepath.Join(out, name)); err != nil {
			t.Errorf("fetched module lacks %s", name)
		}
	}
}

func TestRunReportsDownloadFailure(t *testing.T) {
	m := Manifest{Go: testutil.Go(t), Path: "example.invalid/nothing", Version: "v1.0.0"}
	t.Setenv("GOPROXY", "off")
	work := t.TempDir()
	if err := Run(m, filepath.Join(t.TempDir(), "out"), work); err == nil {
		t.Fatal("Run succeeded with GOPROXY=off")
	}
	home := filepath.Join(work, "home")
	for _, dir := range []string{
		filepath.Join(home, "Library", "Application Support", "go", "telemetry"),
		filepath.Join(home, ".config", "go", "telemetry"),
	} {
		if _, err := os.Stat(filepath.Join(dir, "local")); err == nil {
			t.Errorf("telemetry wrote %s/local", dir)
		}
	}
}
