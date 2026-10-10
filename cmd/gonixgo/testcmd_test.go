package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// gonixgo test runs the binary its manifest names and keeps the log as
// the derivation's output.
func TestTestCommand(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	if err := os.MkdirAll(filepath.Join(src, "p"), 0o755); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "p.test")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho \"ran with $*\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out")
	attrs, err := json.Marshal(map[string]any{
		"manifest": map[string]any{
			"go": "/go/bin/go", "importPath": "example.com/m/p", "bin": bin,
			"srcDir": src, "subdir": "p", "flags": []string{"-v"}, "env": map[string]string{},
		},
		"outputs": map[string]string{"out": out},
	})
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, ".attrs.json")
	if err := os.WriteFile(file, attrs, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NIX_ATTRS_JSON_FILE", file)

	var stdout, stderr bytes.Buffer
	if code := run([]string{"test"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit code = %d\n%s", code, stderr.String())
	}
	log, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(log), "ran with -test.paniconexit0 -test.timeout=10m0s -test.v\n") ||
		!strings.Contains(string(log), "ok  \texample.com/m/p\t") {
		t.Errorf("log:\n%s", log)
	}
}
