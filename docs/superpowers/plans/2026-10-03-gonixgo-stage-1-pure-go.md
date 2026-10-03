# gonixgo Stage 1 (Pure Go, End to End) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A flake with `src = ./.` and no lockfile builds a pure-Go program with third-party dependencies, one derivation per package, resolved at evaluation time through `builtins.exec`.

**Architecture:** One Go binary, `gonixgo`, has two roles. At evaluation time `gonixgo resolve` runs `go list`, hashes each module directory as a NAR, pre-seeds it into the Nix store, and prints a Nix function describing every module, package and binary. At build time `gonixgo compile`, `link` and `fetch` are the builders of the derivations that function creates. A small Nix library (`nix/`) defines how each node kind is built and exposes `mkGoEnv { pkgs }` and `buildGoApplication`.

**Tech Stack:** Go (standard library only), Nix 2.26+ with flakes, nixpkgs `nixos-26.05` (Go 1.26.7).

**Spec:** `docs/superpowers/specs/2026-10-03-gonixgo-design.md`

## Global Constraints

- Module path is `github.com/draganm/gonixgo`. `go.mod` has `go 1.23` and no `require` lines: the tool uses only the Go standard library.
- Run Go commands through the flake's toolchain: `nix develop --command go …`.
- Any Nix command that evaluates a fixture needs `--option allow-unsafe-native-code-during-evaluation true`. A flake's `nixConfig` cannot supply it.
- `.#` flake references see only git-tracked files. `git add` new files before using `.#`. The integration driver uses `path:` and sees untracked files too.
- Never leave built binaries in the tree: no `go build` output inside the repository, always pass `--no-link` to `nix build`, delete any `result` symlink.
- Derivation name prefixes: `gomod-` (module), `gopkg-` (third-party package), `golocal-` (local package), `gosrc-` (local package source), `gobin-` (binary), `go-stdlib-` (standard library).
- Compile with `-buildid ""`; link with `-buildid=redacted`.
- Stage 1 rejects, with an explicit error from `resolve`: packages with cgo, C, C++, Objective-C, Fortran, SWIG or `.syso` files, and modules with a `replace` directive. `doCheck`, `checkFlags` and `packageOverrides` are accepted and ignored; `passthru.tests` is `{ }`.
- Work happens on branch `stage-1-pure-go`. Every commit message ends with these trailer lines, referred to below as `$TRAILERS`:

  ```
  Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>
  Claude-Session: https://claude.ai/code/session_012ue2EuU54PE7mfZNkxf3jU
  ```

  Set it once per shell:

  ```bash
  TRAILERS=$'Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>\nClaude-Session: https://claude.ai/code/session_012ue2EuU54PE7mfZNkxf3jU'
  ```

## Facts the code relies on

Captured from `go build -x -trimpath` with Go 1.26.8 on darwin/arm64, and from probes on Nix 2.26.1.

- Compile: `compile -o pkg.a -trimpath "<srcdir>=><rewrite>;<workdir>=>" -p <import path or main> -lang=go1.N [-complete] -buildid … [-shared] -nolocalimports -importcfg <cfg> [-embedcfg <cfg>] -pack [-symabis <f> -asmhdr <f>] ./a.go …`, run in the package directory with `PWD` set to it.
- `-complete` is passed only when the package has no assembly files.
- `-shared` is passed to `compile` and `asm` when the target's default build mode is PIE and the OS is not Windows. PIE is the default on darwin, ios, android and windows.
- Assembly: `asm -p <pkg> -trimpath … -I <workdir> -I $GOROOT/pkg/include -D GOOS_<os> -D GOARCH_<arch> [-shared] -gensymabis -o symabis ./*.s` before compiling, an empty `go_asm.h` created first; then one `asm … -o <file>.o ./<file>.s` per file; then the objects are appended to the archive.
- The Go distribution ships no `pack` tool (only `asm`, `cgo`, `compile`, `cover`, `fix`, `link`, `preprofile`, `vet`). Appending objects to the archive is done in Go.
- Link: `GOROOT='' link -o <out> -importcfg <cfg> [-X=runtime.godebugDefault=<DefaultGODEBUG>] -buildmode=<pie|exe> -buildid=… <main>.a`. The importcfg ends with a `modinfo "<quoted>"` line: the module info wrapped in two 16-byte markers.
- `nix store add --name N --mode nar --hash-algo sha256 DIR` produces the same store path as a fixed-output derivation named `N` with that NAR hash.
- `builtins.path { path = "<store path string>"; filter = …; }` works in pure flake evaluation; the filter receives absolute paths under that string.
- nixpkgs' `go` exposes `go.GOOS`, `go.GOARCH` and `go.version`.

## Review Focus

1. **Module paths with upper-case letters** (`github.com/BurntSushi/toml`): the fetch fallback must read Go's case-escaped module cache directory (`!burnt!sushi`), and store names must stay valid. Pinned in Task 3 (`TestSanitizeName`) and Task 12 (`TestEscape`).
2. **File names with spaces, quotes or `${`** (embed targets): the generated Nix must evaluate back to the same string. Pinned in Task 8 (`TestQuoteRoundTripsThroughNix`).
3. **A project with no third-party dependencies, and one with several main packages:** resolve must give an empty `modules` set and one binary per main package. Pinned in Task 6 (`TestBuildWithoutThirdParty`, `TestBuildSeveralMains`) and the `asm-embed` fixture in Task 15.
4. **Several broken packages at once** (a missing `go.sum` entry and a missing import): all are reported in one run, with the `go mod tidy` hint. Pinned in Task 6 (`TestBuildReportsEveryLoadError`).
5. **`ldflags` written as one string with a quoted value** (`-X 'main.msg=hello world'`): split as `go build -ldflags` splits. Pinned in Task 11 (`TestSplitFlags`, `TestCompileLinkRun`).

## File Structure

```
flake.nix                       dev shell, lib.mkGoEnv, packages.gonixgo, legacyPackages.{goEnv,fixtures}
default.nix                     { pkgs }: non-flake entry point
.envrc, .gitignore              from the golang-flake template
go.mod                          module github.com/draganm/gonixgo, go 1.23
cmd/gonixgo/main.go             subcommand dispatch
cmd/gonixgo/manifest.go         loading a derivation's manifest and output path
cmd/gonixgo/{resolve,compile,link,fetch}.go   one subcommand each
internal/nar/                   NAR serialisation, hashing, SRI
internal/storepath/             store name sanitising, fixed-output paths
internal/testutil/              test helpers: go binary, temp trees, std importcfg
internal/golist/                running and decoding `go list` and `go env`
internal/modinfo/               the module info `go build` embeds
internal/graph/                 the graph model and its construction from go list output
internal/modcache/              go.sum parsing, hash cache, store pre-seeding
internal/emit/                  graph to Nix text
internal/resolve/               the resolve pipeline
internal/gotool/                locating and running compile, asm, link
internal/compile/               compiling one package
internal/link/                  linking one binary
internal/fetch/                 downloading one module
nix/tool.nix                    the gonixgo binary
nix/stdlib.nix                  the standard library derivation
nix/builders.nix                fetchModule, localDir, compile, link
nix/build-go-application.nix    buildGoApplication
nix/mk-go-env.nix               mkGoEnv
tests/fixtures.nix              fixture derivations
tests/fixtures/hello-deps/      pure Go with third-party dependencies
tests/fixtures/asm-embed/       assembly, embeds, two main packages, no dependencies
tests/run.sh                    integration driver
```

---

### Task 1: Scaffold the flake, the module and the command dispatcher

**Files:**
- Create: `flake.nix`, `.envrc`, `.gitignore`, `go.mod`, `flake.lock` (generated)
- Create: `cmd/gonixgo/main.go`
- Test: `cmd/gonixgo/main_test.go`

**Interfaces:**
- Produces: `type command func(args []string, stdout io.Writer) error`, the registry `var commands = map[string]command{}`, and `func run(args []string, stdout, stderr io.Writer) int` in package `main`. Later tasks register a subcommand with `func init() { commands["name"] = fn }`.

- [ ] **Step 1: Switch to the branch and copy the template**

The branch already exists; it holds the plan.

```bash
cd /Users/dragan/draganm/gonixgo
git switch stage-1-pure-go
cp ~/.claude/templates/golang-flake/flake.nix ~/.claude/templates/golang-flake/.envrc ~/.claude/templates/golang-flake/.gitignore .
```

- [ ] **Step 2: Fill in the template**

In `flake.nix` replace `{{PROJECT_NAME}}` with `gonixgo` and `{{NIXPKGS_VERSION}}` with `26.05`. Append to `.gitignore`:

```
# nix build outputs
result
result-*

# built binary
/gonixgo
```

Run: `grep -n '{{' flake.nix .envrc .gitignore`
Expected: no output.

- [ ] **Step 3: Initialise the module with the flake's Go**

```bash
git add flake.nix .envrc .gitignore
nix develop --command go mod init github.com/draganm/gonixgo
nix develop --command go mod edit -go=1.23 -toolchain=none
direnv allow .
git add go.mod flake.lock
```

Run: `nix develop --command go version && cat go.mod`
Expected: `go version go1.26.7 …`, and `go.mod` containing exactly `module github.com/draganm/gonixgo` and `go 1.23`.

- [ ] **Step 4: Write the failing test**

`cmd/gonixgo/main_test.go`:

```go
package main

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestRunWithoutArguments(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run(nil, &stdout, &stderr); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "usage: gonixgo") {
		t.Fatalf("stderr = %q, want usage", stderr.String())
	}
}

func TestRunUnknownCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"frobnicate"}, &stdout, &stderr); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), `unknown command "frobnicate"`) {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRunDispatches(t *testing.T) {
	commands["test-ok"] = func(args []string, stdout io.Writer) error {
		_, err := io.WriteString(stdout, strings.Join(args, ","))
		return err
	}
	commands["test-fail"] = func([]string, io.Writer) error { return errors.New("boom") }
	defer delete(commands, "test-ok")
	defer delete(commands, "test-fail")

	var stdout, stderr bytes.Buffer
	if code := run([]string{"test-ok", "a", "b"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if stdout.String() != "a,b" {
		t.Fatalf("stdout = %q, want a,b", stdout.String())
	}

	stdout.Reset()
	if code := run([]string{"test-fail"}, &stdout, &stderr); code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "gonixgo test-fail: boom") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}
```

- [ ] **Step 5: Run the test to verify it fails**

Run: `nix develop --command go test ./cmd/gonixgo/`
Expected: FAIL, `undefined: run` and `undefined: commands`.

- [ ] **Step 6: Write the implementation**

`cmd/gonixgo/main.go`:

```go
// Command gonixgo resolves Go package graphs into Nix expressions and runs
// the build steps of the derivations those expressions describe.
package main

import (
	"fmt"
	"io"
	"os"
)

// command is one gonixgo subcommand. args are the arguments after its name.
type command func(args []string, stdout io.Writer) error

// commands is filled by the init function of each subcommand's file.
var commands = map[string]command{}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: gonixgo <resolve|compile|link|fetch> ...")
		return 2
	}
	cmd, ok := commands[args[0]]
	if !ok {
		fmt.Fprintf(stderr, "gonixgo: unknown command %q\n", args[0])
		return 2
	}
	if err := cmd(args[1:], stdout); err != nil {
		fmt.Fprintf(stderr, "gonixgo %s: %v\n", args[0], err)
		return 1
	}
	return 0
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}
```

- [ ] **Step 7: Run the test to verify it passes**

Run: `nix develop --command go test ./cmd/gonixgo/`
Expected: `ok  	github.com/draganm/gonixgo/cmd/gonixgo`

- [ ] **Step 8: Commit**

```bash
git add flake.nix flake.lock .envrc .gitignore go.mod cmd/gonixgo
git status --short   # only the files above, .direnv/ ignored
git commit -m "feat: scaffold flake, module and command dispatcher" -m "$TRAILERS"
```

---

### Task 2: NAR serialisation and hashing

**Files:**
- Create: `internal/nar/nar.go`
- Test: `internal/nar/nar_test.go`

**Interfaces:**
- Produces:
  - `func Write(w io.Writer, root string) error`
  - `func Hash(root string) ([32]byte, error)`
  - `func SRI(sum [32]byte) string` returning `sha256-<base64>`
  - `func ParseSRI(s string) ([32]byte, error)`

- [ ] **Step 1: Write the failing test**

`internal/nar/nar_test.go`:

```go
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
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `nix develop --command go test ./internal/nar/`
Expected: FAIL, `undefined: Hash`, `undefined: SRI`, `undefined: ParseSRI`.

- [ ] **Step 3: Write the implementation**

`internal/nar/nar.go`:

```go
// Package nar serialises file trees in the Nix archive format and hashes
// them the way Nix does for recursive fixed-output paths.
package nar

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Write serialises the file tree rooted at root to w as a NAR.
func Write(w io.Writer, root string) error {
	if err := writeString(w, "nix-archive-1"); err != nil {
		return err
	}
	return writeNode(w, root)
}

// Hash returns the SHA-256 of the NAR serialisation of root.
func Hash(root string) ([32]byte, error) {
	var sum [32]byte
	h := sha256.New()
	if err := Write(h, root); err != nil {
		return sum, err
	}
	h.Sum(sum[:0])
	return sum, nil
}

// SRI formats sum as the "sha256-<base64>" string Nix's outputHash accepts.
func SRI(sum [32]byte) string {
	return "sha256-" + base64.StdEncoding.EncodeToString(sum[:])
}

// ParseSRI is the inverse of SRI.
func ParseSRI(s string) ([32]byte, error) {
	var sum [32]byte
	b64, ok := strings.CutPrefix(s, "sha256-")
	if !ok {
		return sum, fmt.Errorf("nar: %q is not a sha256 SRI hash", s)
	}
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil || len(raw) != len(sum) {
		return sum, fmt.Errorf("nar: %q is not a sha256 SRI hash", s)
	}
	copy(sum[:], raw)
	return sum, nil
}

func writeNode(w io.Writer, path string) error {
	fi, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if err := writeStrings(w, "(", "type"); err != nil {
		return err
	}
	switch mode := fi.Mode(); {
	case mode.IsRegular():
		err = writeRegular(w, path, fi)
	case mode&os.ModeSymlink != 0:
		var target string
		if target, err = os.Readlink(path); err == nil {
			err = writeStrings(w, "symlink", "target", target)
		}
	case mode.IsDir():
		err = writeDir(w, path)
	default:
		err = fmt.Errorf("nar: %s: unsupported file type %s", path, mode.Type())
	}
	if err != nil {
		return err
	}
	return writeString(w, ")")
}

func writeRegular(w io.Writer, path string, fi os.FileInfo) error {
	if err := writeString(w, "regular"); err != nil {
		return err
	}
	// Nix records only the owner's execute bit.
	if fi.Mode()&0o100 != 0 {
		if err := writeStrings(w, "executable", ""); err != nil {
			return err
		}
	}
	if err := writeString(w, "contents"); err != nil {
		return err
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	size := fi.Size()
	if err := writeLen(w, uint64(size)); err != nil {
		return err
	}
	if _, err := io.CopyN(w, f, size); err != nil {
		return fmt.Errorf("nar: %s: %w", path, err)
	}
	return writePad(w, uint64(size))
}

func writeDir(w io.Writer, path string) error {
	if err := writeString(w, "directory"); err != nil {
		return err
	}
	// os.ReadDir sorts by file name, the order the format requires.
	entries, err := os.ReadDir(path)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err := writeStrings(w, "entry", "(", "name", e.Name(), "node"); err != nil {
			return err
		}
		if err := writeNode(w, filepath.Join(path, e.Name())); err != nil {
			return err
		}
		if err := writeString(w, ")"); err != nil {
			return err
		}
	}
	return nil
}

func writeStrings(w io.Writer, ss ...string) error {
	for _, s := range ss {
		if err := writeString(w, s); err != nil {
			return err
		}
	}
	return nil
}

// writeString writes a length-prefixed string padded to 8 bytes.
func writeString(w io.Writer, s string) error {
	if err := writeLen(w, uint64(len(s))); err != nil {
		return err
	}
	if _, err := io.WriteString(w, s); err != nil {
		return err
	}
	return writePad(w, uint64(len(s)))
}

func writeLen(w io.Writer, n uint64) error {
	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:], n)
	_, err := w.Write(buf[:])
	return err
}

func writePad(w io.Writer, n uint64) error {
	var zeros [8]byte
	_, err := w.Write(zeros[:(8-n%8)%8])
	return err
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `nix develop --command go test ./internal/nar/ -v`
Expected: PASS for all five tests (`TestHashMatchesNix` runs, since `nix` is on `PATH`).

- [ ] **Step 5: Commit**

```bash
git add internal/nar
git commit -m "feat(nar): add NAR serialisation and hashing" -m "$TRAILERS"
```

---

### Task 3: Store names and fixed-output store paths

**Files:**
- Create: `internal/storepath/storepath.go`
- Test: `internal/storepath/storepath_test.go`

**Interfaces:**
- Produces:
  - `func SanitizeName(s string) string` — maps any string to a valid store path name of at most 211 bytes. Callers always pass a prefixed name (`gomod-…`), so a leading `.` cannot occur.
  - `func FixedOutput(storeDir, name string, narHash [32]byte) string` — the store path of a recursive SHA-256 fixed-output path.

- [ ] **Step 1: Write the failing test**

`internal/storepath/storepath_test.go`:

```go
package storepath

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestFixedOutputGolden(t *testing.T) {
	// `nix store add --name gomod-example.com-mod-v1.0.0 --mode nar` on a
	// tree whose NAR hash is the one below printed this path.
	raw, err := base64.StdEncoding.DecodeString("nxE+RQ9BqCLb4kQ7dEgKNtudirqbZBN+Np7RzJT3Qws=")
	if err != nil {
		t.Fatal(err)
	}
	var sum [32]byte
	copy(sum[:], raw)
	const want = "/nix/store/13f9vr10w98dsl8n79bpqi28i0bg6awr-gomod-example.com-mod-v1.0.0"
	if got := FixedOutput("/nix/store", "gomod-example.com-mod-v1.0.0", sum); got != want {
		t.Fatalf("FixedOutput = %s, want %s", got, want)
	}
}

func TestSanitizeName(t *testing.T) {
	tests := []struct{ in, want string }{
		{"gomod-github.com/fatih/color-v1.18.0", "gomod-github.com-fatih-color-v1.18.0"},
		{"gomod-github.com/BurntSushi/toml-v1.6.0", "gomod-github.com-BurntSushi-toml-v1.6.0"},
		{"gopkg-gopkg.in/yaml.v3-v3.0.1", "gopkg-gopkg.in-yaml.v3-v3.0.1"},
		{"gomod-example.com/a-v0.0.0-20240101000000-abcdef123456+incompatible", "gomod-example.com-a-v0.0.0-20240101000000-abcdef123456+incompatible"},
		{"golocal-example.com/a b@c~d!e", "golocal-example.com-a-b-c-d-e"},
		{"golocal-example.com/päth", "golocal-example.com-p--th"},
	}
	for _, tt := range tests {
		if got := SanitizeName(tt.in); got != tt.want {
			t.Errorf("SanitizeName(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestSanitizeNameBoundsLength(t *testing.T) {
	a := SanitizeName("gopkg-" + strings.Repeat("a", 300) + "/x")
	b := SanitizeName("gopkg-" + strings.Repeat("a", 300) + "/y")
	if len(a) > 211 || len(b) > 211 {
		t.Fatalf("lengths %d and %d exceed 211", len(a), len(b))
	}
	if a == b {
		t.Fatal("different inputs truncated to the same name")
	}
	if a != SanitizeName("gopkg-"+strings.Repeat("a", 300)+"/x") {
		t.Fatal("SanitizeName is not deterministic")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `nix develop --command go test ./internal/storepath/`
Expected: FAIL, `undefined: FixedOutput`, `undefined: SanitizeName`.

- [ ] **Step 3: Write the implementation**

`internal/storepath/storepath.go`:

```go
// Package storepath computes Nix store names and fixed-output store paths.
package storepath

import (
	"crypto/sha256"
	"encoding/hex"
)

// maxNameLen is the longest name Nix accepts in a store path.
const maxNameLen = 211

// SanitizeName maps s to a valid store path name: every byte outside
// [A-Za-z0-9+._?=-] becomes '-'. Names longer than Nix allows are cut and
// given a hash suffix so distinct inputs stay distinct.
func SanitizeName(s string) string {
	b := []byte(s)
	for i, c := range b {
		switch {
		case 'a' <= c && c <= 'z', 'A' <= c && c <= 'Z', '0' <= c && c <= '9':
		case c == '+', c == '-', c == '.', c == '_', c == '?', c == '=':
		default:
			b[i] = '-'
		}
	}
	if len(b) <= maxNameLen {
		return string(b)
	}
	sum := sha256.Sum256([]byte(s))
	return string(b[:maxNameLen-17]) + "-" + hex.EncodeToString(sum[:8])
}

// FixedOutput returns the store path of a fixed-output path with the given
// name whose content has the given recursive (NAR) SHA-256 hash. It is the
// path both `nix store add --mode nar` and a fixed-output derivation with
// outputHashMode = "recursive" produce.
func FixedOutput(storeDir, name string, narHash [32]byte) string {
	fingerprint := "source:sha256:" + hex.EncodeToString(narHash[:]) + ":" + storeDir + ":" + name
	digest := sha256.Sum256([]byte(fingerprint))
	var folded [20]byte
	for i, b := range digest {
		folded[i%len(folded)] ^= b
	}
	return storeDir + "/" + base32(folded[:]) + "-" + name
}

// alphabet is Nix's base-32 alphabet: no e, o, u or t.
const alphabet = "0123456789abcdfghijklmnpqrsvwxyz"

// base32 encodes b the way Nix does, least significant bits last.
func base32(b []byte) string {
	n := (len(b)*8-1)/5 + 1
	out := make([]byte, 0, n)
	for i := n - 1; i >= 0; i-- {
		bit := i * 5
		idx, off := bit/8, uint(bit%8)
		c := b[idx] >> off
		if idx+1 < len(b) {
			c |= b[idx+1] << (8 - off)
		}
		out = append(out, alphabet[c&0x1f])
	}
	return string(out)
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `nix develop --command go test ./internal/storepath/ -v`
Expected: PASS for all three tests.

- [ ] **Step 5: Commit**

```bash
git add internal/storepath
git commit -m "feat(storepath): add store names and fixed-output paths" -m "$TRAILERS"
```

---

### Task 4: Running and decoding `go list`

**Files:**
- Create: `internal/testutil/testutil.go`
- Create: `internal/golist/golist.go`
- Test: `internal/golist/golist_test.go`

**Interfaces:**
- Produces, in `internal/testutil`:
  - `func Go(t *testing.T) string` — the `go` on `PATH`, or skips the test.
  - `func WriteTree(t *testing.T, files map[string]string) string` — writes files under a new temp directory, returns it with symlinks resolved.
  - `func StdImportcfg(t *testing.T, goBin string) string` — builds the standard library without cgo and returns an importcfg file listing its archives.
- Produces, in `internal/golist`:
  - `type Module struct { Path, Version string; Main bool; Dir, GoVersion string; Replace *Module }`
  - `type PackageError struct { Pos, Err string }`
  - `type Package struct` with fields `Dir, ImportPath, Name string; Standard, DepOnly bool; Module *Module; DefaultGODEBUG string; GoFiles, CgoFiles, CFiles, CXXFiles, MFiles, HFiles, FFiles, SFiles, SwigFiles, SwigCXXFiles, SysoFiles, EmbedPatterns, EmbedFiles, Imports []string; ImportMap map[string]string; Error *PackageError`
  - `type Options struct { Go, Dir, GOOS, GOARCH, CgoEnabled string; Tags []string; Stderr io.Writer }`
  - `func List(o Options, patterns ...string) ([]Package, error)`
  - `func Decode(r io.Reader) ([]Package, error)`
  - `func Env(o Options, keys ...string) (map[string]string, error)`

- [ ] **Step 1: Write the test helpers**

`internal/testutil/testutil.go`:

```go
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
```

- [ ] **Step 2: Write the failing test**

`internal/golist/golist_test.go`:

```go
package golist

import (
	"reflect"
	"strings"
	"testing"

	"github.com/draganm/gonixgo/internal/testutil"
)

var appFiles = map[string]string{
	"go.mod": "module example.com/app\n\ngo 1.21\n",
	"main.go": `package main

import (
	"fmt"

	"example.com/app/internal/greet"
)

func main() { fmt.Println(greet.Hello()) }
`,
	"internal/greet/greet.go": "package greet\n\nfunc Hello() string { return \"hello\" }\n",
}

func index(pkgs []Package) map[string]Package {
	m := make(map[string]Package, len(pkgs))
	for _, p := range pkgs {
		m[p.ImportPath] = p
	}
	return m
}

func TestListClassifiesPackages(t *testing.T) {
	goBin := testutil.Go(t)
	dir := testutil.WriteTree(t, appFiles)

	pkgs, err := List(Options{Go: goBin, Dir: dir}, ".")
	if err != nil {
		t.Fatal(err)
	}
	by := index(pkgs)

	main, ok := by["example.com/app"]
	if !ok {
		t.Fatalf("example.com/app missing from %d packages", len(pkgs))
	}
	if main.DepOnly || main.Name != "main" || main.Module == nil || !main.Module.Main {
		t.Errorf("main package = %+v", main)
	}
	if main.Dir != dir {
		t.Errorf("main.Dir = %s, want %s", main.Dir, dir)
	}
	if !reflect.DeepEqual(main.GoFiles, []string{"main.go"}) {
		t.Errorf("main.GoFiles = %v", main.GoFiles)
	}

	greet := by["example.com/app/internal/greet"]
	if !greet.DepOnly || greet.Module == nil || !greet.Module.Main {
		t.Errorf("greet package = %+v", greet)
	}
	if fmtPkg := by["fmt"]; !fmtPkg.Standard {
		t.Errorf("fmt package = %+v, want Standard", fmtPkg)
	}
}

func TestListReportsBrokenPackagesWithoutFailing(t *testing.T) {
	goBin := testutil.Go(t)
	files := map[string]string{
		"go.mod":  "module example.com/app\n\ngo 1.21\n",
		"main.go": "package main\n\nimport _ \"example.com/app/missing\"\n\nfunc main() {}\n",
	}
	pkgs, err := List(Options{Go: goBin, Dir: testutil.WriteTree(t, files)}, ".")
	if err != nil {
		t.Fatalf("List failed, want the error on the package: %v", err)
	}
	missing := index(pkgs)["example.com/app/missing"]
	if missing.Error == nil || missing.Error.Err == "" {
		t.Fatalf("missing package = %+v, want Error set", missing)
	}
}

func TestListAppliesTags(t *testing.T) {
	goBin := testutil.Go(t)
	files := map[string]string{
		"go.mod":   "module example.com/app\n\ngo 1.21\n",
		"main.go":  "package main\n\nfunc main() {}\n",
		"extra.go": "//go:build extra\n\npackage main\n\nvar _ = 1\n",
	}
	dir := testutil.WriteTree(t, files)
	pkgs, err := List(Options{Go: goBin, Dir: dir, Tags: []string{"extra"}}, ".")
	if err != nil {
		t.Fatal(err)
	}
	got := index(pkgs)["example.com/app"].GoFiles
	if !reflect.DeepEqual(got, []string{"extra.go", "main.go"}) {
		t.Fatalf("GoFiles = %v, want extra.go and main.go", got)
	}
}

func TestDecode(t *testing.T) {
	const stream = `{"ImportPath": "fmt", "Standard": true}
{"ImportPath": "example.com/a", "Module": {"Path": "example.com/a", "Main": true},
 "Error": {"Pos": "a.go:1:1", "Err": "boom"}}`
	pkgs, err := Decode(strings.NewReader(stream))
	if err != nil {
		t.Fatal(err)
	}
	if len(pkgs) != 2 || !pkgs[0].Standard || pkgs[1].Error.Err != "boom" || !pkgs[1].Module.Main {
		t.Fatalf("decoded %+v", pkgs)
	}
}

func TestDecodeRejectsGarbage(t *testing.T) {
	if _, err := Decode(strings.NewReader(`{"ImportPath": `)); err == nil {
		t.Fatal("Decode of truncated input succeeded")
	}
}

func TestEnv(t *testing.T) {
	goBin := testutil.Go(t)
	dir := testutil.WriteTree(t, appFiles)
	env, err := Env(Options{Go: goBin, Dir: dir, GOOS: "plan9", GOARCH: "amd64"}, "GOOS", "GOARCH", "GOVERSION")
	if err != nil {
		t.Fatal(err)
	}
	if env["GOOS"] != "plan9" || env["GOARCH"] != "amd64" || !strings.HasPrefix(env["GOVERSION"], "go") {
		t.Fatalf("env = %v", env)
	}
}
```

- [ ] **Step 3: Run the test to verify it fails**

Run: `nix develop --command go test ./internal/golist/`
Expected: FAIL, `undefined: List`, `undefined: Options`, `undefined: Decode`, `undefined: Env`.

- [ ] **Step 4: Write the implementation**

`internal/golist/golist.go`:

```go
// Package golist runs `go list` and `go env` and decodes their output.
package golist

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// Module is the part of go list's Module that gonixgo uses.
type Module struct {
	Path      string
	Version   string
	Main      bool
	Dir       string
	GoVersion string
	Replace   *Module
}

// PackageError is a package load error reported by go list -e.
type PackageError struct {
	Pos string
	Err string
}

// Package is the part of go list's Package that gonixgo uses.
type Package struct {
	Dir            string
	ImportPath     string
	Name           string
	Standard       bool
	DepOnly        bool
	Module         *Module
	DefaultGODEBUG string

	GoFiles      []string
	CgoFiles     []string
	CFiles       []string
	CXXFiles     []string
	MFiles       []string
	HFiles       []string
	FFiles       []string
	SFiles       []string
	SwigFiles    []string
	SwigCXXFiles []string
	SysoFiles    []string

	EmbedPatterns []string
	EmbedFiles    []string

	Imports   []string
	ImportMap map[string]string

	Error *PackageError
}

// Options says how to run the go command.
type Options struct {
	Go         string   // path to the go binary
	Dir        string   // working directory, the module root
	GOOS       string   // "" keeps the environment's
	GOARCH     string   // "" keeps the environment's
	CgoEnabled string   // "0", "1", or "" for Go's default
	Tags       []string // build tags
	Stderr     io.Writer
}

// environ is the caller's environment with the variables that decide the
// package graph pinned. GOPROXY, GOPRIVATE, GOMODCACHE, NETRC and the Go
// env file are left alone so private modules resolve as they do for the
// user.
func (o Options) environ() []string {
	set := map[string]string{
		"GOFLAGS":     "-mod=readonly",
		"GOWORK":      "off",
		"GOTOOLCHAIN": "local",
		// os/exec only sets PWD when Env is nil. go derives package
		// directories from it, so it must name Dir.
		"PWD": o.Dir,
	}
	if o.GOOS != "" {
		set["GOOS"] = o.GOOS
	}
	if o.GOARCH != "" {
		set["GOARCH"] = o.GOARCH
	}
	if o.CgoEnabled != "" {
		set["CGO_ENABLED"] = o.CgoEnabled
	}
	var env []string
	for _, kv := range os.Environ() {
		key, _, _ := strings.Cut(kv, "=")
		if _, pinned := set[key]; !pinned {
			env = append(env, kv)
		}
	}
	for k, v := range set {
		env = append(env, k+"="+v)
	}
	return env
}

func (o Options) command(args ...string) *exec.Cmd {
	cmd := exec.Command(o.Go, args...)
	cmd.Dir = o.Dir
	cmd.Env = o.environ()
	cmd.Stderr = o.Stderr
	if cmd.Stderr == nil {
		cmd.Stderr = os.Stderr
	}
	return cmd
}

// List runs `go list -e -deps -json` for patterns. Packages that fail to
// load are returned with Error set; List itself fails only when the go
// command does.
func List(o Options, patterns ...string) ([]Package, error) {
	args := []string{"list", "-e", "-deps", "-json"}
	if len(o.Tags) > 0 {
		args = append(args, "-tags", strings.Join(o.Tags, ","))
	}
	args = append(args, "--")
	args = append(args, patterns...)
	out, err := o.command(args...).Output()
	if err != nil {
		return nil, fmt.Errorf("go list: %w", err)
	}
	return Decode(bytes.NewReader(out))
}

// Decode reads the concatenated JSON objects go list -json prints.
func Decode(r io.Reader) ([]Package, error) {
	var pkgs []Package
	dec := json.NewDecoder(r)
	for {
		var p Package
		err := dec.Decode(&p)
		if err == io.EOF {
			return pkgs, nil
		}
		if err != nil {
			return nil, fmt.Errorf("decoding go list output: %w", err)
		}
		pkgs = append(pkgs, p)
	}
}

// Env runs `go env -json` for keys.
func Env(o Options, keys ...string) (map[string]string, error) {
	out, err := o.command(append([]string{"env", "-json"}, keys...)...).Output()
	if err != nil {
		return nil, fmt.Errorf("go env: %w", err)
	}
	env := map[string]string{}
	if err := json.Unmarshal(out, &env); err != nil {
		return nil, fmt.Errorf("decoding go env output: %w", err)
	}
	return env, nil
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `nix develop --command go test ./internal/golist/ -v`
Expected: PASS for all six tests.

- [ ] **Step 6: Commit**

```bash
git add internal/testutil internal/golist
git commit -m "feat(golist): run and decode go list and go env" -m "$TRAILERS"
```

---

### Task 5: Module info as `go build -trimpath` embeds it

**Files:**
- Create: `internal/modinfo/modinfo.go`
- Test: `internal/modinfo/modinfo_test.go`

**Interfaces:**
- Produces:
  - `type Module struct { Path, Version, Sum string }`
  - `type Setting struct { Key, Value string }`
  - `type Info struct { Path string; Main Module; Deps []Module; Settings []Setting }`
  - `func (i Info) String() string`
  - `func Settings(env map[string]string, tags []string, godebug string) []Setting` — `env` holds `go env` values for `CGO_ENABLED`, `GOARCH`, `GOOS` and the architecture key (`GOARM64`, `GOAMD64`, …).
  - `func Wrap(info string) string` — adds the two 16-byte markers the linker's `modinfo` importcfg line carries.

- [ ] **Step 1: Write the failing test**

`internal/modinfo/modinfo_test.go`:

```go
package modinfo

import (
	"strconv"
	"strings"
	"testing"
)

// goldenEnv and goldenInfo reproduce what `go build -trimpath` with Go
// 1.26.8 embedded for module example.com/gx on darwin/arm64.
var goldenEnv = map[string]string{
	"CGO_ENABLED": "1", "GOARCH": "arm64", "GOOS": "darwin", "GOARM64": "v8.0", "GOAMD64": "v1",
}

const goldenGodebug = "containermaxprocs=0,cryptocustomrand=1,decoratemappings=0,tlssecpmlkem=0,tlssha1=1,updatemaxprocs=0,urlstrictcolons=0,x509sha256skid=0"

const goldenInfo = "path\texample.com/gx\n" +
	"mod\texample.com/gx\t(devel)\t\n" +
	"build\t-buildmode=exe\n" +
	"build\t-compiler=gc\n" +
	"build\t-trimpath=true\n" +
	"build\tDefaultGODEBUG=" + goldenGodebug + "\n" +
	"build\tCGO_ENABLED=1\n" +
	"build\tGOARCH=arm64\n" +
	"build\tGOOS=darwin\n" +
	"build\tGOARM64=v8.0\n"

func TestStringGolden(t *testing.T) {
	info := Info{
		Path:     "example.com/gx",
		Main:     Module{Path: "example.com/gx", Version: "(devel)"},
		Settings: Settings(goldenEnv, nil, goldenGodebug),
	}
	if got := info.String(); got != goldenInfo {
		t.Fatalf("String() =\n%q\nwant\n%q", got, goldenInfo)
	}
}

func TestStringWithDepsAndTags(t *testing.T) {
	info := Info{
		Path: "example.com/app/cmd/app",
		Main: Module{Path: "example.com/app", Version: "(devel)"},
		Deps: []Module{
			{Path: "github.com/fatih/color", Version: "v1.18.0", Sum: "h1:abc="},
			{Path: "golang.org/x/sys", Version: "v0.25.0"},
		},
		Settings: Settings(map[string]string{"CGO_ENABLED": "0", "GOARCH": "amd64", "GOOS": "linux", "GOAMD64": "v1"}, []string{"netgo", "osusergo"}, ""),
	}
	const want = "path\texample.com/app/cmd/app\n" +
		"mod\texample.com/app\t(devel)\t\n" +
		"dep\tgithub.com/fatih/color\tv1.18.0\th1:abc=\n" +
		"dep\tgolang.org/x/sys\tv0.25.0\t\n" +
		"build\t-buildmode=exe\n" +
		"build\t-compiler=gc\n" +
		"build\t-tags=netgo,osusergo\n" +
		"build\t-trimpath=true\n" +
		"build\tCGO_ENABLED=0\n" +
		"build\tGOARCH=amd64\n" +
		"build\tGOOS=linux\n" +
		"build\tGOAMD64=v1\n"
	if got := info.String(); got != want {
		t.Fatalf("String() =\n%q\nwant\n%q", got, want)
	}
}

func TestStringQuotesValuesWithSpaces(t *testing.T) {
	info := Info{Path: "p", Main: Module{Path: "m", Version: "(devel)"}, Settings: []Setting{{"-tags", "a b"}}}
	if got := info.String(); !strings.HasSuffix(got, "build\t-tags=\"a b\"\n") {
		t.Fatalf("String() = %q, want the value quoted", got)
	}
}

func TestWrap(t *testing.T) {
	// The quoted form go build -x printed in importcfg.link.
	const wantPrefix = `"0w\xaf\f\x92t\b\x02A\xe1\xc1\a\xe6\xd6\x18\xe6path\t`
	const wantSuffix = `GOARM64=v8.0\n\xf92C1\x86\x18 r\x00\x82B\x10A\x16\xd8\xf2"`
	got := strconv.Quote(Wrap(goldenInfo))
	if !strings.HasPrefix(got, wantPrefix) || !strings.HasSuffix(got, wantSuffix) {
		t.Fatalf("Quote(Wrap(info)) = %s", got)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `nix develop --command go test ./internal/modinfo/`
Expected: FAIL, `undefined: Info`, `undefined: Settings`, `undefined: Wrap`.

- [ ] **Step 3: Write the implementation**

`internal/modinfo/modinfo.go`:

```go
// Package modinfo produces the module information `go build -trimpath`
// embeds in a binary, the text `go version -m` prints.
package modinfo

import (
	"fmt"
	"strconv"
	"strings"
)

// Module is one mod or dep line.
type Module struct {
	Path    string
	Version string
	Sum     string
}

// Setting is one build line.
type Setting struct {
	Key   string
	Value string
}

// Info mirrors runtime/debug.BuildInfo without the Go version, which the
// linker records separately.
type Info struct {
	Path     string   // import path of the main package
	Main     Module   // the main module
	Deps     []Module // modules providing packages to the binary, sorted by path
	Settings []Setting
}

// String formats i as runtime/debug.BuildInfo.String does.
func (i Info) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "path\t%s\n", i.Path)
	fmt.Fprintf(&b, "mod\t%s\t%s\t%s\n", i.Main.Path, i.Main.Version, i.Main.Sum)
	for _, d := range i.Deps {
		fmt.Fprintf(&b, "dep\t%s\t%s\t%s\n", d.Path, d.Version, d.Sum)
	}
	for _, s := range i.Settings {
		key, value := s.Key, s.Value
		if key == "" || strings.ContainsAny(key, "= \t\r\n\"`") {
			key = strconv.Quote(key)
		}
		if strings.ContainsAny(value, " \t\r\n\"`") {
			value = strconv.Quote(value)
		}
		fmt.Fprintf(&b, "build\t%s=%s\n", key, value)
	}
	return b.String()
}

// archKey names the go env variable recorded for each GOARCH.
var archKey = map[string]string{
	"386":      "GO386",
	"amd64":    "GOAMD64",
	"arm":      "GOARM",
	"arm64":    "GOARM64",
	"mips":     "GOMIPS",
	"mipsle":   "GOMIPS",
	"mips64":   "GOMIPS64",
	"mips64le": "GOMIPS64",
	"ppc64":    "GOPPC64",
	"ppc64le":  "GOPPC64",
	"riscv64":  "GORISCV64",
	"wasm":     "GOWASM",
}

// Settings returns the build settings cmd/go records for a -trimpath
// build, in its order. Under -trimpath cmd/go omits -ldflags and the CGO_*
// flag variables.
func Settings(env map[string]string, tags []string, godebug string) []Setting {
	s := []Setting{{"-buildmode", "exe"}, {"-compiler", "gc"}}
	if len(tags) > 0 {
		s = append(s, Setting{"-tags", strings.Join(tags, ",")})
	}
	s = append(s, Setting{"-trimpath", "true"})
	if godebug != "" {
		s = append(s, Setting{"DefaultGODEBUG", godebug})
	}
	s = append(s,
		Setting{"CGO_ENABLED", env["CGO_ENABLED"]},
		Setting{"GOARCH", env["GOARCH"]},
		Setting{"GOOS", env["GOOS"]},
	)
	if key := archKey[env["GOARCH"]]; key != "" && env[key] != "" {
		s = append(s, Setting{key, env[key]})
	}
	return s
}

// The markers cmd/go puts around the module info so tools can find it in
// the binary.
const (
	infoStart = "\x30\x77\xaf\x0c\x92\x74\x08\x02\x41\xe1\xc1\x07\xe6\xd6\x18\xe6"
	infoEnd   = "\xf9\x32\x43\x31\x86\x18\x20\x72\x00\x82\x42\x10\x41\x16\xd8\xf2"
)

// Wrap returns info between the markers, the value of the linker
// importcfg's modinfo line.
func Wrap(info string) string {
	return infoStart + info + infoEnd
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `nix develop --command go test ./internal/modinfo/ -v`
Expected: PASS for all four tests.

- [ ] **Step 5: Commit**

```bash
git add internal/modinfo
git commit -m "feat(modinfo): format module info like go build -trimpath" -m "$TRAILERS"
```

---

### Task 6: The graph model and its construction

**Files:**
- Create: `internal/graph/graph.go` (model, `Build`, binaries)
- Create: `internal/graph/pkg.go` (one package: classification, files, embeds, `-lang`)
- Test: `internal/graph/graph_test.go`, `internal/graph/pkg_test.go`

**Interfaces:**
- Consumes: `golist.Package`, `golist.Module` (Task 4); `storepath.SanitizeName` (Task 3); `modinfo.Info`, `modinfo.Module`, `modinfo.Settings` (Task 5).
- Produces:
  - `type Module struct { Key, Path, Version, Dir, Sum, Hash, Name string }` — `Key` is `path@version`; `Hash` is filled later by `modcache`.
  - `type Package struct { ImportPath, Name, SrcName string; Local, IsMain bool; ModuleKey, ModulePath, Subdir, TrimTo, Lang string; GoFiles, SFiles []string; Embed map[string][]string; SrcFiles, Deps []string }` — `Subdir` is the package directory relative to its source root (`""` for the root). `SrcFiles` are relative to the source root and set for local packages only.
  - `type Binary struct { Name, DrvName, Main string; Deps []string; Modinfo, Godebug string }`
  - `type Graph struct { GoVersion, GOOS, GOARCH string; CgoEnabled bool; Modules map[string]*Module; Packages map[string]*Package; Bins []*Binary }`
  - `func (g *Graph) ModuleList() []*Module` — sorted by key.
  - `type Input struct { Packages []golist.Package; Src string; Env map[string]string; Tags []string; Sums map[string]string }`
  - `func Build(in Input) (*Graph, error)`
  - `type LoadError struct { Problems []string }` with `Error() string`.

- [ ] **Step 1: Write the failing tests for one package**

`internal/graph/pkg_test.go`:

```go
package graph

import (
	"reflect"
	"testing"
)

func TestLang(t *testing.T) {
	tests := []struct{ in, want string }{
		{"", "go1.16"},
		{"1.21", "go1.21"},
		{"1.21.3", "go1.21"},
		{"1.21rc1", "go1.21"},
		{"1.24.0", "go1.24"},
		{"1.9", "go1.9"},
	}
	for _, tt := range tests {
		if got := lang(tt.in); got != tt.want {
			t.Errorf("lang(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestExecName(t *testing.T) {
	tests := []struct{ in, want string }{
		{"example.com/app", "app"},
		{"example.com/app/cmd/server", "server"},
		{"example.com/app/v2", "app"},
		{"example.com/app/v10", "app"},
		{"example.com/tool/v1", "v1"},
		{"example.com/tool/v0", "v0"},
		{"example.com/tool/vendor", "vendor"},
		{"app", "app"},
	}
	for _, tt := range tests {
		if got := execName(tt.in); got != tt.want {
			t.Errorf("execName(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestEmbedMap(t *testing.T) {
	files := []string{
		"static/index.html",
		"static/css/site.css",
		"static/.hidden",
		"static/_draft/a.txt",
		"tmpl/a b.tmpl",
	}
	patterns := []string{"static", "all:static", "static/*", "tmpl/*.tmpl", "static/index.html"}
	want := map[string][]string{
		// A directory embeds its tree without dot and underscore names.
		"static": {"static/index.html", "static/css/site.css"},
		// all: keeps them.
		"all:static": {"static/index.html", "static/css/site.css", "static/.hidden", "static/_draft/a.txt"},
		// A glob names hidden files and directories directly, so they are
		// embedded; only names below a matched directory are dropped.
		"static/*":          {"static/index.html", "static/css/site.css", "static/.hidden", "static/_draft/a.txt"},
		"tmpl/*.tmpl":       {"tmpl/a b.tmpl"},
		"static/index.html": {"static/index.html"},
	}
	if got := embedMap(patterns, files); !reflect.DeepEqual(got, want) {
		t.Fatalf("embedMap =\n%v\nwant\n%v", got, want)
	}
	if got := embedMap(nil, nil); got != nil {
		t.Fatalf("embedMap(nil, nil) = %v, want nil", got)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `nix develop --command go test ./internal/graph/`
Expected: FAIL, `undefined: lang`, `undefined: execName`, `undefined: embedMap`.

- [ ] **Step 3: Write `pkg.go`**

`internal/graph/pkg.go`:

```go
package graph

import (
	"fmt"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/draganm/gonixgo/internal/golist"
	"github.com/draganm/gonixgo/internal/storepath"
)

// newPackage turns one non-standard go list package into a graph node. It
// also returns the module a third-party package comes from.
func newPackage(p *golist.Package, src string, byPath map[string]*golist.Package) (*Package, *Module, error) {
	m := p.Module
	switch {
	case m == nil:
		return nil, nil, fmt.Errorf("%s: not part of a module", p.ImportPath)
	case m.Replace != nil:
		return nil, nil, fmt.Errorf("%s: module %s is replaced; replace directives are not supported yet", p.ImportPath, m.Path)
	case hasNonGo(p):
		return nil, nil, fmt.Errorf("%s: cgo, C, C++, Objective-C, Fortran, SWIG and .syso files are not supported yet", p.ImportPath)
	}

	pkg := &Package{
		ImportPath: p.ImportPath,
		IsMain:     p.Name == "main",
		ModulePath: m.Path,
		Lang:       lang(m.GoVersion),
		GoFiles:    p.GoFiles,
		SFiles:     p.SFiles,
		Embed:      embedMap(p.EmbedPatterns, p.EmbedFiles),
	}
	for _, imp := range p.Imports {
		if mapped, ok := p.ImportMap[imp]; ok {
			imp = mapped
		}
		dep, ok := byPath[imp]
		if !ok {
			return nil, nil, fmt.Errorf("%s: imports %s, which go list did not report", p.ImportPath, imp)
		}
		if !dep.Standard {
			pkg.Deps = append(pkg.Deps, imp)
		}
	}
	sort.Strings(pkg.Deps)

	if m.Main {
		rel, err := filepath.Rel(src, p.Dir)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return nil, nil, fmt.Errorf("%s: directory %s is outside src %s", p.ImportPath, p.Dir, src)
		}
		pkg.Local = true
		pkg.Name = storepath.SanitizeName("golocal-" + p.ImportPath)
		pkg.SrcName = storepath.SanitizeName("gosrc-" + p.ImportPath)
		pkg.Subdir = slashDir(rel)
		pkg.TrimTo = p.ImportPath
		pkg.SrcFiles = srcFiles(pkg.Subdir, p)
		return pkg, nil, nil
	}

	if m.Version == "" || m.Dir == "" {
		return nil, nil, fmt.Errorf("%s: module %s has no version or is not in the module cache", p.ImportPath, m.Path)
	}
	rel, err := filepath.Rel(m.Dir, p.Dir)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, nil, fmt.Errorf("%s: directory %s is outside module directory %s", p.ImportPath, p.Dir, m.Dir)
	}
	key := m.Path + "@" + m.Version
	pkg.Name = storepath.SanitizeName("gopkg-" + p.ImportPath + "-" + m.Version)
	pkg.ModuleKey = key
	pkg.Subdir = slashDir(rel)
	// cmd/go rewrites a module-cache directory to module@version/subdir.
	pkg.TrimTo = key + strings.TrimPrefix(p.ImportPath, m.Path)
	mod := &Module{
		Key:     key,
		Path:    m.Path,
		Version: m.Version,
		Dir:     m.Dir,
		Name:    storepath.SanitizeName("gomod-" + m.Path + "-" + m.Version),
	}
	return pkg, mod, nil
}

func hasNonGo(p *golist.Package) bool {
	return len(p.CgoFiles)+len(p.CFiles)+len(p.CXXFiles)+len(p.MFiles)+len(p.FFiles)+
		len(p.SwigFiles)+len(p.SwigCXXFiles)+len(p.SysoFiles) > 0
}

// slashDir turns a relative directory into the form the graph uses: slash
// separated, "" for the root.
func slashDir(rel string) string {
	if rel == "." {
		return ""
	}
	return filepath.ToSlash(rel)
}

// srcFiles lists every file a local package's compile reads, relative to
// the source root.
func srcFiles(subdir string, p *golist.Package) []string {
	var files []string
	for _, group := range [][]string{p.GoFiles, p.SFiles, p.HFiles, p.EmbedFiles} {
		for _, f := range group {
			files = append(files, path.Join(subdir, f))
		}
	}
	sort.Strings(files)
	return slices.Compact(files)
}

// lang returns the -lang value cmd/go passes for a module's go directive:
// its major.minor, or go1.16 when the module has none.
func lang(goVersion string) string {
	if goVersion == "" {
		goVersion = "1.16"
	}
	end, dots := len(goVersion), 0
	for i, c := range goVersion {
		if c == '.' {
			dots++
			if dots < 2 {
				continue
			}
		} else if '0' <= c && c <= '9' {
			continue
		}
		end = i
		break
	}
	return "go" + goVersion[:end]
}

// execName names a main package's binary as cmd/go does: the last element
// of its import path, or the one before a major-version suffix.
func execName(importPath string) string {
	dir, elem := path.Split(importPath)
	if elem != importPath && isVersionElement(elem) {
		_, elem = path.Split(path.Clean(dir))
	}
	return elem
}

// isVersionElement reports whether s is a major-version path element such
// as v2 (not v0 or v1).
func isVersionElement(s string) bool {
	if len(s) < 2 || s[0] != 'v' || s[1] == '0' || s[1] == '1' && len(s) == 2 {
		return false
	}
	for i := 1; i < len(s); i++ {
		if s[i] < '0' || '9' < s[i] {
			return false
		}
	}
	return true
}

// embedMap assigns the files go list matched to the //go:embed patterns
// that matched them, the mapping the compiler's -embedcfg needs. go list
// reports only the union.
func embedMap(patterns, files []string) map[string][]string {
	if len(patterns) == 0 {
		return nil
	}
	m := make(map[string][]string, len(patterns))
	for _, pattern := range patterns {
		glob, all := strings.CutPrefix(pattern, "all:")
		matched := []string{}
		for _, file := range files {
			if embedMatches(glob, all, file) {
				matched = append(matched, file)
			}
		}
		m[pattern] = matched
	}
	return m
}

// embedMatches reports whether glob embeds file. A glob that matches the
// file itself embeds it. A glob that matches a directory above it embeds
// the directory's tree, minus names beginning with '.' or '_' unless the
// pattern had the all: prefix.
func embedMatches(glob string, all bool, file string) bool {
	if ok, _ := path.Match(glob, file); ok {
		return true
	}
	parts := strings.Split(file, "/")
	for i := 1; i < len(parts); i++ {
		if ok, _ := path.Match(glob, strings.Join(parts[:i], "/")); !ok {
			continue
		}
		if all {
			return true
		}
		hidden := false
		for _, elem := range parts[i:] {
			if strings.HasPrefix(elem, ".") || strings.HasPrefix(elem, "_") {
				hidden = true
			}
		}
		if !hidden {
			return true
		}
	}
	return false
}
```

`graph.go` does not exist yet, so add the model first to make the package compile. `internal/graph/graph.go`:

```go
// Package graph models the build of a Go program as modules, packages and
// binaries, and constructs that model from go list output.
package graph

import (
	"maps"
	"slices"
)

// Module is a fetched module that owns at least one package in the graph.
type Module struct {
	Key     string // path@version
	Path    string
	Version string
	Dir     string // extracted directory in the module cache
	Sum     string // the module's h1: line from go.sum, "" if absent
	Hash    string // SRI NAR hash of Dir, filled by modcache
	Name    string // store name of the fetch derivation
}

// Package is one compile.
type Package struct {
	ImportPath string
	Name       string // derivation name
	SrcName    string // store name of a local package's source
	Local      bool   // in the main module
	IsMain     bool
	ModuleKey  string // third-party: key into Graph.Modules
	ModulePath string
	Subdir     string // package directory relative to its source root, "" for the root
	TrimTo     string // what -trimpath rewrites the source directory to
	Lang       string // -lang value, e.g. go1.24
	GoFiles    []string
	SFiles     []string
	Embed      map[string][]string // //go:embed pattern to matched files
	SrcFiles   []string            // local: files to copy, relative to the source root
	Deps       []string            // direct non-standard imports, sorted
}

// Binary is one link.
type Binary struct {
	Name    string   // file name in $out/bin
	DrvName string   // derivation name
	Main    string   // import path of the main package
	Deps    []string // transitive non-standard imports of Main, sorted
	Modinfo string   // module info to embed
	Godebug string   // DefaultGODEBUG, "" for none
}

// Graph is everything resolve emits.
type Graph struct {
	GoVersion  string // toolchain version without the "go" prefix
	GOOS       string
	GOARCH     string
	CgoEnabled bool
	Modules    map[string]*Module
	Packages   map[string]*Package
	Bins       []*Binary
}

// ModuleList returns the modules sorted by key.
func (g *Graph) ModuleList() []*Module {
	mods := make([]*Module, 0, len(g.Modules))
	for _, key := range slices.Sorted(maps.Keys(g.Modules)) {
		mods = append(mods, g.Modules[key])
	}
	return mods
}
```

- [ ] **Step 4: Run the package-level tests to verify they pass**

Run: `nix develop --command go test ./internal/graph/ -run 'TestLang|TestExecName|TestEmbedMap' -v`
Expected: PASS for all three.

- [ ] **Step 5: Write the failing tests for `Build`**

`internal/graph/graph_test.go`:

```go
package graph

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/draganm/gonixgo/internal/golist"
)

var (
	mainMod   = &golist.Module{Path: "example.com/app", Main: true, Dir: "/src", GoVersion: "1.24"}
	colorMod  = &golist.Module{Path: "github.com/fatih/color", Version: "v1.18.0", Dir: "/mod/github.com/fatih/color@v1.18.0", GoVersion: "1.17"}
	isattyMod = &golist.Module{Path: "github.com/mattn/go-isatty", Version: "v0.0.20", Dir: "/mod/github.com/mattn/go-isatty@v0.0.20", GoVersion: "1.15"}
	sysMod    = &golist.Module{Path: "golang.org/x/sys", Version: "v0.25.0", Dir: "/mod/golang.org/x/sys@v0.25.0", GoVersion: "1.18"}

	testEnv = map[string]string{"GOVERSION": "go1.26.8", "GOOS": "darwin", "GOARCH": "arm64", "CGO_ENABLED": "1", "GOARM64": "v8.0"}
	sums    = map[string]string{
		"github.com/fatih/color@v1.18.0":     "h1:color",
		"github.com/mattn/go-isatty@v0.0.20": "h1:isatty",
	}
)

func std(importPath string) golist.Package {
	return golist.Package{ImportPath: importPath, Standard: true, DepOnly: true}
}

// appPackages is `go list -deps` output for a program with one local
// library and three third-party packages, dependencies first.
func appPackages() []golist.Package {
	return []golist.Package{
		std("unsafe"),
		std("fmt"),
		{ImportPath: "golang.org/x/sys/unix", Name: "unix", Dir: sysMod.Dir + "/unix", Module: sysMod, DepOnly: true,
			GoFiles: []string{"syscall.go"}, SFiles: []string{"asm_bsd_arm64.s"}, Imports: []string{"unsafe"}},
		{ImportPath: "github.com/mattn/go-isatty", Name: "isatty", Dir: isattyMod.Dir, Module: isattyMod, DepOnly: true,
			GoFiles: []string{"isatty.go"}, Imports: []string{"golang.org/x/sys/unix"}},
		{ImportPath: "github.com/fatih/color", Name: "color", Dir: colorMod.Dir, Module: colorMod, DepOnly: true,
			GoFiles: []string{"color.go", "doc.go"}, Imports: []string{"fmt", "github.com/mattn/go-isatty"}},
		{ImportPath: "example.com/app/internal/greet", Name: "greet", Dir: "/src/internal/greet", Module: mainMod, DepOnly: true,
			GoFiles: []string{"greet.go"}, SFiles: []string{"greet_arm64.s"}, HFiles: []string{"greet.h"},
			EmbedPatterns: []string{"static"}, EmbedFiles: []string{"static/a.txt"}, Imports: []string{"fmt"}},
		{ImportPath: "example.com/app", Name: "main", Dir: "/src", Module: mainMod, DefaultGODEBUG: "a=1",
			GoFiles: []string{"main.go"}, Imports: []string{"example.com/app/internal/greet", "fmt", "github.com/fatih/color"}},
	}
}

func build(t *testing.T, pkgs []golist.Package) *Graph {
	t.Helper()
	g, err := Build(Input{Packages: pkgs, Src: "/src", Env: testEnv, Sums: sums})
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func TestBuildPackages(t *testing.T) {
	g := build(t, appPackages())

	if g.GoVersion != "1.26.8" || g.GOOS != "darwin" || g.GOARCH != "arm64" || !g.CgoEnabled {
		t.Errorf("graph header = %+v", g)
	}
	if len(g.Packages) != 5 {
		t.Fatalf("%d packages, want 5 (standard library excluded)", len(g.Packages))
	}

	wantMain := &Package{
		ImportPath: "example.com/app", Name: "golocal-example.com-app", SrcName: "gosrc-example.com-app",
		Local: true, IsMain: true, ModulePath: "example.com/app", Subdir: "", TrimTo: "example.com/app", Lang: "go1.24",
		GoFiles: []string{"main.go"}, SrcFiles: []string{"main.go"},
		Deps: []string{"example.com/app/internal/greet", "github.com/fatih/color"},
	}
	if got := g.Packages["example.com/app"]; !reflect.DeepEqual(got, wantMain) {
		t.Errorf("main package =\n%+v\nwant\n%+v", got, wantMain)
	}

	wantGreet := &Package{
		ImportPath: "example.com/app/internal/greet", Name: "golocal-example.com-app-internal-greet",
		SrcName: "gosrc-example.com-app-internal-greet",
		Local:   true, ModulePath: "example.com/app", Subdir: "internal/greet", TrimTo: "example.com/app/internal/greet", Lang: "go1.24",
		GoFiles: []string{"greet.go"}, SFiles: []string{"greet_arm64.s"},
		Embed: map[string][]string{"static": {"static/a.txt"}},
		SrcFiles: []string{
			"internal/greet/greet.go", "internal/greet/greet.h",
			"internal/greet/greet_arm64.s", "internal/greet/static/a.txt",
		},
	}
	if got := g.Packages["example.com/app/internal/greet"]; !reflect.DeepEqual(got, wantGreet) {
		t.Errorf("greet package =\n%+v\nwant\n%+v", got, wantGreet)
	}

	wantUnix := &Package{
		ImportPath: "golang.org/x/sys/unix", Name: "gopkg-golang.org-x-sys-unix-v0.25.0",
		ModuleKey: "golang.org/x/sys@v0.25.0", ModulePath: "golang.org/x/sys", Subdir: "unix",
		TrimTo: "golang.org/x/sys@v0.25.0/unix", Lang: "go1.18",
		GoFiles: []string{"syscall.go"}, SFiles: []string{"asm_bsd_arm64.s"},
	}
	if got := g.Packages["golang.org/x/sys/unix"]; !reflect.DeepEqual(got, wantUnix) {
		t.Errorf("unix package =\n%+v\nwant\n%+v", got, wantUnix)
	}

	color := g.Packages["github.com/fatih/color"]
	if color.Local || color.Subdir != "" || color.TrimTo != "github.com/fatih/color@v1.18.0" ||
		!reflect.DeepEqual(color.Deps, []string{"github.com/mattn/go-isatty"}) {
		t.Errorf("color package = %+v", color)
	}
}

func TestBuildModules(t *testing.T) {
	g := build(t, appPackages())
	var keys []string
	for _, m := range g.ModuleList() {
		keys = append(keys, m.Key)
	}
	want := []string{"github.com/fatih/color@v1.18.0", "github.com/mattn/go-isatty@v0.0.20", "golang.org/x/sys@v0.25.0"}
	if !reflect.DeepEqual(keys, want) {
		t.Fatalf("modules = %v, want %v", keys, want)
	}
	wantColor := &Module{
		Key: "github.com/fatih/color@v1.18.0", Path: "github.com/fatih/color", Version: "v1.18.0",
		Dir: "/mod/github.com/fatih/color@v1.18.0", Sum: "h1:color", Name: "gomod-github.com-fatih-color-v1.18.0",
	}
	if got := g.Modules["github.com/fatih/color@v1.18.0"]; !reflect.DeepEqual(got, wantColor) {
		t.Errorf("color module = %+v, want %+v", got, wantColor)
	}
}

func TestBuildBinary(t *testing.T) {
	g := build(t, appPackages())
	if len(g.Bins) != 1 {
		t.Fatalf("%d binaries, want 1", len(g.Bins))
	}
	want := &Binary{
		Name: "app", DrvName: "gobin-app", Main: "example.com/app", Godebug: "a=1",
		Deps: []string{
			"example.com/app/internal/greet", "github.com/fatih/color",
			"github.com/mattn/go-isatty", "golang.org/x/sys/unix",
		},
		Modinfo: "path\texample.com/app\n" +
			"mod\texample.com/app\t(devel)\t\n" +
			"dep\tgithub.com/fatih/color\tv1.18.0\th1:color\n" +
			"dep\tgithub.com/mattn/go-isatty\tv0.0.20\th1:isatty\n" +
			"dep\tgolang.org/x/sys\tv0.25.0\t\n" +
			"build\t-buildmode=exe\n" +
			"build\t-compiler=gc\n" +
			"build\t-trimpath=true\n" +
			"build\tDefaultGODEBUG=a=1\n" +
			"build\tCGO_ENABLED=1\n" +
			"build\tGOARCH=arm64\n" +
			"build\tGOOS=darwin\n" +
			"build\tGOARM64=v8.0\n",
	}
	if got := g.Bins[0]; !reflect.DeepEqual(got, want) {
		t.Fatalf("binary =\n%+v\nwant\n%+v", got, want)
	}
}

func TestBuildWithoutThirdParty(t *testing.T) {
	g := build(t, []golist.Package{
		std("fmt"),
		{ImportPath: "example.com/app", Name: "main", Dir: "/src", Module: mainMod, GoFiles: []string{"main.go"}, Imports: []string{"fmt"}},
	})
	if len(g.Modules) != 0 || len(g.ModuleList()) != 0 {
		t.Fatalf("modules = %v, want none", g.Modules)
	}
	if len(g.Bins) != 1 || len(g.Bins[0].Deps) != 0 {
		t.Fatalf("bins = %+v", g.Bins)
	}
}

func TestBuildSeveralMains(t *testing.T) {
	g := build(t, []golist.Package{
		std("fmt"),
		{ImportPath: "example.com/app/cmd/zeta", Name: "main", Dir: "/src/cmd/zeta", Module: mainMod, GoFiles: []string{"main.go"}, Imports: []string{"fmt"}},
		{ImportPath: "example.com/app/cmd/alpha", Name: "main", Dir: "/src/cmd/alpha", Module: mainMod, GoFiles: []string{"main.go"}, Imports: []string{"fmt"}},
	})
	if len(g.Bins) != 2 || g.Bins[0].Name != "alpha" || g.Bins[1].Name != "zeta" {
		t.Fatalf("bins = %+v, want alpha then zeta", g.Bins)
	}
}

func TestBuildWindowsBinaryName(t *testing.T) {
	env := map[string]string{"GOVERSION": "go1.26.8", "GOOS": "windows", "GOARCH": "amd64", "CGO_ENABLED": "0", "GOAMD64": "v1"}
	g, err := Build(Input{Src: "/src", Env: env, Packages: []golist.Package{
		{ImportPath: "example.com/app", Name: "main", Dir: "/src", Module: mainMod, GoFiles: []string{"main.go"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if g.Bins[0].Name != "app.exe" || g.CgoEnabled {
		t.Fatalf("bin = %+v, cgo = %v", g.Bins[0], g.CgoEnabled)
	}
}

func TestBuildReportsEveryLoadError(t *testing.T) {
	_, err := Build(Input{Src: "/src", Env: testEnv, Packages: []golist.Package{
		{ImportPath: "github.com/a/b", DepOnly: true, Error: &golist.PackageError{Err: "missing go.sum entry for module providing package github.com/a/b"}},
		{ImportPath: "example.com/app/missing", DepOnly: true, Error: &golist.PackageError{Pos: "main.go:3:8", Err: "package example.com/app/missing is not in std"}},
		{ImportPath: "example.com/app", Name: "main", Dir: "/src", Module: mainMod, GoFiles: []string{"main.go"}},
	}})
	var loadErr *LoadError
	if !errors.As(err, &loadErr) {
		t.Fatalf("err = %v, want a *LoadError", err)
	}
	if len(loadErr.Problems) != 2 {
		t.Fatalf("problems = %v, want both packages", loadErr.Problems)
	}
	msg := err.Error()
	for _, want := range []string{"github.com/a/b: missing go.sum entry", "example.com/app/missing: main.go:3:8: package", "go mod tidy"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message lacks %q:\n%s", want, msg)
		}
	}
}

func TestBuildRejectsUnsupported(t *testing.T) {
	tests := []struct {
		name string
		pkg  golist.Package
		want string
	}{
		{"cgo", golist.Package{ImportPath: "example.com/app/c", Name: "c", Dir: "/src/c", Module: mainMod, DepOnly: true, GoFiles: []string{"c.go"}, CgoFiles: []string{"cgo.go"}}, "are not supported yet"},
		{"syso", golist.Package{ImportPath: "example.com/app/c", Name: "c", Dir: "/src/c", Module: mainMod, DepOnly: true, GoFiles: []string{"c.go"}, SysoFiles: []string{"x.syso"}}, "are not supported yet"},
		{"replace", golist.Package{ImportPath: "github.com/x/y", Name: "y", Dir: "/elsewhere", DepOnly: true, GoFiles: []string{"y.go"},
			Module: &golist.Module{Path: "github.com/x/y", Version: "v1.0.0", Replace: &golist.Module{Path: "../y", Dir: "/elsewhere"}}}, "replace directives are not supported yet"},
		{"outside src", golist.Package{ImportPath: "example.com/app/o", Name: "o", Dir: "/other/o", Module: mainMod, DepOnly: true, GoFiles: []string{"o.go"}}, "is outside src"},
		{"no module", golist.Package{ImportPath: "example.com/app/n", Name: "n", Dir: "/src/n", DepOnly: true, GoFiles: []string{"n.go"}}, "not part of a module"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Build(Input{Src: "/src", Env: testEnv, Packages: []golist.Package{
				tt.pkg,
				{ImportPath: "example.com/app", Name: "main", Dir: "/src", Module: mainMod, GoFiles: []string{"main.go"}},
			}})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tt.want)
			}
		})
	}
}

func TestBuildRejectsBadRoots(t *testing.T) {
	lib := golist.Package{ImportPath: "example.com/app/lib", Name: "lib", Dir: "/src/lib", Module: mainMod, GoFiles: []string{"lib.go"}}
	if _, err := Build(Input{Src: "/src", Env: testEnv, Packages: []golist.Package{lib}}); err == nil ||
		!strings.Contains(err.Error(), "example.com/app/lib: not a main package") {
		t.Fatalf("err = %v, want a not-a-main-package error", err)
	}

	if _, err := Build(Input{Src: "/src", Env: testEnv, Packages: []golist.Package{std("fmt")}}); err == nil ||
		!strings.Contains(err.Error(), "no main packages") {
		t.Fatalf("err = %v, want a no-main-packages error", err)
	}

	a := golist.Package{ImportPath: "example.com/app/a/tool", Name: "main", Dir: "/src/a/tool", Module: mainMod, GoFiles: []string{"main.go"}}
	b := golist.Package{ImportPath: "example.com/app/b/tool", Name: "main", Dir: "/src/b/tool", Module: mainMod, GoFiles: []string{"main.go"}}
	if _, err := Build(Input{Src: "/src", Env: testEnv, Packages: []golist.Package{a, b}}); err == nil ||
		!strings.Contains(err.Error(), `would both build the binary "tool"`) {
		t.Fatalf("err = %v, want a duplicate-binary error", err)
	}
}
```

- [ ] **Step 6: Run the tests to verify they fail**

Run: `nix develop --command go test ./internal/graph/`
Expected: FAIL, `undefined: Build`, `undefined: Input`, `undefined: LoadError`.

- [ ] **Step 7: Add `Build` to `graph.go`**

Replace the import block of `internal/graph/graph.go` with:

```go
import (
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"

	"github.com/draganm/gonixgo/internal/golist"
	"github.com/draganm/gonixgo/internal/modinfo"
	"github.com/draganm/gonixgo/internal/storepath"
)
```

and append:

```go
// Input is what Build needs from the evaluation-time go commands.
type Input struct {
	Packages []golist.Package  // go list -e -deps output for the main packages
	Src      string            // absolute path of the source root, symlinks resolved
	Env      map[string]string // go env: GOVERSION, GOOS, GOARCH, CGO_ENABLED and the GO<arch> keys
	Tags     []string          // build tags
	Sums     map[string]string // path@version to h1: sum, from go.sum
}

// LoadError lists everything that keeps a graph from being built.
type LoadError struct {
	Problems []string
}

func newLoadError(problems []string) *LoadError {
	sort.Strings(problems)
	return &LoadError{Problems: slices.Compact(problems)}
}

func (e *LoadError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d problem(s) loading packages:", len(e.Problems))
	hint := false
	for _, p := range e.Problems {
		b.WriteString("\n  " + p)
		hint = hint || strings.Contains(p, "go.sum")
	}
	if hint {
		b.WriteString("\nrun `go mod tidy` to update go.sum")
	}
	return b.String()
}

// Build constructs the graph. It reports every problem it finds, not just
// the first.
func Build(in Input) (*Graph, error) {
	var problems []string
	for i := range in.Packages {
		if p := &in.Packages[i]; p.Error != nil {
			msg := strings.TrimSpace(p.Error.Err)
			if p.Error.Pos != "" {
				msg = p.Error.Pos + ": " + msg
			}
			problems = append(problems, p.ImportPath+": "+msg)
		}
	}
	if len(problems) > 0 {
		return nil, newLoadError(problems)
	}

	byPath := make(map[string]*golist.Package, len(in.Packages))
	for i := range in.Packages {
		byPath[in.Packages[i].ImportPath] = &in.Packages[i]
	}

	g := &Graph{
		GoVersion:  strings.TrimPrefix(in.Env["GOVERSION"], "go"),
		GOOS:       in.Env["GOOS"],
		GOARCH:     in.Env["GOARCH"],
		CgoEnabled: in.Env["CGO_ENABLED"] == "1",
		Modules:    map[string]*Module{},
		Packages:   map[string]*Package{},
	}
	for i := range in.Packages {
		p := &in.Packages[i]
		if p.Standard {
			continue
		}
		pkg, mod, err := newPackage(p, in.Src, byPath)
		if err != nil {
			problems = append(problems, err.Error())
			continue
		}
		g.Packages[pkg.ImportPath] = pkg
		if mod != nil && g.Modules[mod.Key] == nil {
			mod.Sum = in.Sums[mod.Key]
			g.Modules[mod.Key] = mod
		}
	}
	if len(problems) > 0 {
		return nil, newLoadError(problems)
	}

	owner := map[string]string{} // binary name to main package
	for i := range in.Packages {
		p := &in.Packages[i]
		if p.Standard || p.DepOnly {
			continue
		}
		if p.Name != "main" {
			problems = append(problems, p.ImportPath+": not a main package")
			continue
		}
		bin := g.newBinary(p, in)
		if other, dup := owner[bin.Name]; dup {
			problems = append(problems, fmt.Sprintf("%s and %s would both build the binary %q", other, p.ImportPath, bin.Name))
			continue
		}
		owner[bin.Name] = p.ImportPath
		g.Bins = append(g.Bins, bin)
	}
	if len(problems) == 0 && len(g.Bins) == 0 {
		problems = append(problems, "no main packages matched subPackages")
	}
	if len(problems) > 0 {
		return nil, newLoadError(problems)
	}
	sort.Slice(g.Bins, func(i, j int) bool { return g.Bins[i].Name < g.Bins[j].Name })
	return g, nil
}

// newBinary describes the link of main package p.
func (g *Graph) newBinary(p *golist.Package, in Input) *Binary {
	deps := g.closure(p.ImportPath)

	var mods []modinfo.Module
	seen := map[string]bool{}
	for _, ip := range deps {
		key := g.Packages[ip].ModuleKey
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		m := g.Modules[key]
		mods = append(mods, modinfo.Module{Path: m.Path, Version: m.Version, Sum: m.Sum})
	}
	sort.Slice(mods, func(i, j int) bool { return mods[i].Path < mods[j].Path })

	name := execName(p.ImportPath)
	if g.GOOS == "windows" {
		name += ".exe"
	}
	info := modinfo.Info{
		Path:     p.ImportPath,
		Main:     modinfo.Module{Path: p.Module.Path, Version: "(devel)"},
		Deps:     mods,
		Settings: modinfo.Settings(in.Env, in.Tags, p.DefaultGODEBUG),
	}
	return &Binary{
		Name:    name,
		DrvName: storepath.SanitizeName("gobin-" + name),
		Main:    p.ImportPath,
		Deps:    deps,
		Modinfo: info.String(),
		Godebug: p.DefaultGODEBUG,
	}
}

// closure returns the non-standard packages root imports transitively,
// sorted, without root itself.
func (g *Graph) closure(root string) []string {
	seen := map[string]bool{root: true}
	queue := []string{root}
	var out []string
	for len(queue) > 0 {
		ip := queue[0]
		queue = queue[1:]
		for _, dep := range g.Packages[ip].Deps {
			if !seen[dep] {
				seen[dep] = true
				out = append(out, dep)
				queue = append(queue, dep)
			}
		}
	}
	sort.Strings(out)
	return out
}
```

- [ ] **Step 8: Run the tests to verify they pass**

Run: `nix develop --command go test ./internal/graph/ -v`
Expected: PASS for every test in both files.

- [ ] **Step 9: Commit**

```bash
git add internal/graph
git commit -m "feat(graph): build the module, package and binary graph" -m "$TRAILERS"
```

---

### Task 7: Module hashing, the hash cache and store pre-seeding

**Files:**
- Create: `internal/modcache/modcache.go`
- Test: `internal/modcache/modcache_test.go`

**Interfaces:**
- Consumes: `graph.Module` (Task 6), `nar.Hash`, `nar.SRI`, `nar.ParseSRI` (Task 2), `storepath.FixedOutput` (Task 3).
- Produces:
  - `func ParseGoSum(data []byte) map[string]string` — `path@version` to `h1:…`, module zip lines only.
  - `type Seeder struct { StoreDir, CacheDir string; Add func(name, dir string) (string, error); Warn func(format string, args ...any) }` — `CacheDir == ""` disables the cache, `Add == nil` disables pre-seeding, `Warn == nil` discards warnings.
  - `func (s *Seeder) Prepare(mods []*graph.Module) error` — fills `Hash` on every module.
  - `func NixStoreAdd(name, dir string) (string, error)` — runs `nix store add`, returns the store path.

- [ ] **Step 1: Write the failing test**

`internal/modcache/modcache_test.go`:

```go
package modcache

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/draganm/gonixgo/internal/graph"
	"github.com/draganm/gonixgo/internal/nar"
	"github.com/draganm/gonixgo/internal/storepath"
	"github.com/draganm/gonixgo/internal/testutil"
)

func module(t *testing.T, key string, files map[string]string) *graph.Module {
	t.Helper()
	path, version, _ := strings.Cut(key, "@")
	return &graph.Module{
		Key: key, Path: path, Version: version, Sum: "h1:" + key,
		Dir:  testutil.WriteTree(t, files),
		Name: storepath.SanitizeName("gomod-" + path + "-" + version),
	}
}

func sri(t *testing.T, dir string) string {
	t.Helper()
	sum, err := nar.Hash(dir)
	if err != nil {
		t.Fatal(err)
	}
	return nar.SRI(sum)
}

// fakeStore answers Add with the path Nix would give and records the call.
type fakeStore struct {
	mu    sync.Mutex
	dir   string
	added []string
}

func (s *fakeStore) add(name, dir string) (string, error) {
	sum, err := nar.Hash(dir)
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.added = append(s.added, name)
	return storepath.FixedOutput(s.dir, name, sum), nil
}

func TestParseGoSum(t *testing.T) {
	const goSum = `github.com/fatih/color v1.18.0 h1:S8gINlzdQ840/4pfAwic/ZE0djQEH3wM94VfqLTZcOM=
github.com/fatih/color v1.18.0/go.mod h1:4FelSpRwEGDpQ12mAdzqdOukCy4u8WUtOY6lkT/6HfU=

malformed line
golang.org/x/sys v0.25.0 h1:r+8e+loiHxRqhXVl6ML1nO3l1+oFoWbnlu2Ehimmi34=
`
	want := map[string]string{
		"github.com/fatih/color@v1.18.0": "h1:S8gINlzdQ840/4pfAwic/ZE0djQEH3wM94VfqLTZcOM=",
		"golang.org/x/sys@v0.25.0":       "h1:r+8e+loiHxRqhXVl6ML1nO3l1+oFoWbnlu2Ehimmi34=",
	}
	if got := ParseGoSum([]byte(goSum)); !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseGoSum = %v, want %v", got, want)
	}
}

func TestPrepareHashesAndSeeds(t *testing.T) {
	a := module(t, "example.com/a@v1.0.0", map[string]string{"a.go": "package a\n"})
	b := module(t, "example.com/b@v2.0.0", map[string]string{"b.go": "package b\n", "sub/c.go": "package sub\n"})
	store := &fakeStore{dir: t.TempDir()}
	s := &Seeder{StoreDir: store.dir, CacheDir: t.TempDir(), Add: store.add}

	if err := s.Prepare([]*graph.Module{a, b}); err != nil {
		t.Fatal(err)
	}
	if a.Hash != sri(t, a.Dir) || b.Hash != sri(t, b.Dir) {
		t.Fatalf("hashes = %q, %q", a.Hash, b.Hash)
	}
	sort.Strings(store.added)
	if want := []string{"gomod-example.com-a-v1.0.0", "gomod-example.com-b-v2.0.0"}; !reflect.DeepEqual(store.added, want) {
		t.Fatalf("added = %v, want %v", store.added, want)
	}
}

func TestPrepareNoModules(t *testing.T) {
	if err := (&Seeder{StoreDir: t.TempDir()}).Prepare(nil); err != nil {
		t.Fatal(err)
	}
}

func TestPrepareSkipsPathsAlreadyInStore(t *testing.T) {
	m := module(t, "example.com/a@v1.0.0", map[string]string{"a.go": "package a\n"})
	store := &fakeStore{dir: t.TempDir()}
	sum, err := nar.Hash(m.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(storepath.FixedOutput(store.dir, m.Name, sum), 0o755); err != nil {
		t.Fatal(err)
	}
	s := &Seeder{StoreDir: store.dir, Add: store.add}
	if err := s.Prepare([]*graph.Module{m}); err != nil {
		t.Fatal(err)
	}
	if len(store.added) != 0 {
		t.Fatalf("added = %v, want nothing", store.added)
	}
}

func TestPrepareUsesCache(t *testing.T) {
	m := module(t, "example.com/a@v1.0.0", map[string]string{"a.go": "package a\n"})
	s := &Seeder{StoreDir: t.TempDir(), CacheDir: t.TempDir()}
	if err := s.Prepare([]*graph.Module{m}); err != nil {
		t.Fatal(err)
	}
	first := m.Hash

	// Without pre-seeding nothing checks the directory again, so a changed
	// directory still yields the cached hash.
	if err := os.WriteFile(filepath.Join(m.Dir, "new.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m.Hash = ""
	if err := s.Prepare([]*graph.Module{m}); err != nil {
		t.Fatal(err)
	}
	if m.Hash != first {
		t.Fatalf("hash = %s, want the cached %s", m.Hash, first)
	}
}

func TestPrepareRecoversFromStaleCache(t *testing.T) {
	m := module(t, "example.com/a@v1.0.0", map[string]string{"a.go": "package a\n"})
	cache := t.TempDir()
	if err := (&Seeder{StoreDir: t.TempDir(), CacheDir: cache}).Prepare([]*graph.Module{m}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(m.Dir, "new.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	store := &fakeStore{dir: t.TempDir()}
	if err := (&Seeder{StoreDir: store.dir, CacheDir: cache, Add: store.add}).Prepare([]*graph.Module{m}); err != nil {
		t.Fatal(err)
	}
	want := sri(t, m.Dir)
	if m.Hash != want {
		t.Fatalf("hash = %s, want the directory's current %s", m.Hash, want)
	}

	// The cache entry was corrected.
	m.Hash = ""
	if err := (&Seeder{StoreDir: t.TempDir(), CacheDir: cache}).Prepare([]*graph.Module{m}); err != nil {
		t.Fatal(err)
	}
	if m.Hash != want {
		t.Fatalf("cached hash = %s, want %s", m.Hash, want)
	}
}

func TestPrepareWithoutSumDoesNotCache(t *testing.T) {
	m := module(t, "example.com/a@v1.0.0", map[string]string{"a.go": "package a\n"})
	m.Sum = ""
	cache := t.TempDir()
	if err := (&Seeder{StoreDir: t.TempDir(), CacheDir: cache}).Prepare([]*graph.Module{m}); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(cache)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 || m.Hash == "" {
		t.Fatalf("cache has %d entries, hash = %q", len(entries), m.Hash)
	}
}

func TestPrepareWarnsWhenAddFails(t *testing.T) {
	m := module(t, "example.com/a@v1.0.0", map[string]string{"a.go": "package a\n"})
	var warnings []string
	s := &Seeder{
		StoreDir: t.TempDir(),
		Add:      func(string, string) (string, error) { return "", errors.New("daemon unreachable") },
		Warn:     func(format string, args ...any) { warnings = append(warnings, format) },
	}
	if err := s.Prepare([]*graph.Module{m}); err != nil {
		t.Fatalf("Prepare failed, want a warning: %v", err)
	}
	if len(warnings) != 1 || m.Hash != sri(t, m.Dir) {
		t.Fatalf("warnings = %v, hash = %q", warnings, m.Hash)
	}
}

func TestPrepareFailsOnMismatch(t *testing.T) {
	m := module(t, "example.com/a@v1.0.0", map[string]string{"a.go": "package a\n"})
	s := &Seeder{StoreDir: t.TempDir(), Add: func(string, string) (string, error) { return "/elsewhere/x", nil }}
	err := s.Prepare([]*graph.Module{m})
	if err == nil || !strings.Contains(err.Error(), "example.com/a@v1.0.0") || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("err = %v, want a mismatch naming the module", err)
	}
}

func TestPrepareMissingDir(t *testing.T) {
	m := &graph.Module{Key: "example.com/a@v1.0.0", Dir: filepath.Join(t.TempDir(), "absent"), Name: "gomod-a"}
	if err := (&Seeder{StoreDir: t.TempDir()}).Prepare([]*graph.Module{m}); err == nil {
		t.Fatal("Prepare of a missing directory succeeded")
	}
}

func TestNixStoreAdd(t *testing.T) {
	if _, err := exec.LookPath("nix"); err != nil {
		t.Skip("nix not on PATH")
	}
	dir := testutil.WriteTree(t, map[string]string{"go.mod": "module example.com/seedtest\n", "a.go": "package a\n"})
	const name = "gomod-example.com-seedtest-v0.0.1"
	got, err := NixStoreAdd(name, dir)
	if err != nil {
		t.Fatal(err)
	}
	sum, err := nar.Hash(dir)
	if err != nil {
		t.Fatal(err)
	}
	if want := storepath.FixedOutput(filepath.Dir(got), name, sum); got != want {
		t.Fatalf("nix store add gave %s, FixedOutput computes %s", got, want)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `nix develop --command go test ./internal/modcache/`
Expected: FAIL, `undefined: ParseGoSum`, `undefined: Seeder`, `undefined: NixStoreAdd`.

- [ ] **Step 3: Write the implementation**

`internal/modcache/modcache.go`:

```go
// Package modcache hashes module directories from the Go module cache and
// pre-seeds them into the Nix store, so the fetch derivations for them are
// already built.
package modcache

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/draganm/gonixgo/internal/graph"
	"github.com/draganm/gonixgo/internal/nar"
	"github.com/draganm/gonixgo/internal/storepath"
)

// ParseGoSum returns the h1: sum of each module zip in a go.sum file, keyed
// by path@version. The /go.mod lines are skipped.
func ParseGoSum(data []byte) map[string]string {
	sums := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Fields(line)
		if len(f) != 3 || strings.HasSuffix(f[1], "/go.mod") {
			continue
		}
		sums[f[0]+"@"+f[1]] = f[2]
	}
	return sums
}

// Seeder computes module hashes and adds module directories to the store.
type Seeder struct {
	StoreDir string
	// CacheDir holds computed hashes. "" disables the cache.
	CacheDir string
	// Add puts dir in the store under name and returns its store path.
	// nil disables pre-seeding.
	Add func(name, dir string) (string, error)
	// Warn reports problems that do not stop evaluation. nil discards them.
	Warn func(format string, args ...any)
}

// Prepare fills in the Hash of every module and, when pre-seeding is on,
// makes sure each module's store path exists.
func (s *Seeder) Prepare(mods []*graph.Module) error {
	jobs := make(chan *graph.Module)
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		problems []string
	)
	for range min(runtime.NumCPU(), len(mods)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for m := range jobs {
				if err := s.prepare(m); err != nil {
					mu.Lock()
					problems = append(problems, fmt.Sprintf("module %s: %v", m.Key, err))
					mu.Unlock()
				}
			}
		}()
	}
	for _, m := range mods {
		jobs <- m
	}
	close(jobs)
	wg.Wait()
	if len(problems) == 0 {
		return nil
	}
	sort.Strings(problems)
	return errors.New(strings.Join(problems, "\n"))
}

func (s *Seeder) prepare(m *graph.Module) error {
	sum, cached, err := s.hash(m)
	if err != nil {
		return err
	}
	path := storepath.FixedOutput(s.StoreDir, m.Name, sum)
	if _, statErr := os.Lstat(path); s.Add != nil && statErr != nil {
		added, err := s.Add(m.Name, m.Dir)
		switch {
		case err != nil:
			s.warn("gonixgo: could not add %s to the store, it will be fetched at build time: %v", m.Key, err)
		case added != path && cached:
			// The cached hash no longer describes the directory.
			if sum, err = nar.Hash(m.Dir); err != nil {
				return err
			}
			if storepath.FixedOutput(s.StoreDir, m.Name, sum) != added {
				return fmt.Errorf("store path %s does not match the hash of %s", added, m.Dir)
			}
			s.remember(m, sum)
		case added != path:
			return fmt.Errorf("store path %s does not match the hash of %s; the module cache changed while it was read", added, m.Dir)
		}
	}
	m.Hash = nar.SRI(sum)
	return nil
}

// hash returns the NAR hash of the module's directory and whether it came
// from the cache.
func (s *Seeder) hash(m *graph.Module) (sum [32]byte, cached bool, err error) {
	if file := s.cacheFile(m); file != "" {
		if data, readErr := os.ReadFile(file); readErr == nil {
			if sum, parseErr := nar.ParseSRI(strings.TrimSpace(string(data))); parseErr == nil {
				return sum, true, nil
			}
		}
	}
	if sum, err = nar.Hash(m.Dir); err != nil {
		return sum, false, err
	}
	s.remember(m, sum)
	return sum, false, nil
}

// cacheFile names the cache entry for m. A module without a go.sum line is
// not cached: nothing ties its content to its version.
func (s *Seeder) cacheFile(m *graph.Module) string {
	if s.CacheDir == "" || m.Sum == "" {
		return ""
	}
	key := sha256.Sum256([]byte("v1\n" + m.Key + "\n" + m.Sum + "\n"))
	return filepath.Join(s.CacheDir, hex.EncodeToString(key[:]))
}

// remember stores sum in the cache. Failures are ignored: the cache only
// saves time.
func (s *Seeder) remember(m *graph.Module, sum [32]byte) {
	file := s.cacheFile(m)
	if file == "" {
		return
	}
	if err := os.MkdirAll(s.CacheDir, 0o755); err != nil {
		return
	}
	tmp, err := os.CreateTemp(s.CacheDir, "tmp-*")
	if err != nil {
		return
	}
	_, writeErr := tmp.WriteString(nar.SRI(sum) + "\n")
	closeErr := tmp.Close()
	if writeErr != nil || closeErr != nil || os.Rename(tmp.Name(), file) != nil {
		os.Remove(tmp.Name())
	}
}

func (s *Seeder) warn(format string, args ...any) {
	if s.Warn != nil {
		s.Warn(format, args...)
	}
}

// NixStoreAdd adds dir to the Nix store as a recursive SHA-256 path named
// name and returns the store path.
func NixStoreAdd(name, dir string) (string, error) {
	cmd := exec.Command("nix", "--extra-experimental-features", "nix-command",
		"store", "add", "--name", name, "--mode", "nar", "--hash-algo", "sha256", dir)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("nix store add: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(string(out)), nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `nix develop --command go test ./internal/modcache/ -v`
Expected: PASS for all eleven tests (`TestNixStoreAdd` runs, since `nix` is on `PATH`; it adds one small path to the store).

- [ ] **Step 5: Commit**

```bash
git add internal/modcache
git commit -m "feat(modcache): hash modules and pre-seed them into the store" -m "$TRAILERS"
```

---

### Task 8: Emitting the graph as Nix

**Files:**
- Create: `internal/emit/emit.go`
- Test: `internal/emit/emit_test.go`

**Interfaces:**
- Consumes: `graph.Graph`, `graph.Module`, `graph.Package`, `graph.Binary` (Task 6).
- Produces: `func Nix(w io.Writer, g *graph.Graph) error`. The output is a function `b: rec { goVersion; cgoEnabled; modules; packages; bins; }` calling:
  - `b.fetchModule { name; path; version; hash; }`
  - `b.localDir { name; files; }` — `files` relative to the source root
  - `b.compile { name; importPath; src; subdir; module; trimTo; lang; isMain; goFiles; sFiles; embed; deps; }`
  - `b.link { name; binName; main; deps; modinfo; godebug; }`

- [ ] **Step 1: Write the failing test**

`internal/emit/emit_test.go`:

```go
package emit

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/draganm/gonixgo/internal/graph"
)

func testGraph() *graph.Graph {
	return &graph.Graph{
		GoVersion: "1.26.8", GOOS: "darwin", GOARCH: "arm64", CgoEnabled: true,
		Modules: map[string]*graph.Module{
			"github.com/fatih/color@v1.18.0": {
				Key: "github.com/fatih/color@v1.18.0", Path: "github.com/fatih/color", Version: "v1.18.0",
				Name: "gomod-github.com-fatih-color-v1.18.0", Hash: "sha256-AAAA",
			},
		},
		Packages: map[string]*graph.Package{
			"example.com/app": {
				ImportPath: "example.com/app", Name: "golocal-example.com-app", SrcName: "gosrc-example.com-app",
				Local: true, IsMain: true, ModulePath: "example.com/app", TrimTo: "example.com/app", Lang: "go1.24",
				GoFiles:  []string{"main.go"},
				SrcFiles: []string{"main.go", "static/a b.txt"},
				Embed:    map[string][]string{"static/*": {"static/a b.txt"}},
				Deps:     []string{"github.com/fatih/color"},
			},
			"github.com/fatih/color": {
				ImportPath: "github.com/fatih/color", Name: "gopkg-github.com-fatih-color-v1.18.0",
				ModuleKey: "github.com/fatih/color@v1.18.0", ModulePath: "github.com/fatih/color",
				TrimTo: "github.com/fatih/color@v1.18.0", Lang: "go1.17",
				GoFiles: []string{"color.go", "doc.go"},
			},
		},
		Bins: []*graph.Binary{{
			Name: "app", DrvName: "gobin-app", Main: "example.com/app",
			Deps:    []string{"github.com/fatih/color"},
			Modinfo: "path\texample.com/app\n", Godebug: "x=1",
		}},
	}
}

const golden = `b: rec {
  goVersion = "1.26.8";
  cgoEnabled = true;
  modules = {
    "github.com/fatih/color@v1.18.0" = b.fetchModule {
      name = "gomod-github.com-fatih-color-v1.18.0";
      path = "github.com/fatih/color";
      version = "v1.18.0";
      hash = "sha256-AAAA";
    };
  };
  packages = {
    "example.com/app" = b.compile {
      name = "golocal-example.com-app";
      importPath = "example.com/app";
      src = b.localDir { name = "gosrc-example.com-app"; files = [ "main.go" "static/a b.txt" ]; };
      subdir = "";
      module = "example.com/app";
      trimTo = "example.com/app";
      lang = "go1.24";
      isMain = true;
      goFiles = [ "main.go" ];
      sFiles = [ ];
      embed = { "static/*" = [ "static/a b.txt" ]; };
      deps = [ packages."github.com/fatih/color" ];
    };
    "github.com/fatih/color" = b.compile {
      name = "gopkg-github.com-fatih-color-v1.18.0";
      importPath = "github.com/fatih/color";
      src = modules."github.com/fatih/color@v1.18.0";
      subdir = "";
      module = "github.com/fatih/color";
      trimTo = "github.com/fatih/color@v1.18.0";
      lang = "go1.17";
      isMain = false;
      goFiles = [ "color.go" "doc.go" ];
      sFiles = [ ];
      embed = { };
      deps = [ ];
    };
  };
  bins = {
    "app" = b.link {
      name = "gobin-app";
      binName = "app";
      main = packages."example.com/app";
      deps = [ packages."github.com/fatih/color" ];
      modinfo = "path\texample.com/app\n";
      godebug = "x=1";
    };
  };
}
`

func TestNixGolden(t *testing.T) {
	var buf bytes.Buffer
	if err := Nix(&buf, testGraph()); err != nil {
		t.Fatal(err)
	}
	if got := buf.String(); got != golden {
		t.Fatalf("Nix() =\n%s\nwant\n%s", got, golden)
	}
}

func TestNixEmptyGraphSections(t *testing.T) {
	g := testGraph()
	g.Modules = map[string]*graph.Module{}
	var buf bytes.Buffer
	if err := Nix(&buf, g); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(buf.Bytes(), []byte("  modules = {\n  };\n")) {
		t.Fatalf("empty modules set not emitted:\n%s", buf.String())
	}
}

func TestQuote(t *testing.T) {
	tests := []struct{ in, want string }{
		{`plain`, `"plain"`},
		{`with "quotes"`, `"with \"quotes\""`},
		{`back\slash`, `"back\\slash"`},
		{`${interp}`, `"\${interp}"`},
		{`$${x}`, `"$\${x}"`},
		{`cost $5`, `"cost $5"`},
		{"tab\tline\nreturn\r", `"tab\tline\nreturn\r"`},
	}
	for _, tt := range tests {
		if got := quote(tt.in); got != tt.want {
			t.Errorf("quote(%q) = %s, want %s", tt.in, got, tt.want)
		}
	}
}

func TestQuoteRoundTripsThroughNix(t *testing.T) {
	nix, err := exec.LookPath("nix-instantiate")
	if err != nil {
		t.Skip("nix-instantiate not on PATH")
	}
	for _, s := range []string{
		`plain`, `sp ace`, `with "quotes"`, `back\slash`, `${interp}`, `$${x}`, `''two quotes''`,
		"tab\tline\nreturn\r", `päth/ünïcode`, `static/a b ${c}.txt`,
	} {
		out, err := exec.Command(nix, "--eval", "--strict", "--json", "-E", quote(s)).Output()
		if err != nil {
			t.Errorf("nix-instantiate rejected %s: %v", quote(s), err)
			continue
		}
		var got string
		if err := json.Unmarshal(out, &got); err != nil {
			t.Errorf("decoding %s: %v", out, err)
			continue
		}
		if got != s {
			t.Errorf("%q came back from Nix as %q", s, got)
		}
	}
}

func TestNixOutputEvaluates(t *testing.T) {
	nix, err := exec.LookPath("nix-instantiate")
	if err != nil {
		t.Skip("nix-instantiate not on PATH")
	}
	file := filepath.Join(t.TempDir(), "graph.nix")
	var buf bytes.Buffer
	if err := Nix(&buf, testGraph()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	// Builders that return the node's name show the wiring evaluates.
	expr := fmt.Sprintf("import %s { fetchModule = a: a.name; localDir = a: a.name; compile = a: a.name; link = a: a.name; }", file)
	out, err := exec.Command(nix, "--eval", "--strict", "--json", "-E", expr).Output()
	if err != nil {
		t.Fatalf("nix-instantiate: %v", err)
	}
	var got struct {
		GoVersion string            `json:"goVersion"`
		Packages  map[string]string `json:"packages"`
		Bins      map[string]string `json:"bins"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if got.GoVersion != "1.26.8" || got.Packages["example.com/app"] != "golocal-example.com-app" || got.Bins["app"] != "gobin-app" {
		t.Fatalf("evaluated to %+v", got)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `nix develop --command go test ./internal/emit/`
Expected: FAIL, `undefined: Nix`, `undefined: quote`.

- [ ] **Step 3: Write the implementation**

`internal/emit/emit.go`:

```go
// Package emit prints a graph as the Nix function gonixgo's builders
// consume.
package emit

import (
	"fmt"
	"io"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/draganm/gonixgo/internal/graph"
)

// Nix writes g as a function from the builder set to the module, package
// and binary derivations. Output is deterministic: every set is sorted.
func Nix(w io.Writer, g *graph.Graph) error {
	var b strings.Builder
	b.WriteString("b: rec {\n")
	attr(&b, 1, "goVersion", quote(g.GoVersion))
	attr(&b, 1, "cgoEnabled", strconv.FormatBool(g.CgoEnabled))

	b.WriteString("  modules = {\n")
	for _, m := range g.ModuleList() {
		fmt.Fprintf(&b, "    %s = b.fetchModule {\n", quote(m.Key))
		attr(&b, 3, "name", quote(m.Name))
		attr(&b, 3, "path", quote(m.Path))
		attr(&b, 3, "version", quote(m.Version))
		attr(&b, 3, "hash", quote(m.Hash))
		b.WriteString("    };\n")
	}
	b.WriteString("  };\n")

	b.WriteString("  packages = {\n")
	for _, importPath := range slices.Sorted(maps.Keys(g.Packages)) {
		p := g.Packages[importPath]
		fmt.Fprintf(&b, "    %s = b.compile {\n", quote(importPath))
		attr(&b, 3, "name", quote(p.Name))
		attr(&b, 3, "importPath", quote(p.ImportPath))
		if p.Local {
			attr(&b, 3, "src", fmt.Sprintf("b.localDir { name = %s; files = %s; }", quote(p.SrcName), list(p.SrcFiles)))
		} else {
			attr(&b, 3, "src", "modules."+quote(p.ModuleKey))
		}
		attr(&b, 3, "subdir", quote(p.Subdir))
		attr(&b, 3, "module", quote(p.ModulePath))
		attr(&b, 3, "trimTo", quote(p.TrimTo))
		attr(&b, 3, "lang", quote(p.Lang))
		attr(&b, 3, "isMain", strconv.FormatBool(p.IsMain))
		attr(&b, 3, "goFiles", list(p.GoFiles))
		attr(&b, 3, "sFiles", list(p.SFiles))
		attr(&b, 3, "embed", embed(p.Embed))
		attr(&b, 3, "deps", refs(p.Deps))
		b.WriteString("    };\n")
	}
	b.WriteString("  };\n")

	b.WriteString("  bins = {\n")
	for _, bin := range g.Bins {
		fmt.Fprintf(&b, "    %s = b.link {\n", quote(bin.Name))
		attr(&b, 3, "name", quote(bin.DrvName))
		attr(&b, 3, "binName", quote(bin.Name))
		attr(&b, 3, "main", "packages."+quote(bin.Main))
		attr(&b, 3, "deps", refs(bin.Deps))
		attr(&b, 3, "modinfo", quote(bin.Modinfo))
		attr(&b, 3, "godebug", quote(bin.Godebug))
		b.WriteString("    };\n")
	}
	b.WriteString("  };\n")

	b.WriteString("}\n")
	_, err := io.WriteString(w, b.String())
	return err
}

func attr(b *strings.Builder, depth int, name, value string) {
	fmt.Fprintf(b, "%s%s = %s;\n", strings.Repeat("  ", depth), name, value)
}

// list formats items as a Nix list of strings.
func list(items []string) string {
	if len(items) == 0 {
		return "[ ]"
	}
	quoted := make([]string, len(items))
	for i, item := range items {
		quoted[i] = quote(item)
	}
	return "[ " + strings.Join(quoted, " ") + " ]"
}

// refs formats import paths as a Nix list of references into packages.
func refs(importPaths []string) string {
	if len(importPaths) == 0 {
		return "[ ]"
	}
	out := make([]string, len(importPaths))
	for i, ip := range importPaths {
		out[i] = "packages." + quote(ip)
	}
	return "[ " + strings.Join(out, " ") + " ]"
}

// embed formats the pattern-to-files map as a Nix attribute set.
func embed(m map[string][]string) string {
	if len(m) == 0 {
		return "{ }"
	}
	var b strings.Builder
	b.WriteString("{")
	for _, pattern := range slices.Sorted(maps.Keys(m)) {
		fmt.Fprintf(&b, " %s = %s;", quote(pattern), list(m[pattern]))
	}
	b.WriteString(" }")
	return b.String()
}

// quote formats s as a Nix double-quoted string. It serves for attribute
// names too.
func quote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		switch c := s[i]; c {
		case '\\', '"':
			b.WriteByte('\\')
			b.WriteByte(c)
		case '$':
			// Only "${" starts an interpolation.
			if i+1 < len(s) && s[i+1] == '{' {
				b.WriteByte('\\')
			}
			b.WriteByte('$')
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			b.WriteByte(c)
		}
	}
	b.WriteByte('"')
	return b.String()
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `nix develop --command go test ./internal/emit/ -v`
Expected: PASS for all five tests.

- [ ] **Step 5: Commit**

```bash
git add internal/emit
git commit -m "feat(emit): print the graph as a Nix function" -m "$TRAILERS"
```

---

### Task 9: The `resolve` subcommand

**Files:**
- Create: `internal/resolve/resolve.go`
- Create: `cmd/gonixgo/resolve.go`
- Test: `internal/resolve/resolve_test.go`

**Interfaces:**
- Consumes: `golist.Options`, `golist.List`, `golist.Env` (Task 4); `graph.Build`, `graph.Input`, `(*graph.Graph).ModuleList` (Task 6); `modcache.ParseGoSum`, `modcache.Seeder`, `modcache.NixStoreAdd` (Task 7); `emit.Nix` (Task 8); `commands` (Task 1).
- Produces:
  - `type Args struct` with JSON fields `go`, `src`, `storeDir`, `modRoot`, `subPackages`, `tags`, `goos`, `goarch`, `cgoEnabled` (`*bool`, `null` for Go's default), `doCheck` — the single argument Nix passes.
  - `type Options struct { Stderr io.Writer; CacheDir string; Add func(name, dir string) (string, error) }`
  - `func Run(a Args, opts Options, stdout io.Writer) error`
  - The command `gonixgo resolve '<json>'`, printing only the Nix expression on stdout.

- [ ] **Step 1: Write the failing test**

`internal/resolve/resolve_test.go`:

```go
package resolve

import (
	"bytes"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/draganm/gonixgo/internal/graph"
	"github.com/draganm/gonixgo/internal/testutil"
)

var appFiles = map[string]string{
	"go.mod": "module example.com/app\n\ngo 1.21\n",
	"main.go": `package main

import (
	"fmt"

	"example.com/app/internal/greet"
)

func main() { fmt.Println(greet.Hello()) }
`,
	"internal/greet/greet.go": "package greet\n\nfunc Hello() string { return \"hello\" }\n",
	"cmd/second/main.go":      "package main\n\nfunc main() {}\n",
}

func run(t *testing.T, a Args) (string, error) {
	t.Helper()
	a.Go = testutil.Go(t)
	a.StoreDir = "/nix/store"
	var out bytes.Buffer
	err := Run(a, Options{Stderr: io.Discard, CacheDir: t.TempDir()}, &out)
	return out.String(), err
}

func TestRunLocalOnly(t *testing.T) {
	out, err := run(t, Args{Src: testutil.WriteTree(t, appFiles), ModRoot: ".", SubPackages: []string{"."}})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"b: rec {\n",
		"  modules = {\n  };\n",
		`    "example.com/app" = b.compile {`,
		`      src = b.localDir { name = "gosrc-example.com-app"; files = [ "main.go" ]; };`,
		`    "example.com/app/internal/greet" = b.compile {`,
		`      deps = [ packages."example.com/app/internal/greet" ];`,
		`    "app" = b.link {`,
		`      lang = "go1.21";`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "cmd/second") {
		t.Errorf("output includes a package outside subPackages:\n%s", out)
	}
}

func TestRunSeveralSubPackages(t *testing.T) {
	out, err := run(t, Args{Src: testutil.WriteTree(t, appFiles), ModRoot: ".", SubPackages: []string{".", "cmd/second"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"app" = b.link {`, `"second" = b.link {`} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

func TestRunModRoot(t *testing.T) {
	files := map[string]string{}
	for name, content := range appFiles {
		files["services/api/"+name] = content
	}
	out, err := run(t, Args{Src: testutil.WriteTree(t, files), ModRoot: "services/api", SubPackages: []string{"."}})
	if err != nil {
		t.Fatal(err)
	}
	if want := `files = [ "services/api/internal/greet/greet.go" ]`; !strings.Contains(out, want) {
		t.Errorf("output lacks %q:\n%s", want, out)
	}
	if want := `subdir = "services/api/internal/greet";`; !strings.Contains(out, want) {
		t.Errorf("output lacks %q:\n%s", want, out)
	}
}

func TestRunReportsLoadErrors(t *testing.T) {
	files := map[string]string{
		"go.mod":  "module example.com/app\n\ngo 1.21\n",
		"main.go": "package main\n\nimport _ \"example.com/app/missing\"\n\nfunc main() {}\n",
	}
	out, err := run(t, Args{Src: testutil.WriteTree(t, files), ModRoot: ".", SubPackages: []string{"."}})
	var loadErr *graph.LoadError
	if !errors.As(err, &loadErr) {
		t.Fatalf("err = %v, want a *graph.LoadError", err)
	}
	if out != "" {
		t.Fatalf("stdout = %q, want nothing on failure", out)
	}
}

func TestRunCgoDisabled(t *testing.T) {
	off := false
	out, err := run(t, Args{Src: testutil.WriteTree(t, appFiles), ModRoot: ".", SubPackages: []string{"."}, CgoEnabled: &off})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "cgoEnabled = false;") || !strings.Contains(out, `build\tCGO_ENABLED=0\n`) {
		t.Fatalf("cgo setting not reflected:\n%s", out)
	}
}

func TestPatterns(t *testing.T) {
	tests := []struct {
		in   []string
		want []string
	}{
		{nil, []string{"."}},
		{[]string{"."}, []string{"."}},
		{[]string{"cmd/app", "./cmd/tool", "cmd/x/"}, []string{"./cmd/app", "./cmd/tool", "./cmd/x"}},
	}
	for _, tt := range tests {
		if got := patterns(tt.in); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("patterns(%v) = %v, want %v", tt.in, got, tt.want)
		}
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `nix develop --command go test ./internal/resolve/`
Expected: FAIL, `undefined: Run`, `undefined: Args`, `undefined: Options`, `undefined: patterns`.

- [ ] **Step 3: Write the implementation**

`internal/resolve/resolve.go`:

```go
// Package resolve is the evaluation-time pipeline: go list, graph, module
// hashes, Nix.
package resolve

import (
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"

	"github.com/draganm/gonixgo/internal/emit"
	"github.com/draganm/gonixgo/internal/golist"
	"github.com/draganm/gonixgo/internal/graph"
	"github.com/draganm/gonixgo/internal/modcache"
)

// Args is the JSON argument buildGoApplication passes.
type Args struct {
	Go          string   `json:"go"`
	Src         string   `json:"src"`
	StoreDir    string   `json:"storeDir"`
	ModRoot     string   `json:"modRoot"`
	SubPackages []string `json:"subPackages"`
	Tags        []string `json:"tags"`
	GOOS        string   `json:"goos"`
	GOARCH      string   `json:"goarch"`
	CgoEnabled  *bool    `json:"cgoEnabled"` // nil: Go's default for the target
	DoCheck     bool     `json:"doCheck"`    // accepted, unused until tests are supported
}

// Options are the parts of the environment Run depends on.
type Options struct {
	Stderr   io.Writer
	CacheDir string                                 // module hash cache, "" for none
	Add      func(name, dir string) (string, error) // store pre-seeding, nil for none
}

// envKeys are the go env values the graph and the module info need.
var envKeys = []string{
	"GOVERSION", "GOOS", "GOARCH", "CGO_ENABLED",
	"GO386", "GOAMD64", "GOARM", "GOARM64", "GOMIPS", "GOMIPS64", "GOPPC64", "GORISCV64", "GOWASM",
}

// Run resolves the package graph of a.Src and writes it to stdout as Nix.
// Nothing is written when it fails.
func Run(a Args, opts Options, stdout io.Writer) error {
	// go reports directories with symlinks resolved; compare like with like.
	src, err := filepath.EvalSymlinks(a.Src)
	if err != nil {
		return err
	}
	o := golist.Options{
		Go:     a.Go,
		Dir:    filepath.Join(src, filepath.FromSlash(a.ModRoot)),
		GOOS:   a.GOOS,
		GOARCH: a.GOARCH,
		Tags:   a.Tags,
		Stderr: opts.Stderr,
	}
	if a.CgoEnabled != nil {
		o.CgoEnabled = "0"
		if *a.CgoEnabled {
			o.CgoEnabled = "1"
		}
	}

	env, err := golist.Env(o, envKeys...)
	if err != nil {
		return err
	}
	pkgs, err := golist.List(o, patterns(a.SubPackages)...)
	if err != nil {
		return err
	}
	sums := map[string]string{}
	if data, err := os.ReadFile(filepath.Join(o.Dir, "go.sum")); err == nil {
		sums = modcache.ParseGoSum(data)
	}

	g, err := graph.Build(graph.Input{Packages: pkgs, Src: src, Env: env, Tags: a.Tags, Sums: sums})
	if err != nil {
		return err
	}

	seeder := modcache.Seeder{
		StoreDir: a.StoreDir,
		CacheDir: opts.CacheDir,
		Add:      opts.Add,
		Warn: func(format string, args ...any) {
			fmt.Fprintf(opts.Stderr, format+"\n", args...)
		},
	}
	if err := seeder.Prepare(g.ModuleList()); err != nil {
		return err
	}
	return emit.Nix(stdout, g)
}

// patterns turns subPackages into go list patterns relative to the module
// root: "." stays, anything else gets a "./" prefix.
func patterns(subPackages []string) []string {
	if len(subPackages) == 0 {
		return []string{"."}
	}
	out := make([]string, len(subPackages))
	for i, sp := range subPackages {
		sp = path.Clean(sp)
		if sp != "." {
			sp = "./" + sp
		}
		out[i] = sp
	}
	return out
}
```

`cmd/gonixgo/resolve.go`:

```go
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/draganm/gonixgo/internal/modcache"
	"github.com/draganm/gonixgo/internal/resolve"
)

func init() { commands["resolve"] = resolveCmd }

// resolveCmd runs at evaluation time under builtins.exec. Its stdout is
// parsed as a Nix expression, so everything else goes to stderr.
func resolveCmd(args []string, stdout io.Writer) error {
	if len(args) != 1 {
		return errors.New("usage: gonixgo resolve <json>")
	}
	var a resolve.Args
	if err := json.Unmarshal([]byte(args[0]), &a); err != nil {
		return fmt.Errorf("parsing arguments: %w", err)
	}
	opts := resolve.Options{Stderr: os.Stderr}
	if dir, err := os.UserCacheDir(); err == nil {
		opts.CacheDir = filepath.Join(dir, "gonixgo", "narhash")
	}
	if _, err := exec.LookPath("nix"); err == nil {
		opts.Add = modcache.NixStoreAdd
	} else {
		fmt.Fprintln(os.Stderr, "gonixgo: nix is not on PATH; modules will be fetched at build time")
	}
	return resolve.Run(a, opts, stdout)
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `nix develop --command go test ./internal/resolve/ ./cmd/gonixgo/ -v`
Expected: PASS for all six resolve tests and the three dispatcher tests.

- [ ] **Step 5: Check the command by hand on this repository**

```bash
nix develop --command go run ./cmd/gonixgo resolve \
  '{"go":"go","src":"'"$PWD"'","storeDir":"/nix/store","modRoot":".","subPackages":["cmd/gonixgo"],"cgoEnabled":null}'
```

Expected: a Nix expression starting `b: rec {`, with an empty `modules` set, a `packages` entry for every `internal/…` package and for `github.com/draganm/gonixgo/cmd/gonixgo`, and one `"gonixgo" = b.link {` entry. `go run` leaves no binary behind.

- [ ] **Step 6: Commit**

```bash
git add internal/resolve cmd/gonixgo/resolve.go
git commit -m "feat(resolve): add the evaluation-time resolve command" -m "$TRAILERS"
```

---

### Task 10: The toolchain wrapper and the `compile` subcommand

**Files:**
- Create: `internal/gotool/gotool.go`
- Create: `internal/compile/compile.go`
- Create: `cmd/gonixgo/manifest.go`, `cmd/gonixgo/compile.go`
- Test: `internal/gotool/gotool_test.go`, `internal/compile/compile_test.go`, `cmd/gonixgo/manifest_test.go`

**Interfaces:**
- Consumes: `testutil.Go`, `testutil.WriteTree`, `testutil.StdImportcfg` (Task 4); `commands` (Task 1).
- Produces, in `internal/gotool`:
  - `type Toolchain struct { GOOS, GOARCH, GOROOT, ToolDir string }` plus unexported state.
  - `func New(goBin, goos, goarch, workDir string) (*Toolchain, error)` — `goos`/`goarch` may be `""` for the host.
  - `func (t *Toolchain) Tool(dir string, extraEnv []string, name string, args ...string) error` — runs `$GOTOOLDIR/<name>` in `dir`.
  - `func (t *Toolchain) PIE() bool`, `func (t *Toolchain) Shared() bool`, `func (t *Toolchain) AsmDefines() []string`
  - `func ConcatFiles(dst string, srcs []string) error`
- Produces, in `internal/compile`:
  - `type Manifest struct` with JSON fields `go`, `goos`, `goarch`, `importPath`, `isMain`, `srcDir`, `trimTo`, `lang`, `goFiles`, `sFiles`, `embed`, `importcfgs`.
  - `func Run(m Manifest, outDir, workDir string) error` — writes `outDir/pkg.a` and `outDir/importcfg` (one line: `packagefile <importPath>=<outDir>/pkg.a`).
- Produces, in package `main`:
  - `func loadManifest(v any) (out string, err error)` — reads `$NIX_ATTRS_JSON_FILE` (`{"manifest": …, "outputs": {"out": …}}`) when set, otherwise the `manifest` and `out` environment variables.
  - The command `gonixgo compile`.

- [ ] **Step 1: Write the failing toolchain test**

`internal/gotool/gotool_test.go`:

```go
package gotool

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/draganm/gonixgo/internal/testutil"
)

func TestNew(t *testing.T) {
	tc, err := New(testutil.Go(t), "", "", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if tc.GOOS != runtime.GOOS || tc.GOARCH != runtime.GOARCH {
		t.Errorf("target = %s/%s, want the host's %s/%s", tc.GOOS, tc.GOARCH, runtime.GOOS, runtime.GOARCH)
	}
	for _, file := range []string{
		filepath.Join(tc.ToolDir, "compile"),
		filepath.Join(tc.ToolDir, "asm"),
		filepath.Join(tc.ToolDir, "link"),
		filepath.Join(tc.GOROOT, "pkg", "include", "textflag.h"),
	} {
		if _, err := os.Stat(file); err != nil {
			t.Errorf("missing %s", file)
		}
	}
}

func TestNewCrossTarget(t *testing.T) {
	tc, err := New(testutil.Go(t), "linux", "amd64", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if tc.GOOS != "linux" || tc.GOARCH != "amd64" || tc.PIE() || tc.Shared() {
		t.Errorf("toolchain = %+v, PIE = %v", tc, tc.PIE())
	}
	want := []string{"-D", "GOOS_linux", "-D", "GOARCH_amd64", "-D", "GOAMD64_v1"}
	if got := tc.AsmDefines(); !reflect.DeepEqual(got, want) {
		t.Errorf("AsmDefines = %v, want %v", got, want)
	}
}

func TestPIEAndShared(t *testing.T) {
	tests := []struct {
		goos        string
		pie, shared bool
	}{
		{"linux", false, false},
		{"darwin", true, true},
		{"ios", true, true},
		{"android", true, true},
		{"windows", true, false},
		{"freebsd", false, false},
	}
	for _, tt := range tests {
		tc := &Toolchain{GOOS: tt.goos}
		if tc.PIE() != tt.pie || tc.Shared() != tt.shared {
			t.Errorf("%s: PIE = %v, Shared = %v, want %v, %v", tt.goos, tc.PIE(), tc.Shared(), tt.pie, tt.shared)
		}
	}
}

func TestAsmDefines(t *testing.T) {
	tests := []struct {
		goarch string
		env    map[string]string
		want   []string
	}{
		{"arm64", map[string]string{"GOARM64": "v8.0"}, nil},
		{"arm64", map[string]string{"GOARM64": "v8.1,lse"}, []string{"GOARM64_LSE"}},
		{"amd64", map[string]string{"GOAMD64": "v3"}, []string{"GOAMD64_v3"}},
		{"386", map[string]string{"GO386": "sse2"}, []string{"GO386_sse2"}},
		{"arm", map[string]string{"GOARM": "7"}, []string{"GOARM_7", "GOARM_6", "GOARM_5"}},
		{"arm", map[string]string{"GOARM": "6,softfloat"}, []string{"GOARM_6", "GOARM_5"}},
		{"arm", map[string]string{"GOARM": "5"}, []string{"GOARM_5"}},
		{"ppc64le", map[string]string{"GOPPC64": "power9"}, []string{"GOPPC64_power9", "GOPPC64_power8"}},
		{"riscv64", map[string]string{"GORISCV64": "rva20u64"}, []string{"GORISCV64_rva20u64"}},
		{"mipsle", map[string]string{"GOMIPS": "hardfloat"}, []string{"GOMIPS_hardfloat"}},
		{"mips64", map[string]string{"GOMIPS64": "hardfloat"}, []string{"GOMIPS64_hardfloat"}},
	}
	for _, tt := range tests {
		tc := &Toolchain{GOOS: "linux", GOARCH: tt.goarch, env: tt.env}
		want := []string{"-D", "GOOS_linux", "-D", "GOARCH_" + tt.goarch}
		for _, d := range tt.want {
			want = append(want, "-D", d)
		}
		if got := tc.AsmDefines(); !reflect.DeepEqual(got, want) {
			t.Errorf("%s %v: AsmDefines = %v, want %v", tt.goarch, tt.env, got, want)
		}
	}
}

func TestToolReportsFailure(t *testing.T) {
	tc, err := New(testutil.Go(t), "", "", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	err = tc.Tool(t.TempDir(), nil, "compile", "-no-such-flag")
	if err == nil || !strings.Contains(err.Error(), "go tool compile") {
		t.Fatalf("err = %v, want it to name the tool", err)
	}
}

func TestConcatFiles(t *testing.T) {
	dir := testutil.WriteTree(t, map[string]string{"a": "one\n", "b": "two\n"})
	dst := filepath.Join(dir, "out")
	if err := ConcatFiles(dst, []string{filepath.Join(dir, "a"), filepath.Join(dir, "b")}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "one\ntwo\n" {
		t.Fatalf("concatenation = %q", got)
	}
	if err := ConcatFiles(dst, []string{filepath.Join(dir, "absent")}); err == nil {
		t.Fatal("ConcatFiles of a missing file succeeded")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `nix develop --command go test ./internal/gotool/`
Expected: FAIL, `undefined: New`, `undefined: Toolchain`, `undefined: ConcatFiles`.

- [ ] **Step 3: Write the toolchain wrapper**

`internal/gotool/gotool.go`:

```go
// Package gotool locates and runs the Go toolchain's compile, asm and link
// binaries for one target.
package gotool

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Toolchain is a Go toolchain aimed at one GOOS/GOARCH.
type Toolchain struct {
	GOOS    string
	GOARCH  string
	GOROOT  string
	ToolDir string

	env  map[string]string // go env values, including the GO<arch> keys
	home string
}

var envKeys = []string{
	"GOOS", "GOARCH", "GOROOT", "GOTOOLDIR",
	"GO386", "GOAMD64", "GOARM", "GOARM64", "GOMIPS", "GOMIPS64", "GOPPC64", "GORISCV64",
}

// New asks goBin about itself. goos and goarch may be empty for the host.
// workDir is a scratch directory the toolchain may write to.
func New(goBin, goos, goarch, workDir string) (*Toolchain, error) {
	t := &Toolchain{GOOS: goos, GOARCH: goarch, home: filepath.Join(workDir, "home")}
	if err := os.MkdirAll(t.home, 0o755); err != nil {
		return nil, err
	}
	cmd := exec.Command(goBin, append([]string{"env", "-json"}, envKeys...)...)
	cmd.Env = t.environ()
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go env: %w", err)
	}
	if err := json.Unmarshal(out, &t.env); err != nil {
		return nil, fmt.Errorf("decoding go env output: %w", err)
	}
	t.GOOS, t.GOARCH = t.env["GOOS"], t.env["GOARCH"]
	t.GOROOT, t.ToolDir = t.env["GOROOT"], t.env["GOTOOLDIR"]
	return t, nil
}

// environ is a minimal environment: nothing from the caller's Go
// configuration leaks into a build step.
func (t *Toolchain) environ(extra ...string) []string {
	env := []string{
		"PATH=" + os.Getenv("PATH"),
		"TMPDIR=" + os.Getenv("TMPDIR"),
		"HOME=" + t.home,
		"GOCACHE=" + filepath.Join(t.home, "gocache"),
		"GOENV=off",
		"GOFLAGS=",
		"GOTOOLCHAIN=local",
	}
	if t.GOOS != "" {
		env = append(env, "GOOS="+t.GOOS)
	}
	if t.GOARCH != "" {
		env = append(env, "GOARCH="+t.GOARCH)
	}
	return append(env, extra...)
}

// Tool runs a toolchain binary such as compile, asm or link in dir. Its
// output goes to stderr.
func (t *Toolchain) Tool(dir string, extraEnv []string, name string, args ...string) error {
	cmd := exec.Command(filepath.Join(t.ToolDir, name), args...)
	cmd.Dir = dir
	// The tools resolve relative file arguments against PWD, as cmd/go
	// arranges; -trimpath matches on the resulting absolute paths.
	cmd.Env = t.environ(append([]string{"PWD=" + dir}, extraEnv...)...)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("go tool %s: %w", name, err)
	}
	return nil
}

// PIE reports whether the target's default build mode is a
// position-independent executable.
func (t *Toolchain) PIE() bool {
	switch t.GOOS {
	case "android", "ios", "windows", "darwin":
		return true
	}
	return false
}

// Shared reports whether compile and asm need -shared, which cmd/go passes
// for PIE targets other than Windows.
func (t *Toolchain) Shared() bool {
	return t.PIE() && t.GOOS != "windows"
}

// AsmDefines returns the -D flags cmd/go passes to the assembler.
func (t *Toolchain) AsmDefines() []string {
	d := []string{"-D", "GOOS_" + t.GOOS, "-D", "GOARCH_" + t.GOARCH}
	def := func(names ...string) {
		for _, name := range names {
			d = append(d, "-D", name)
		}
	}
	switch t.GOARCH {
	case "386":
		def("GO386_" + t.env["GO386"])
	case "amd64":
		def("GOAMD64_" + t.env["GOAMD64"])
	case "arm":
		switch {
		case strings.Contains(t.env["GOARM"], "7"):
			def("GOARM_7", "GOARM_6", "GOARM_5")
		case strings.Contains(t.env["GOARM"], "6"):
			def("GOARM_6", "GOARM_5")
		default:
			def("GOARM_5")
		}
	case "arm64":
		if strings.Contains(t.env["GOARM64"], ",lse") {
			def("GOARM64_LSE")
		}
	case "mips", "mipsle":
		def("GOMIPS_" + t.env["GOMIPS"])
	case "mips64", "mips64le":
		def("GOMIPS64_" + t.env["GOMIPS64"])
	case "ppc64", "ppc64le":
		switch t.env["GOPPC64"] {
		case "power10":
			def("GOPPC64_power10", "GOPPC64_power9", "GOPPC64_power8")
		case "power9":
			def("GOPPC64_power9", "GOPPC64_power8")
		default:
			def("GOPPC64_power8")
		}
	case "riscv64":
		def("GORISCV64_" + t.env["GORISCV64"])
	}
	return d
}

// ConcatFiles writes the contents of srcs, in order, to dst. It builds an
// importcfg from the fragments each package derivation outputs.
func ConcatFiles(dst string, srcs []string) error {
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	for _, src := range srcs {
		in, err := os.Open(src)
		if err != nil {
			out.Close()
			return err
		}
		_, err = io.Copy(out, in)
		in.Close()
		if err != nil {
			out.Close()
			return err
		}
	}
	return out.Close()
}
```

- [ ] **Step 4: Run the toolchain tests to verify they pass**

Run: `nix develop --command go test ./internal/gotool/ -v`
Expected: PASS for all six tests.

- [ ] **Step 5: Write the failing compile test**

`internal/compile/compile_test.go`:

```go
package compile

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/draganm/gonixgo/internal/testutil"
)

var files = map[string]string{
	"plain/plain.go": "package plain\n\nimport \"fmt\"\n\nfunc Hello() string { return fmt.Sprint(\"hello\") }\n",
	"lib/lib.go": `package lib

import _ "embed"

//go:embed data/msg.txt
var Msg string

// Nop is implemented in assembly.
func Nop()
`,
	// RET assembles on every architecture Go supports.
	"lib/nop.s":        "#include \"textflag.h\"\n\nTEXT ·Nop(SB),NOSPLIT,$0-0\n\tRET\n",
	"lib/data/msg.txt": "embedded",
	"broken/broken.go": "package broken\n\nfunc Oops() { return 1 }\n",
}

func libManifest(goBin, src, std string) Manifest {
	return Manifest{
		Go: goBin, ImportPath: "example.com/app/lib", SrcDir: filepath.Join(src, "lib"),
		TrimTo: "example.com/app/lib", Lang: "go1.21",
		GoFiles: []string{"lib.go"}, SFiles: []string{"nop.s"},
		Embed:      map[string][]string{"data/msg.txt": {"data/msg.txt"}},
		Importcfgs: []string{std},
	}
}

func TestRunWritesArchiveAndImportcfg(t *testing.T) {
	goBin := testutil.Go(t)
	src := testutil.WriteTree(t, files)
	out := filepath.Join(t.TempDir(), "out")
	m := Manifest{
		Go: goBin, ImportPath: "example.com/app/plain", SrcDir: filepath.Join(src, "plain"),
		TrimTo: "example.com/app/plain", Lang: "go1.21", GoFiles: []string{"plain.go"},
		Importcfgs: []string{testutil.StdImportcfg(t, goBin)},
	}
	if err := Run(m, out, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	archive, err := os.ReadFile(filepath.Join(out, "pkg.a"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(archive, []byte("!<arch>\n")) {
		t.Fatalf("pkg.a does not start with the archive magic")
	}
	if bytes.Contains(archive, []byte(src)) {
		t.Fatalf("pkg.a contains the source directory %s; -trimpath did not apply", src)
	}
	cfg, err := os.ReadFile(filepath.Join(out, "importcfg"))
	if err != nil {
		t.Fatal(err)
	}
	if want := "packagefile example.com/app/plain=" + filepath.Join(out, "pkg.a") + "\n"; string(cfg) != want {
		t.Fatalf("importcfg = %q, want %q", cfg, want)
	}
}

func TestRunWithAssemblyAndEmbed(t *testing.T) {
	goBin := testutil.Go(t)
	src := testutil.WriteTree(t, files)
	out := t.TempDir()
	if err := Run(libManifest(goBin, src, testutil.StdImportcfg(t, goBin)), out, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	archive, err := os.ReadFile(filepath.Join(out, "pkg.a"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(archive, []byte("nop.o")) {
		t.Fatal("pkg.a has no nop.o member")
	}
	if !bytes.Contains(archive, []byte("embedded")) {
		t.Fatal("pkg.a does not contain the embedded file's content")
	}
}

func TestRunIsReproducible(t *testing.T) {
	goBin := testutil.Go(t)
	src := testutil.WriteTree(t, files)
	std := testutil.StdImportcfg(t, goBin)
	var archives [2][]byte
	for i := range archives {
		out := t.TempDir()
		if err := Run(libManifest(goBin, src, std), out, t.TempDir()); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(filepath.Join(out, "pkg.a"))
		if err != nil {
			t.Fatal(err)
		}
		archives[i] = data
	}
	if !bytes.Equal(archives[0], archives[1]) {
		t.Fatal("two compiles of the same package in different directories differ")
	}
}

func TestRunReportsCompileErrors(t *testing.T) {
	goBin := testutil.Go(t)
	src := testutil.WriteTree(t, files)
	m := Manifest{
		Go: goBin, ImportPath: "example.com/app/broken", SrcDir: filepath.Join(src, "broken"),
		TrimTo: "example.com/app/broken", Lang: "go1.21", GoFiles: []string{"broken.go"},
		Importcfgs: []string{testutil.StdImportcfg(t, goBin)},
	}
	if err := Run(m, t.TempDir(), t.TempDir()); err == nil {
		t.Fatal("compiling a package with a type error succeeded")
	}
}

func TestWriteEmbedcfg(t *testing.T) {
	file := filepath.Join(t.TempDir(), "embedcfg")
	embed := map[string][]string{"static": {"static/a.txt", "static/b c.txt"}, "one.txt": {"one.txt"}}
	if err := writeEmbedcfg(file, "/src/pkg", embed); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Patterns map[string][]string
		Files    map[string]string
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	wantFiles := map[string]string{
		"static/a.txt":   "/src/pkg/static/a.txt",
		"static/b c.txt": "/src/pkg/static/b c.txt",
		"one.txt":        "/src/pkg/one.txt",
	}
	if !reflect.DeepEqual(got.Patterns, embed) || !reflect.DeepEqual(got.Files, wantFiles) {
		t.Fatalf("embedcfg = %+v", got)
	}
}

func TestAppendObjects(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "pkg.a")
	if err := os.WriteFile(archive, []byte("!<arch>\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	odd := filepath.Join(dir, "odd.o")
	even := filepath.Join(dir, "a-rather-long-object-name.o")
	if err := os.WriteFile(odd, []byte("abc"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(even, []byte("abcd"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := appendObjects(archive, []string{odd, even}); err != nil {
		t.Fatal(err)
	}
	header := func(name string, size int) string {
		return fmt.Sprintf("%-16s%-12d%-6d%-6d%-8o%-10d`\n", name, 0, 0, 0, 0o644, size)
	}
	// Members are padded to an even length; names are cut to 16 bytes.
	want := "!<arch>\n" + header("odd.o", 3) + "abc\x00" + header("a-rather-long-ob", 4) + "abcd"
	got, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("archive = %q\nwant      %q", got, want)
	}
}
```

- [ ] **Step 6: Run the test to verify it fails**

Run: `nix develop --command go test ./internal/compile/`
Expected: FAIL, `undefined: Manifest`, `undefined: Run`, `undefined: writeEmbedcfg`, `undefined: appendObjects`.

- [ ] **Step 7: Write the compile implementation**

`internal/compile/compile.go`:

```go
// Package compile builds one Go package into an archive, the way cmd/go
// does with -trimpath, without cmd/go.
package compile

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"github.com/draganm/gonixgo/internal/gotool"
)

// Manifest describes one package compile. The compile builder in
// nix/builders.nix produces it.
type Manifest struct {
	Go         string              `json:"go"`         // path to the go binary
	GOOS       string              `json:"goos"`       // "" for the host
	GOARCH     string              `json:"goarch"`     // "" for the host
	ImportPath string              `json:"importPath"` //
	IsMain     bool                `json:"isMain"`     //
	SrcDir     string              `json:"srcDir"`     // directory holding the package's files
	TrimTo     string              `json:"trimTo"`     // what -trimpath rewrites SrcDir to
	Lang       string              `json:"lang"`       // e.g. go1.24
	GoFiles    []string            `json:"goFiles"`    //
	SFiles     []string            `json:"sFiles"`     //
	Embed      map[string][]string `json:"embed"`      // //go:embed pattern to files
	Importcfgs []string            `json:"importcfgs"` // importcfg fragments of the standard library and direct imports
}

// Run compiles the package into outDir/pkg.a and writes outDir/importcfg,
// the fragment importers and the linker use to find it.
func Run(m Manifest, outDir, workDir string) error {
	tc, err := gotool.New(m.Go, m.GOOS, m.GOARCH, workDir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	importcfg := filepath.Join(workDir, "importcfg")
	if err := gotool.ConcatFiles(importcfg, m.Importcfgs); err != nil {
		return err
	}

	archive := filepath.Join(outDir, "pkg.a")
	pkgPath := m.ImportPath
	if m.IsMain {
		pkgPath = "main"
	}
	trim := m.SrcDir + "=>" + m.TrimTo + ";" + workDir + "=>"

	args := []string{"-o", archive, "-trimpath", trim, "-p", pkgPath, "-lang=" + m.Lang}
	if len(m.SFiles) == 0 {
		// Without assembly every function must have a body.
		args = append(args, "-complete")
	}
	args = append(args, "-buildid", "", "-c="+strconv.Itoa(cores()))
	if tc.Shared() {
		args = append(args, "-shared")
	}
	args = append(args, "-nolocalimports", "-importcfg", importcfg)
	if len(m.Embed) > 0 {
		embedcfg := filepath.Join(workDir, "embedcfg")
		if err := writeEmbedcfg(embedcfg, m.SrcDir, m.Embed); err != nil {
			return err
		}
		args = append(args, "-embedcfg", embedcfg)
	}
	args = append(args, "-pack")

	asm := []string{"-p", pkgPath, "-trimpath", trim, "-I", workDir, "-I", filepath.Join(tc.GOROOT, "pkg", "include")}
	asm = append(asm, tc.AsmDefines()...)
	if tc.Shared() {
		asm = append(asm, "-shared")
	}
	if len(m.SFiles) > 0 {
		// The compiler needs the assembly's symbol ABIs, and writes the
		// header the assembly includes.
		symabis := filepath.Join(workDir, "symabis")
		asmhdr := filepath.Join(workDir, "go_asm.h")
		if err := os.WriteFile(asmhdr, nil, 0o644); err != nil {
			return err
		}
		gen := slices.Concat(asm, []string{"-gensymabis", "-o", symabis}, dotSlash(m.SFiles))
		if err := tc.Tool(m.SrcDir, nil, "asm", gen...); err != nil {
			return err
		}
		args = append(args, "-symabis", symabis, "-asmhdr", asmhdr)
	}
	if err := tc.Tool(m.SrcDir, nil, "compile", append(args, dotSlash(m.GoFiles)...)...); err != nil {
		return err
	}

	var objects []string
	for _, s := range m.SFiles {
		obj := filepath.Join(workDir, strings.TrimSuffix(filepath.Base(s), ".s")+".o")
		if err := tc.Tool(m.SrcDir, nil, "asm", slices.Concat(asm, []string{"-o", obj, "./" + s})...); err != nil {
			return err
		}
		objects = append(objects, obj)
	}
	if err := appendObjects(archive, objects); err != nil {
		return err
	}

	line := "packagefile " + m.ImportPath + "=" + archive + "\n"
	return os.WriteFile(filepath.Join(outDir, "importcfg"), []byte(line), 0o644)
}

// cores is the compiler's backend concurrency.
func cores() int {
	if n, err := strconv.Atoi(os.Getenv("NIX_BUILD_CORES")); err == nil && n > 0 {
		return n
	}
	return runtime.NumCPU()
}

// dotSlash names files relative to the package directory, as cmd/go does.
func dotSlash(files []string) []string {
	out := make([]string, len(files))
	for i, f := range files {
		out[i] = "./" + f
	}
	return out
}

// writeEmbedcfg writes the file the compiler's -embedcfg flag reads:
// each pattern's files, and where each file is on disk.
func writeEmbedcfg(file, srcDir string, embed map[string][]string) error {
	cfg := struct {
		Patterns map[string][]string
		Files    map[string]string
	}{Patterns: embed, Files: map[string]string{}}
	for _, files := range embed {
		for _, f := range files {
			cfg.Files[f] = filepath.Join(srcDir, filepath.FromSlash(f))
		}
	}
	data, err := json.MarshalIndent(cfg, "", "\t")
	if err != nil {
		return err
	}
	return os.WriteFile(file, data, 0o644)
}

// appendObjects adds object files to a Go archive. The Go distribution no
// longer ships the pack tool, and cmd/go appends the same way.
func appendObjects(archive string, objects []string) error {
	if len(objects) == 0 {
		return nil
	}
	dst, err := os.OpenFile(archive, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		return err
	}
	defer dst.Close()
	w := bufio.NewWriter(dst)
	for _, object := range objects {
		data, err := os.ReadFile(object)
		if err != nil {
			return err
		}
		name := filepath.Base(object)
		if len(name) > 16 {
			name = name[:16]
		}
		fmt.Fprintf(w, "%-16s%-12d%-6d%-6d%-8o%-10d`\n", name, 0, 0, 0, 0o644, len(data))
		w.Write(data)
		if len(data)%2 != 0 {
			w.WriteByte(0)
		}
	}
	if err := w.Flush(); err != nil {
		return err
	}
	return dst.Close()
}
```

- [ ] **Step 8: Run the compile tests to verify they pass**

Run: `nix develop --command go test ./internal/compile/ -v`
Expected: PASS for all six tests. The first run builds the standard library into the Go build cache and takes about a minute.

- [ ] **Step 9: Write the failing manifest test**

`cmd/gonixgo/manifest_test.go`:

```go
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
```

- [ ] **Step 10: Run the test to verify it fails**

Run: `nix develop --command go test ./cmd/gonixgo/`
Expected: FAIL, `undefined: loadManifest`.

- [ ] **Step 11: Write the manifest loader and the command**

`cmd/gonixgo/manifest.go`:

```go
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

// loadManifest decodes the manifest of the derivation being built into v
// and returns the derivation's output path. Derivations with
// __structuredAttrs get both from the attrs file; others pass the manifest
// and $out in the environment.
func loadManifest(v any) (out string, err error) {
	if file := os.Getenv("NIX_ATTRS_JSON_FILE"); file != "" {
		data, err := os.ReadFile(file)
		if err != nil {
			return "", err
		}
		var attrs struct {
			Manifest json.RawMessage `json:"manifest"`
			Outputs  struct {
				Out string `json:"out"`
			} `json:"outputs"`
		}
		if err := json.Unmarshal(data, &attrs); err != nil {
			return "", fmt.Errorf("parsing %s: %w", file, err)
		}
		if err := json.Unmarshal(attrs.Manifest, v); err != nil {
			return "", fmt.Errorf("parsing manifest in %s: %w", file, err)
		}
		return attrs.Outputs.Out, nil
	}

	manifest, out := os.Getenv("manifest"), os.Getenv("out")
	if manifest == "" || out == "" {
		return "", errors.New("no manifest: expected NIX_ATTRS_JSON_FILE, or the manifest and out environment variables")
	}
	if err := json.Unmarshal([]byte(manifest), v); err != nil {
		return "", fmt.Errorf("parsing manifest: %w", err)
	}
	return out, nil
}
```

`cmd/gonixgo/compile.go`:

```go
package main

import (
	"io"
	"os"

	"github.com/draganm/gonixgo/internal/compile"
)

func init() { commands["compile"] = compileCmd }

// compileCmd is the builder of a package derivation.
func compileCmd(_ []string, _ io.Writer) error {
	var m compile.Manifest
	out, err := loadManifest(&m)
	if err != nil {
		return err
	}
	work, err := os.MkdirTemp("", "gonixgo-compile-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	return compile.Run(m, out, work)
}
```

- [ ] **Step 12: Run every test and vet**

Run: `nix develop --command go test ./... && nix develop --command go vet ./...`
Expected: `ok` for every package, and no vet output.

- [ ] **Step 13: Commit**

```bash
git add internal/gotool internal/compile cmd/gonixgo/manifest.go cmd/gonixgo/manifest_test.go cmd/gonixgo/compile.go
git commit -m "feat(compile): compile one package with go tool compile and asm" -m "$TRAILERS"
```

---

### Task 11: The `link` subcommand

**Files:**
- Create: `internal/link/link.go`
- Create: `cmd/gonixgo/link.go`
- Test: `internal/link/link_test.go`

**Interfaces:**
- Consumes: `gotool.New`, `(*gotool.Toolchain).Tool`, `PIE`, `gotool.ConcatFiles` (Task 10); `modinfo.Wrap` (Task 5); `compile.Run`, `compile.Manifest` (Task 10, in the test); `loadManifest`, `commands`.
- Produces:
  - `type Manifest struct` with JSON fields `go`, `goos`, `goarch`, `binName`, `main` (path to the main package's `pkg.a`), `importcfgs`, `modinfo`, `godebug`, `ldflags`.
  - `func Run(m Manifest, outDir, workDir string) error` — writes `outDir/bin/<binName>`.
  - `func SplitFlags(flags []string) ([]string, error)`
  - The command `gonixgo link`.

- [ ] **Step 1: Write the failing test**

`internal/link/link_test.go`:

```go
package link

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/draganm/gonixgo/internal/compile"
	"github.com/draganm/gonixgo/internal/testutil"
)

func TestSplitFlags(t *testing.T) {
	tests := []struct {
		in   []string
		want []string
	}{
		{nil, nil},
		{[]string{"-s", "-w"}, []string{"-s", "-w"}},
		{[]string{"-s -w"}, []string{"-s", "-w"}},
		{[]string{"-X main.version=1.2.3"}, []string{"-X", "main.version=1.2.3"}},
		{[]string{"-X 'main.msg=hello world'"}, []string{"-X", "main.msg=hello world"}},
		{[]string{`-X "main.msg=it's"`}, []string{"-X", "main.msg=it's"}},
		{[]string{"  -s  \t -w  "}, []string{"-s", "-w"}},
		{[]string{"-X", "main.v=1"}, []string{"-X", "main.v=1"}},
	}
	for _, tt := range tests {
		got, err := SplitFlags(tt.in)
		if err != nil {
			t.Errorf("SplitFlags(%q): %v", tt.in, err)
			continue
		}
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("SplitFlags(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
	if _, err := SplitFlags([]string{"-X 'main.msg=unterminated"}); err == nil {
		t.Error("SplitFlags accepted an unterminated quote")
	}
}

var files = map[string]string{
	"lib/lib.go": `package lib

import _ "embed"

//go:embed data/msg.txt
var Msg string

// Nop is implemented in assembly.
func Nop()
`,
	"lib/nop.s":        "#include \"textflag.h\"\n\nTEXT ·Nop(SB),NOSPLIT,$0-0\n\tRET\n",
	"lib/data/msg.txt": "embedded",
	"main.go": `package main

import (
	"fmt"

	"example.com/app/lib"
)

var version = "unset"

func main() {
	lib.Nop()
	fmt.Println(lib.Msg, version)
}
`,
}

const modinfo = "path\texample.com/app\nmod\texample.com/app\t(devel)\t\nbuild\t-trimpath=true\n"

func TestCompileLinkRun(t *testing.T) {
	goBin := testutil.Go(t)
	std := testutil.StdImportcfg(t, goBin)
	src := testutil.WriteTree(t, files)

	libOut, mainOut, binOut := t.TempDir(), t.TempDir(), t.TempDir()
	if err := compile.Run(compile.Manifest{
		Go: goBin, ImportPath: "example.com/app/lib", SrcDir: filepath.Join(src, "lib"),
		TrimTo: "example.com/app/lib", Lang: "go1.21",
		GoFiles: []string{"lib.go"}, SFiles: []string{"nop.s"},
		Embed:      map[string][]string{"data/msg.txt": {"data/msg.txt"}},
		Importcfgs: []string{std},
	}, libOut, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if err := compile.Run(compile.Manifest{
		Go: goBin, ImportPath: "example.com/app", IsMain: true, SrcDir: src,
		TrimTo: "example.com/app", Lang: "go1.21", GoFiles: []string{"main.go"},
		Importcfgs: []string{std, filepath.Join(libOut, "importcfg")},
	}, mainOut, t.TempDir()); err != nil {
		t.Fatal(err)
	}

	err := Run(Manifest{
		Go: goBin, BinName: "app", Main: filepath.Join(mainOut, "pkg.a"),
		Importcfgs: []string{std, filepath.Join(mainOut, "importcfg"), filepath.Join(libOut, "importcfg")},
		Modinfo:    modinfo,
		LDFlags:    []string{"-X 'main.version=1 2'"},
	}, binOut, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	bin := filepath.Join(binOut, "bin", "app")
	out, err := exec.Command(bin).Output()
	if err != nil {
		t.Fatalf("running the linked binary: %v", err)
	}
	if got := string(out); got != "embedded 1 2\n" {
		t.Fatalf("binary printed %q, want %q", got, "embedded 1 2\n")
	}

	info, err := exec.Command(goBin, "version", "-m", bin).Output()
	if err != nil {
		t.Fatalf("go version -m: %v", err)
	}
	for _, want := range []string{"\tpath\texample.com/app\n", "\tmod\texample.com/app\t(devel)\t\n", "\tbuild\t-trimpath=true\n"} {
		if !strings.Contains(string(info), want) {
			t.Errorf("go version -m lacks %q:\n%s", want, info)
		}
	}

	data, err := os.ReadFile(bin)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte(src)) {
		t.Errorf("binary contains the source directory %s", src)
	}
}

func TestRunReportsLinkErrors(t *testing.T) {
	goBin := testutil.Go(t)
	err := Run(Manifest{Go: goBin, BinName: "app", Main: filepath.Join(t.TempDir(), "absent.a")}, t.TempDir(), t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "go tool link") {
		t.Fatalf("err = %v, want a link failure", err)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `nix develop --command go test ./internal/link/`
Expected: FAIL, `undefined: SplitFlags`, `undefined: Run`, `undefined: Manifest`.

- [ ] **Step 3: Write the implementation**

`internal/link/link.go`:

```go
// Package link links one main package into a binary, the way cmd/go does
// with -trimpath, without cmd/go.
package link

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/draganm/gonixgo/internal/gotool"
	"github.com/draganm/gonixgo/internal/modinfo"
)

// Manifest describes one link. The link builder in nix/builders.nix
// produces it.
type Manifest struct {
	Go         string   `json:"go"`
	GOOS       string   `json:"goos"`
	GOARCH     string   `json:"goarch"`
	BinName    string   `json:"binName"`
	Main       string   `json:"main"`       // the main package's archive
	Importcfgs []string `json:"importcfgs"` // fragments for the standard library, the main package and its transitive imports
	Modinfo    string   `json:"modinfo"`    // module info to embed
	Godebug    string   `json:"godebug"`    // DefaultGODEBUG, "" for none
	LDFlags    []string `json:"ldflags"`
}

// Run links the binary to outDir/bin/<BinName>.
func Run(m Manifest, outDir, workDir string) error {
	tc, err := gotool.New(m.Go, m.GOOS, m.GOARCH, workDir)
	if err != nil {
		return err
	}
	ldflags, err := SplitFlags(m.LDFlags)
	if err != nil {
		return err
	}

	importcfg := filepath.Join(workDir, "importcfg.link")
	if err := gotool.ConcatFiles(importcfg, m.Importcfgs); err != nil {
		return err
	}
	f, err := os.OpenFile(importcfg, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(f, "modinfo %q\n", modinfo.Wrap(m.Modinfo))
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}

	binDir := filepath.Join(outDir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return err
	}
	args := []string{"-o", filepath.Join(binDir, m.BinName), "-importcfg", importcfg}
	if m.Godebug != "" {
		args = append(args, "-X=runtime.godebugDefault="+m.Godebug)
	}
	mode := "exe"
	if tc.PIE() {
		mode = "pie"
	}
	args = append(args, "-buildmode="+mode, "-buildid=redacted")
	args = append(args, ldflags...)
	args = append(args, m.Main)

	// An empty GOROOT keeps the toolchain's path out of the binary, as
	// go build -trimpath does.
	return tc.Tool(workDir, []string{"GOROOT="}, "link", args...)
}

// SplitFlags splits each element the way `go build -ldflags` splits its
// argument: on white space, with a leading single or double quote grouping
// up to its match. There is no unescaping inside quotes.
func SplitFlags(flags []string) ([]string, error) {
	var out []string
	for _, flag := range flags {
		s := flag
		for {
			for len(s) > 0 && isSpace(s[0]) {
				s = s[1:]
			}
			if len(s) == 0 {
				break
			}
			if quote := s[0]; quote == '"' || quote == '\'' {
				s = s[1:]
				end := 0
				for end < len(s) && s[end] != quote {
					end++
				}
				if end == len(s) {
					return nil, fmt.Errorf("ldflags %q: unterminated %c string", flag, quote)
				}
				out = append(out, s[:end])
				s = s[end+1:]
				continue
			}
			end := 0
			for end < len(s) && !isSpace(s[end]) {
				end++
			}
			out = append(out, s[:end])
			s = s[end:]
		}
	}
	return out, nil
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}
```

`cmd/gonixgo/link.go`:

```go
package main

import (
	"io"
	"os"

	"github.com/draganm/gonixgo/internal/link"
)

func init() { commands["link"] = linkCmd }

// linkCmd is the builder of a binary derivation.
func linkCmd(_ []string, _ io.Writer) error {
	var m link.Manifest
	out, err := loadManifest(&m)
	if err != nil {
		return err
	}
	work, err := os.MkdirTemp("", "gonixgo-link-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	return link.Run(m, out, work)
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `nix develop --command go test ./internal/link/ ./cmd/gonixgo/ -v`
Expected: PASS. `TestCompileLinkRun` shows a package with assembly and an embed compiled, linked with a quoted `-X` value, run, and inspected with `go version -m`.

- [ ] **Step 5: Commit**

```bash
git add internal/link cmd/gonixgo/link.go
git commit -m "feat(link): link a binary with embedded module info" -m "$TRAILERS"
```

---

### Task 12: The `fetch` subcommand

**Files:**
- Create: `internal/fetch/fetch.go`
- Create: `cmd/gonixgo/fetch.go`
- Test: `internal/fetch/fetch_test.go`

**Interfaces:**
- Consumes: `nar.Hash` (Task 2, in the test); `loadManifest`, `commands`.
- Produces:
  - `type Manifest struct` with JSON fields `go`, `path`, `version`.
  - `func Run(m Manifest, outDir, workDir string) error` — downloads the module and copies its extracted tree to `outDir`.
  - `func Escape(s string) string` — the module cache's case encoding.
  - The command `gonixgo fetch`, reading its manifest from the `manifest` and `out` environment variables.

- [ ] **Step 1: Write the failing test**

`internal/fetch/fetch_test.go`:

```go
package fetch

import (
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
	m := Manifest{Go: testutil.Go(t), Path: "rsc.io/quote", Version: "v1.5.2"}
	if err := Run(m, out, t.TempDir()); err != nil {
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
	if err := Run(m, filepath.Join(t.TempDir(), "out"), t.TempDir()); err == nil {
		t.Fatal("Run succeeded with GOPROXY=off")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `nix develop --command go test ./internal/fetch/`
Expected: FAIL, `undefined: Escape`, `undefined: copyTree`, `undefined: Run`, `undefined: Manifest`.

- [ ] **Step 3: Write the implementation**

`internal/fetch/fetch.go`:

```go
// Package fetch downloads one Go module and keeps its extracted source
// tree, the content a module's fixed-output derivation is hashed over.
package fetch

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Manifest names the module to download. The fetchModule builder in
// nix/builders.nix produces it.
type Manifest struct {
	Go      string `json:"go"`
	Path    string `json:"path"`
	Version string `json:"version"`
}

// Run downloads the module into a private module cache under workDir and
// copies its extracted tree to outDir. Download metadata is left behind so
// the result does not depend on which proxy served it.
func Run(m Manifest, outDir, workDir string) error {
	modCache := filepath.Join(workDir, "modcache")
	cmd := exec.Command(m.Go, "mod", "download", m.Path+"@"+m.Version)
	cmd.Dir = workDir
	// GOPROXY, NETRC and the proxy variables come from the environment:
	// the derivation lists them as impureEnvVars.
	cmd.Env = append(os.Environ(),
		"HOME="+filepath.Join(workDir, "home"),
		"GOMODCACHE="+modCache,
		"GOCACHE="+filepath.Join(workDir, "gocache"),
		"GOENV=off",
		"GOFLAGS=",
		"GOWORK=off",
		"GOTOOLCHAIN=local",
		// The derivation's output hash is the integrity check.
		"GOSUMDB=off",
	)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("go mod download %s@%s: %w", m.Path, m.Version, err)
	}
	return copyTree(filepath.Join(modCache, Escape(m.Path)+"@"+Escape(m.Version)), outDir)
}

// Escape applies the module cache's case encoding: an upper-case letter
// becomes '!' followed by its lower-case form.
func Escape(s string) string {
	var b strings.Builder
	for _, r := range s {
		if 'A' <= r && r <= 'Z' {
			b.WriteByte('!')
			r += 'a' - 'A'
		}
		b.WriteRune(r)
	}
	return b.String()
}

// copyTree copies src to dst, keeping what a NAR records: file contents,
// the execute bit, symlinks and directory structure. The copy is writable
// even though the module cache is not.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		switch {
		case d.IsDir():
			return os.MkdirAll(target, 0o755)
		case d.Type()&fs.ModeSymlink != 0:
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		case d.Type().IsRegular():
			info, err := d.Info()
			if err != nil {
				return err
			}
			mode := fs.FileMode(0o644)
			if info.Mode()&0o100 != 0 {
				mode = 0o755
			}
			return copyFile(path, target, mode)
		default:
			return fmt.Errorf("%s: unsupported file type %s", path, d.Type())
		}
	})
}

func copyFile(src, dst string, mode fs.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
```

`cmd/gonixgo/fetch.go`:

```go
package main

import (
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/draganm/gonixgo/internal/fetch"
)

func init() { commands["fetch"] = fetchCmd }

// fetchCmd is the builder of a module's fixed-output derivation. It runs
// only when the module was not pre-seeded into the store at evaluation.
func fetchCmd(_ []string, _ io.Writer) error {
	var m fetch.Manifest
	out, err := loadManifest(&m)
	if err != nil {
		return err
	}
	work, err := os.MkdirTemp("", "gonixgo-fetch-")
	if err != nil {
		return err
	}
	defer func() {
		// The private module cache is read-only; make it removable.
		filepath.WalkDir(work, func(path string, d fs.DirEntry, err error) error {
			if err == nil && d.IsDir() {
				os.Chmod(path, 0o755)
			}
			return nil
		})
		os.RemoveAll(work)
	}()
	return fetch.Run(m, out, work)
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `nix develop --command go test ./internal/fetch/ ./cmd/gonixgo/ -v`
Expected: PASS; `TestRunDownloadsAModule` is skipped.

Run: `GONIXGO_NETWORK_TESTS=1 nix develop --command go test ./internal/fetch/ -run TestRunDownloadsAModule -v`
Expected: PASS (downloads `rsc.io/quote@v1.5.2`).

- [ ] **Step 5: Run every test and vet**

Run: `nix develop --command go test ./... && nix develop --command go vet ./...`
Expected: `ok` for every package, and no vet output.

- [ ] **Step 6: Commit**

```bash
git add internal/fetch cmd/gonixgo/fetch.go
git commit -m "feat(fetch): add the fallback module download" -m "$TRAILERS"
```

---

### Task 13: The tool and standard-library derivations, and the flake outputs

**Files:**
- Create: `nix/tool.nix`, `nix/stdlib.nix`, `nix/mk-go-env.nix`, `default.nix`
- Modify: `flake.nix` (add `lib`, `packages`, `legacyPackages`)

**Interfaces:**
- Consumes: the `gonixgo` binary's source (`go.mod`, `cmd/`, `internal/`).
- Produces:
  - `nix/tool.nix`: `{ lib, buildGoModule }` → the `gonixgo` derivation, with `bin/gonixgo`.
  - `nix/stdlib.nix`: `{ lib, go, runCommand, runCommandCC, goos, goarch }` → `cgoEnabled:` → a derivation whose output holds `<import path>.a` for every standard-library package and `importcfg`.
  - `nix/mk-go-env.nix`: `{ pkgs, go ? pkgs.buildPackages.go, evalPkgs ? pkgs.buildPackages }` → `{ tool, go, stdlib }` (Task 14 adds `builders` and `buildGoApplication`).
  - Flake outputs `lib.mkGoEnv`, `packages.<system>.{gonixgo,default}`, `legacyPackages.<system>.goEnv`.

- [ ] **Step 1: Verify the outputs do not exist yet**

Run: `nix build "path:$PWD#gonixgo" --no-link`
Expected: FAIL, `error: flake 'path:…' does not provide attribute 'packages.aarch64-darwin.gonixgo', …`

- [ ] **Step 2: Write the tool derivation**

`nix/tool.nix`:

```nix
# The gonixgo binary. It imports nothing outside the standard library, so
# there is no vendor hash to maintain.
{ lib, buildGoModule }:
let
  # Only what the binary is built from. Editing docs, tests or the Nix
  # library must not rebuild the tool, and with it every package.
  goSources = dir:
    lib.fileset.fileFilter (f: f.hasExt "go" && !lib.hasSuffix "_test.go" f.name) dir;
in
buildGoModule {
  pname = "gonixgo";
  version = "0.1.0";
  src = lib.fileset.toSource {
    root = ../.;
    fileset = lib.fileset.unions [
      ../go.mod
      (goSources ../cmd)
      (goSources ../internal)
    ];
  };
  vendorHash = null;
  subPackages = [ "cmd/gonixgo" ];
  # A static binary can be the builder of a derivation with no other inputs.
  env.CGO_ENABLED = 0;
  doCheck = false;
}
```

- [ ] **Step 3: Write the standard-library derivation**

`nix/stdlib.nix`:

```nix
# The Go standard library compiled for one target: every archive, plus an
# importcfg listing them. One derivation serves every build in a goEnv.
{ lib, go, runCommand, runCommandCC, goos, goarch }:

cgoEnabled:
let
  # The cgo parts of the standard library need a C compiler.
  run = if cgoEnabled then runCommandCC else runCommand;
in
run "go-stdlib-${go.version}-${goos}-${goarch}${lib.optionalString cgoEnabled "-cgo"}"
{
  nativeBuildInputs = [ go ];
  env = {
    GOOS = goos;
    GOARCH = goarch;
    CGO_ENABLED = if cgoEnabled then "1" else "0";
  };
}
  ''
    export HOME="$NIX_BUILD_TOP/home"
    mkdir -p "$HOME" goroot/pkg

    # go install writes into GOROOT, so build in a writable copy that
    # shares the toolchain's tools and headers.
    goroot="$(go env GOROOT)"
    cp -R "$goroot/src" "$goroot/lib" goroot/
    chmod -R u+w goroot
    ln -s "$goroot/pkg/tool" "$goroot/pkg/include" goroot/pkg/

    GODEBUG=installgoroot=all GOROOT="$NIX_BUILD_TOP/goroot" go install -trimpath std

    mkdir -p "$out"
    cp -R goroot/pkg/*_*/. "$out/"
    (cd "$out" && find . -name '*.a' | sort | sed -e 's|^\./||' -e 's|\.a$||') |
      while read -r pkg; do
        echo "packagefile $pkg=$out/$pkg.a"
      done > "$out/importcfg"
  ''
```

- [ ] **Step 4: Write `mkGoEnv` and the entry points**

`nix/mk-go-env.nix`:

```nix
# mkGoEnv ties gonixgo to one nixpkgs: that nixpkgs builds the tool and the
# standard library, and performs the Go build.
{ pkgs
, go ? pkgs.buildPackages.go
, evalPkgs ? pkgs.buildPackages
}:
let
  inherit (pkgs) lib;
  buildPkgs = pkgs.buildPackages;

  # Derivations run on the build platform and produce code for the target.
  goos = go.GOOS;
  goarch = go.GOARCH;

  tool = buildPkgs.callPackage ./tool.nix { };

  stdlib = import ./stdlib.nix {
    inherit lib go goos goarch;
    inherit (buildPkgs) runCommand runCommandCC;
  };
in
{
  inherit tool go stdlib;
}
```

`default.nix`:

```nix
# Non-flake entry point: `import ./. { inherit pkgs; }` is mkGoEnv.
args: import ./nix/mk-go-env.nix args
```

Replace the `outputs` of `flake.nix` so the whole file reads:

```nix
{
  description = "gonixgo";
  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-26.05";

    systems.url = "github:nix-systems/default";

  };

  outputs = { self, nixpkgs, systems, ... }@inputs:
    let
      eachSystem = f:
        nixpkgs.lib.genAttrs (import systems)
        (system: f system nixpkgs.legacyPackages.${system});
      mkGoEnv = import ./nix/mk-go-env.nix;
    in {

      lib = { inherit mkGoEnv; };

      packages = eachSystem (system: pkgs: rec {
        gonixgo = (mkGoEnv { inherit pkgs; }).tool;
        default = gonixgo;
      });

      # Anything that needs builtins.exec lives under legacyPackages, which
      # `nix flake check` and `nix flake show` do not evaluate.
      legacyPackages = eachSystem (system: pkgs: {
        goEnv = mkGoEnv { inherit pkgs; };
      });

      devShells = eachSystem (system: pkgs: {
        default = pkgs.mkShell {
          shellHook = ''
            # Set here the env vars you want to be available in the shell
          '';
          hardeningDisable = [ "all" ];

          packages = with pkgs; [ go ];
        };
      });
    };
}
```

- [ ] **Step 5: Build the tool and run it**

```bash
git add nix default.nix flake.nix
out="$(nix build "path:$PWD#gonixgo" --no-link --print-out-paths)"
"$out/bin/gonixgo"; echo "exit=$?"
```

Expected: `usage: gonixgo <resolve|compile|link|fetch> ...` and `exit=2`.

- [ ] **Step 6: Build both standard-library variants and check them**

```bash
stdlib() {
  nix build --impure --no-link --print-out-paths --expr \
    "(builtins.getFlake \"path:$PWD\").legacyPackages.\${builtins.currentSystem}.goEnv.stdlib $1"
}
cgo="$(stdlib true)"; nocgo="$(stdlib false)"
grep -c '^packagefile fmt=' "$cgo/importcfg" "$nocgo/importcfg"
test -f "$cgo/fmt.a" && test -f "$cgo/runtime/cgo.a" && test -f "$cgo/vendor/golang.org/x/net/dns/dnsmessage.a" && echo "cgo ok"
test -f "$nocgo/fmt.a" && ! test -e "$nocgo/runtime/cgo.a" && echo "nocgo ok"
grep -vc "=$cgo/" "$cgo/importcfg" || true
```

Expected: `…/importcfg:1` for both files, then `cgo ok`, `nocgo ok`, and `0` (no importcfg entry points outside the output). The derivation names end in `-darwin-arm64-cgo` and `-darwin-arm64`.

- [ ] **Step 7: Check the flake**

Run: `nix flake check "path:$PWD"`
Expected: succeeds without the `exec` option; it evaluates `packages` and `devShells` and does not touch `legacyPackages`.

- [ ] **Step 8: Commit**

```bash
git add nix default.nix flake.nix flake.lock
git commit -m "feat(nix): build the tool and the standard library" -m "$TRAILERS"
```

---

### Task 14: The builders, `buildGoApplication` and the first fixture

**Files:**
- Create: `nix/builders.nix`, `nix/build-go-application.nix`
- Modify: `nix/mk-go-env.nix` (add the builders and `buildGoApplication`), `flake.nix` (add `fixtures`)
- Create: `tests/fixtures.nix`, `tests/run.sh`
- Create: `tests/fixtures/hello-deps/{go.mod,go.sum,main.go,internal/greet/greet.go}`

**Interfaces:**
- Consumes: the generated graph's calls (Task 8); the manifests of `compile.Manifest` (Task 10), `link.Manifest` (Task 11), `fetch.Manifest` (Task 12); `resolve.Args` (Task 9); `tool`, `stdlib` (Task 13).
- Produces:
  - `nix/builders.nix`: `{ lib, cacert, go, tool, stdlib, system, goos, goarch }` → `{ srcStr, cgoEnabled, ldflags }` → `{ fetchModule, localDir, compile, link }`.
  - `nix/build-go-application.nix`: `{ lib, runCommand, go, evalGo, evalTool, mkBuilders, goos, goarch }` → `buildGoApplication`.
  - `mkGoEnv` returns `{ buildGoApplication, tool, go, stdlib, builders }`.
  - `buildGoApplication { pname, version ? null, src, modRoot ? ".", subPackages ? [ "." ], tags ? [ ], ldflags ? [ ], CGO_ENABLED ? null, doCheck ? true, checkFlags ? [ ], packageOverrides ? { }, meta ? { } }` → a derivation with `$out/bin/*` and `passthru = { go, graph, modules, packages, bins, tests }`.
  - `legacyPackages.<system>.fixtures.<name>`, and `tests/run.sh`.

- [ ] **Step 1: Write the fixture**

`tests/fixtures/hello-deps/go.mod`:

```
module example.com/hello

go 1.24

require github.com/fatih/color v1.18.0
```

`tests/fixtures/hello-deps/main.go`:

```go
// Command hello is a gonixgo integration fixture: a main package, a local
// library and third-party dependencies.
package main

import (
	"fmt"

	"github.com/fatih/color"

	"example.com/hello/internal/greet"
)

func main() {
	color.NoColor = true
	fmt.Println(color.GreenString(greet.Greeting("gonixgo")))
}
```

`tests/fixtures/hello-deps/internal/greet/greet.go`:

```go
// Package greet builds greetings.
package greet

// Greeting returns a greeting for name.
func Greeting(name string) string {
	return "hello, " + name
}
```

Generate `go.sum` and the indirect requirements:

```bash
nix develop --command sh -c 'cd tests/fixtures/hello-deps && go mod tidy && go run .'
```

Expected: `hello, gonixgo`. `go.mod` gains indirect requirements on `github.com/mattn/go-colorable`, `github.com/mattn/go-isatty` and `golang.org/x/sys`, and `go.sum` exists.

- [ ] **Step 2: Write the fixture set and the driver**

`tests/fixtures.nix`:

```nix
# The integration fixtures, built with the goEnv under test.
{ goEnv }:
{
  hello-deps = goEnv.buildGoApplication {
    pname = "hello-deps";
    src = ./fixtures/hello-deps;
  };
}
```

In `flake.nix`, replace the `legacyPackages` output with:

```nix
      legacyPackages = eachSystem (system: pkgs:
        let goEnv = mkGoEnv { inherit pkgs; };
        in {
          inherit goEnv;
          fixtures = import ./tests/fixtures.nix { inherit goEnv; };
        });
```

`tests/run.sh`:

```bash
#!/usr/bin/env bash
# Integration tests: build the fixtures with real nix and check the results.
# They need builtins.exec and, on the first run, the network, so they run
# outside the Nix sandbox.
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
flake="path:$root"
exec_opt=(--option allow-unsafe-native-code-during-evaluation true)

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

# build <attribute under legacyPackages.<system>>: prints the output path.
build() {
  nix build "${exec_opt[@]}" --no-link --print-out-paths "$flake#$1"
}

# check_run <fixture> <binary> <expected stdout>
check_run() {
  local out got
  out="$(build "fixtures.$1")"
  got="$("$out/bin/$2")"
  [ "$got" = "$3" ] || fail "$1: $2 printed '$got', want '$3'"
  echo "ok: $1: $2 runs"
}

# check_modinfo <fixture> <binary> <main package, relative to the fixture>
# The module info must match what `go build -trimpath` embeds.
check_modinfo() {
  local out go tmp
  out="$(build "fixtures.$1")"
  go="$(build "fixtures.$1.go")/bin/go"
  tmp="$(mktemp -d)"
  (cd "$root/tests/fixtures/$1" &&
    GOFLAGS=-mod=readonly GOWORK=off GOTOOLCHAIN=local \
      "$go" build -trimpath -buildvcs=false -o "$tmp/ref" "$3")
  if ! diff <("$go" version -m "$out/bin/$2" | tail -n +2) <("$go" version -m "$tmp/ref" | tail -n +2); then
    rm -rf "$tmp"
    fail "$1: $2 module info differs from go build -trimpath (left: gonixgo, right: go build)"
  fi
  rm -rf "$tmp"
  echo "ok: $1: $2 module info matches go build"
}

check_run hello-deps hello "hello, gonixgo"
check_modinfo hello-deps hello .

echo "all integration checks passed"
```

```bash
chmod +x tests/run.sh
```

- [ ] **Step 3: Run the driver to verify it fails**

Run: `tests/run.sh`
Expected: FAIL during evaluation with `attribute 'buildGoApplication' missing`.

- [ ] **Step 4: Write the builders**

`nix/builders.nix`:

```nix
# The builder functions the generated graph calls. Each defines how one
# kind of node becomes a derivation. The wiring between nodes is in the
# graph `gonixgo resolve` prints.
{ lib, cacert, go, tool, stdlib, system, goos, goarch }:

# Per-application settings.
{ srcStr, cgoEnabled, ldflags }:
let
  goBin = "${go}/bin/go";
  builder = "${tool}/bin/gonixgo";
  std = stdlib cgoEnabled;

  # Every directory on the way to a file: "a/b/c.go" gives [ "a" "a/b" ].
  parents = file:
    let parts = lib.splitString "/" file;
    in lib.genList (i: lib.concatStringsSep "/" (lib.take (i + 1) parts)) (lib.length parts - 1);
in
{
  # A fixed-output derivation holding a module's source tree. resolve adds
  # the tree to the store under this exact path during evaluation, so the
  # builder runs only where that did not happen.
  fetchModule = { name, path, version, hash }:
    derivation {
      inherit name system builder;
      args = [ "fetch" ];
      manifest = builtins.toJSON {
        go = goBin;
        inherit path version;
      };
      outputHashMode = "recursive";
      outputHashAlgo = "sha256";
      outputHash = hash;
      impureEnvVars = lib.fetchers.proxyImpureEnvVars ++ [ "GOPROXY" "NETRC" ];
      SSL_CERT_FILE = "${cacert}/etc/ssl/certs/ca-bundle.crt";
    };

  # A store copy of the source holding exactly `files`, which are relative
  # to the source root. A change to any other file leaves it untouched.
  localDir = { name, files }:
    let
      keepFile = lib.genAttrs files (_: true);
      keepDir = lib.genAttrs (lib.concatMap parents files) (_: true);
    in
    builtins.path {
      inherit name;
      path = srcStr;
      filter = path: type:
        let rel = lib.removePrefix "${srcStr}/" path;
        in if type == "directory" then keepDir ? ${rel} else keepFile ? ${rel};
    };

  # One package. Pure Go needs no stdenv: the tool is the builder.
  compile =
    { name, importPath, src, subdir, trimTo, lang, isMain, goFiles, sFiles, embed, deps, ... }:
    derivation {
      inherit name system builder;
      args = [ "compile" ];
      __structuredAttrs = true;
      manifest = {
        go = goBin;
        inherit goos goarch importPath isMain trimTo lang goFiles sFiles embed;
        srcDir = if subdir == "" then "${src}" else "${src}/${subdir}";
        importcfgs = [ "${std}/importcfg" ] ++ map (dep: "${dep}/importcfg") deps;
      };
    };

  # One binary.
  link = { name, binName, main, deps, modinfo, godebug }:
    derivation {
      inherit name system builder;
      args = [ "link" ];
      __structuredAttrs = true;
      manifest = {
        go = goBin;
        inherit goos goarch binName modinfo godebug ldflags;
        main = "${main}/pkg.a";
        importcfgs = [ "${std}/importcfg" "${main}/importcfg" ]
          ++ map (dep: "${dep}/importcfg") deps;
      };
    };
}
```

- [ ] **Step 5: Write `buildGoApplication`**

`nix/build-go-application.nix`:

```nix
# buildGoApplication resolves the package graph during evaluation, through
# builtins.exec, and turns it into derivations.
{ lib, runCommand, go, evalGo, evalTool, mkBuilders, goos, goarch }:

{ pname
, version ? null
, src
, modRoot ? "."
, subPackages ? [ "." ]
, tags ? [ ]
, ldflags ? [ ]
, CGO_ENABLED ? null
, doCheck ? true
, checkFlags ? [ ]
, packageOverrides ? { }
, meta ? { }
}:
let
  exec = builtins.exec or (throw ''
    gonixgo runs `gonixgo resolve` during evaluation and needs builtins.exec.
    Enable it in one of these ways:
      nix build --option allow-unsafe-native-code-during-evaluation true ...
      NIX_CONFIG="allow-unsafe-native-code-during-evaluation = true" nix build ...
      allow-unsafe-native-code-during-evaluation = true    (in nix.conf)
    A flake's nixConfig cannot enable it.
  '');

  srcStr = "${src}";

  # Nix builds the tool and Go before running this, if they are missing.
  graphFn = exec [
    "${evalTool}/bin/gonixgo"
    "resolve"
    (builtins.toJSON {
      go = "${evalGo}/bin/go";
      src = srcStr;
      inherit (builtins) storeDir;
      inherit modRoot subPackages tags goos goarch doCheck;
      cgoEnabled =
        if CGO_ENABLED == null then null
        else builtins.elem CGO_ENABLED [ 1 "1" true ];
    })
  ];

  # The builders need the cgo setting the resolver settled on. It is a
  # literal in the graph, so laziness ties the knot.
  graph = graphFn (mkBuilders {
    inherit srcStr ldflags;
    inherit (graph) cgoEnabled;
  });

  checked =
    assert lib.assertMsg (graph.goVersion == go.version)
      "gonixgo: evaluation resolved with Go ${graph.goVersion} but the build uses Go ${go.version}";
    graph;
in
runCommand (if version == null then pname else "${pname}-${version}")
{
  inherit meta;
  passthru = {
    inherit go;
    graph = checked;
    inherit (checked) modules packages bins;
    tests = { };
  };
}
  ''
    mkdir -p $out/bin
    ${lib.concatMapStringsSep "\n" (bin: "cp ${bin}/bin/* $out/bin/") (lib.attrValues checked.bins)}
  ''
```

- [ ] **Step 6: Extend `mkGoEnv`**

Replace `nix/mk-go-env.nix` with:

```nix
# mkGoEnv ties gonixgo to one nixpkgs: that nixpkgs builds the tool and the
# standard library, and performs the Go build.
{ pkgs
, go ? pkgs.buildPackages.go
, evalPkgs ? pkgs.buildPackages
}:
let
  inherit (pkgs) lib;
  buildPkgs = pkgs.buildPackages;

  # Derivations run on the build platform and produce code for the target.
  inherit (pkgs.stdenv.buildPlatform) system;
  goos = go.GOOS;
  goarch = go.GOARCH;

  tool = buildPkgs.callPackage ./tool.nix { };

  # The resolver runs on the machine that evaluates.
  evalTool = evalPkgs.callPackage ./tool.nix { };
  evalGo = evalPkgs.go;

  stdlib = import ./stdlib.nix {
    inherit lib go goos goarch;
    inherit (buildPkgs) runCommand runCommandCC;
  };

  builders = import ./builders.nix {
    inherit lib go tool stdlib system goos goarch;
    inherit (buildPkgs) cacert;
  };

  buildGoApplication = import ./build-go-application.nix {
    inherit lib go evalGo evalTool goos goarch;
    inherit (buildPkgs) runCommand;
    mkBuilders = builders;
  };
in
assert lib.assertMsg (evalGo.version == go.version)
  "gonixgo: evalPkgs has Go ${evalGo.version} but the build uses Go ${go.version}; they must be the same version";
{
  inherit buildGoApplication tool go stdlib builders;
}
```

- [ ] **Step 7: Run the driver to verify it passes**

Run: `tests/run.sh`
Expected:

```
ok: hello-deps: hello runs
ok: hello-deps: hello module info matches go build
all integration checks passed
```

The first run builds the tool, the standard library and eight derivations for the fixture: `gopkg-golang.org-x-sys-unix-v0.25.0`, `gopkg-github.com-mattn-go-isatty-v0.0.20`, `gopkg-github.com-mattn-go-colorable-v0.1.13`, `gopkg-github.com-fatih-color-v1.18.0`, `golocal-example.com-hello-internal-greet`, `golocal-example.com-hello`, `gobin-hello` and the `hello-deps` application. It builds no `gomod-…` derivation, because resolve pre-seeded all four modules.

If a step fails, inspect the pieces:

```bash
# The graph resolve prints for the fixture.
nix develop --command go run ./cmd/gonixgo resolve \
  '{"go":"go","src":"'"$PWD"'/tests/fixtures/hello-deps","storeDir":"/nix/store","modRoot":".","subPackages":["."],"cgoEnabled":null}'
# One package's derivation and its build log.
nix build --option allow-unsafe-native-code-during-evaluation true --no-link -L \
  "path:$PWD#fixtures.hello-deps.packages.\"github.com/fatih/color\""
```

- [ ] **Step 8: Verify the modules were pre-seeded, not fetched**

```bash
nix eval --raw --option allow-unsafe-native-code-during-evaluation true \
  "path:$PWD#fixtures.hello-deps.modules.\"github.com/fatih/color@v1.18.0\".outPath" | xargs ls
nix log "$(nix eval --raw --option allow-unsafe-native-code-during-evaluation true \
  "path:$PWD#fixtures.hello-deps.modules.\"github.com/fatih/color@v1.18.0\".drvPath")" 2>&1 | head -3
```

Expected: the first command lists the module's files (`color.go`, `go.mod`, …). The second reports that no build log is available, because the fetch derivation never ran.

- [ ] **Step 9: Commit**

```bash
git add nix flake.nix tests
git status --short   # no result symlinks, no binaries
git commit -m "feat(nix): add builders, buildGoApplication and the hello-deps fixture" -m "$TRAILERS"
```

---

### Task 15: The second fixture, rebuild and error checks, and the README

**Files:**
- Create: `tests/fixtures/asm-embed/{go.mod,main.go,cmd/second/main.go}`
- Create: `tests/fixtures/asm-embed/internal/add/{add_asm.go,add_generic.go,add_amd64.s,add_arm64.s}`
- Create: `tests/fixtures/asm-embed/internal/web/{web.go,static/index.html,static/extra.txt,static/_skip.txt}`
- Modify: `tests/fixtures.nix`, `tests/run.sh`
- Create: `README.md`

**Interfaces:**
- Consumes: `buildGoApplication` and its `passthru` (Task 14).
- Produces: the `asm-embed` fixture; `check_incremental` and `check_exec_error` in `tests/run.sh`; user documentation.

- [ ] **Step 1: Write the fixture**

`tests/fixtures/asm-embed/go.mod`:

```
module example.com/asmembed

go 1.24
```

`tests/fixtures/asm-embed/main.go`:

```go
// Command asmembed is a gonixgo integration fixture: assembly, embedded
// files, a value set with -X, and no third-party dependencies.
package main

import (
	"fmt"

	"example.com/asmembed/internal/add"
	"example.com/asmembed/internal/web"
)

var version = "unset"

func main() {
	fmt.Println(add.Add(1, 2), web.Index(), web.Names(), version)
}
```

`tests/fixtures/asm-embed/cmd/second/main.go`:

```go
// Command second is the fixture's second main package.
package main

import "fmt"

func main() {
	fmt.Println("second")
}
```

`tests/fixtures/asm-embed/internal/add/add_asm.go`:

```go
//go:build amd64 || arm64

// Package add adds numbers, in assembly where it can.
package add

// Add returns a + b.
func Add(a, b int64) int64
```

`tests/fixtures/asm-embed/internal/add/add_generic.go`:

```go
//go:build !amd64 && !arm64

// Package add adds numbers, in assembly where it can.
package add

// Add returns a + b.
func Add(a, b int64) int64 { return a + b }
```

`tests/fixtures/asm-embed/internal/add/add_amd64.s`:

```
#include "textflag.h"

// func Add(a, b int64) int64
TEXT ·Add(SB), NOSPLIT, $0-24
	MOVQ a+0(FP), AX
	ADDQ b+8(FP), AX
	MOVQ AX, ret+16(FP)
	RET
```

`tests/fixtures/asm-embed/internal/add/add_arm64.s`:

```
#include "textflag.h"

// func Add(a, b int64) int64
TEXT ·Add(SB), NOSPLIT, $0-24
	MOVD a+0(FP), R0
	MOVD b+8(FP), R1
	ADD R1, R0
	MOVD R0, ret+16(FP)
	RET
```

`tests/fixtures/asm-embed/internal/web/web.go`:

```go
// Package web serves embedded files.
package web

import "embed"

// A directory pattern leaves out names starting with "_" or ".".
//
//go:embed static
var files embed.FS

//go:embed static/index.html
var index string

// Index returns the embedded index page.
func Index() string { return index }

// Names lists the embedded files, sorted.
func Names() []string {
	entries, err := files.ReadDir("static")
	if err != nil {
		panic(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}
```

```bash
mkdir -p tests/fixtures/asm-embed/internal/web/static
printf 'hi' > tests/fixtures/asm-embed/internal/web/static/index.html
printf 'x'  > tests/fixtures/asm-embed/internal/web/static/extra.txt
printf 'no' > tests/fixtures/asm-embed/internal/web/static/_skip.txt
nix develop --command sh -c 'cd tests/fixtures/asm-embed && go vet ./... && go run -ldflags "-X main.version=1.2.3" .'
```

Expected: `3 hi [extra.txt index.html] 1.2.3`

- [ ] **Step 2: Add the fixture and the new checks**

In `tests/fixtures.nix`, add after `hello-deps`:

```nix
  asm-embed = goEnv.buildGoApplication {
    pname = "asm-embed";
    version = "1.2.3";
    src = ./fixtures/asm-embed;
    subPackages = [ "." "cmd/second" ];
    ldflags = [ "-X main.version=1.2.3" ];
  };
```

In `tests/run.sh`, add these functions after `check_modinfo`:

```bash
# check_incremental <fixture> <file to append to> <what must change>
# Evaluates the fixture from two copies that differ in one file and lists
# the nodes whose derivations differ: package import paths, then bin:<name>,
# then mod:<key>.
check_incremental() {
  local a b got
  a="$(mktemp -d)"
  b="$(mktemp -d)"
  cp -R "$root/tests/fixtures/$1/." "$a/"
  cp -R "$root/tests/fixtures/$1/." "$b/"
  printf '\n// edited\n' >>"$b/$2"
  got="$(nix eval --impure --raw "${exec_opt[@]}" --expr "
    let
      goEnv = (builtins.getFlake \"$flake\").legacyPackages.\${builtins.currentSystem}.goEnv;
      build = src: goEnv.buildGoApplication { pname = \"incremental\"; inherit src; };
      a = build $a;
      b = build $b;
      changed = set: builtins.filter
        (n: a.\${set}.\${n}.drvPath != b.\${set}.\${n}.drvPath)
        (builtins.attrNames a.\${set});
    in builtins.concatStringsSep \" \" (
      changed \"packages\"
      ++ map (n: \"bin:\" + n) (changed \"bins\")
      ++ map (n: \"mod:\" + n) (changed \"modules\"))
  ")"
  rm -rf "$a" "$b"
  [ "$got" = "$3" ] || fail "$1: editing $2 changed [$got], want [$3]"
  echo "ok: $1: editing $2 changes [$3]"
}

# Without the option, evaluation must say what to set.
check_exec_error() {
  local msg
  if msg="$(nix build --no-link "$flake#fixtures.hello-deps" 2>&1)"; then
    fail "building without builtins.exec succeeded"
  fi
  case "$msg" in
    *allow-unsafe-native-code-during-evaluation*) echo "ok: missing builtins.exec is explained" ;;
    *) fail "unhelpful error without builtins.exec: $msg" ;;
  esac
}
```

and replace the calls at the bottom with:

```bash
check_run hello-deps hello "hello, gonixgo"
check_modinfo hello-deps hello .

check_run asm-embed asmembed "3 hi [extra.txt index.html] 1.2.3"
check_run asm-embed second "second"
check_modinfo asm-embed asmembed .
check_modinfo asm-embed second ./cmd/second

# A package edit rebuilds it, its importers and the link. Nothing else.
check_incremental hello-deps internal/greet/greet.go \
  "example.com/hello example.com/hello/internal/greet bin:hello"
check_incremental hello-deps main.go "example.com/hello bin:hello"
# A file no package uses changes nothing.
check_incremental hello-deps NOTES.md ""

check_exec_error

echo "all integration checks passed"
```

- [ ] **Step 3: Run the driver**

Run: `tests/run.sh`
Expected:

```
ok: hello-deps: hello runs
ok: hello-deps: hello module info matches go build
ok: asm-embed: asmembed runs
ok: asm-embed: second runs
ok: asm-embed: asmembed module info matches go build
ok: asm-embed: second module info matches go build
ok: hello-deps: editing internal/greet/greet.go changes [example.com/hello example.com/hello/internal/greet bin:hello]
ok: hello-deps: editing main.go changes [example.com/hello bin:hello]
ok: hello-deps: editing NOTES.md changes []
ok: missing builtins.exec is explained
all integration checks passed
```

Every check here exercises code written in earlier tasks, so a failure is a defect in that code. Fix it where it lives and add a unit test there that reproduces it before re-running the driver.

- [ ] **Step 4: Write the README**

`README.md`:

````markdown
# gonixgo

Build Go programs with Nix one package per derivation, with nothing to check
in when `go.mod` changes.

gonixgo follows [go2nix](https://github.com/numtide/go2nix): the standard
library, every module and every package get their own derivation, so an edit
rebuilds the package, the packages that import it, and the link. It differs
in two ways:

- **No Nix plugin.** A Nix-built binary runs during evaluation through
  `builtins.exec` and prints the Nix code for the build.
- **No lockfile.** Module hashes are computed during evaluation from the
  `go.sum`-verified module cache. A Go project commits no Nix code that
  depends on `go.mod` or `go.sum`.

## Use

```nix
{
  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-26.05";
  inputs.gonixgo.url = "github:draganm/gonixgo";

  outputs = { nixpkgs, gonixgo, ... }:
    let
      system = "aarch64-darwin";
      pkgs = nixpkgs.legacyPackages.${system};
      goEnv = gonixgo.lib.mkGoEnv { inherit pkgs; };
    in {
      packages.${system}.default = goEnv.buildGoApplication {
        pname = "app";
        src = ./.;
      };
    };
}
```

```bash
nix build --option allow-unsafe-native-code-during-evaluation true
```

The `pkgs` you pass builds the gonixgo tool and the standard library, and
performs the Go build. Without flakes, `import gonixgo { inherit pkgs; }`
returns the same set as `mkGoEnv`.

### `buildGoApplication`

| Argument | Default | Meaning |
|---|---|---|
| `pname` | required | Derivation name. |
| `version` | `null` | Appended to the derivation name. |
| `src` | required | The source tree. |
| `modRoot` | `"."` | Directory of `go.mod` inside `src`. |
| `subPackages` | `[ "." ]` | Main packages to build, relative to `modRoot`. |
| `tags` | `[ ]` | Build tags. |
| `ldflags` | `[ ]` | Linker flags, split as `go build -ldflags` splits them. |
| `CGO_ENABLED` | `null` | `null` uses Go's default for the target. |

Binaries land in `$out/bin`, named as `go build` names them. The result's
`passthru` has `packages`, `modules` and `bins`, each a set of derivations,
so one package can be built alone:

```bash
nix build --option allow-unsafe-native-code-during-evaluation true \
  '.#default.packages."example.com/app/internal/web"'
```

## What evaluation needs

- `allow-unsafe-native-code-during-evaluation = true`, from `--option`,
  `NIX_CONFIG` or `nix.conf`. A flake's `nixConfig` cannot set it.
- The project's modules: `go list` runs during evaluation with your
  `GOMODCACHE`, `GOPROXY`, `GOPRIVATE` and `NETRC`, and downloads what is
  missing.
- Import-from-derivation (on by default): the tool and Go are built during
  evaluation the first time.

Module sources are added to the Nix store during evaluation, so nothing is
downloaded twice and private modules need no credentials in the store. A
module is fetched by a derivation only when the build happens on a machine
that did not evaluate it; that fetch uses `GOPROXY` and cannot reach
repositories that need `git`.

## Not yet supported

cgo, tests (`doCheck` is accepted and ignored), `replace` directives,
cross-compilation, `go.work`, and `vendor/` directories. Packages that need
one of the first three are rejected during evaluation with a message naming
them.

## Development

```bash
nix develop --command go test ./...   # unit tests
tests/run.sh                          # integration tests: real nix builds
```

The design is in `docs/superpowers/specs/2026-10-03-gonixgo-design.md`.
````

- [ ] **Step 5: Run everything**

```bash
nix develop --command go test ./...
nix develop --command go vet ./...
nix develop --command gofmt -l cmd internal
tests/run.sh
nix flake check "path:$PWD"
git status --short
```

Expected: every Go package `ok`; no output from `go vet` or `gofmt -l`; the driver ends with `all integration checks passed`; the flake check succeeds; `git status` shows only the new fixture files, `tests/fixtures.nix`, `tests/run.sh` and `README.md`, with no `result` symlink and no binary.

- [ ] **Step 6: Commit**

```bash
git add tests README.md
git commit -m "test: add the asm-embed fixture, rebuild and error checks; add README" -m "$TRAILERS"
```

