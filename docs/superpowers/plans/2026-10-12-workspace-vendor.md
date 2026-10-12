# Workspace and Vendoring Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build projects that use `go.work` or `vendor/` as `go build` builds them: same packages, same recorded source paths, same module info.

**Architecture:** The resolver stops forcing `GOWORK=off` and `-mod=readonly`: it sets `GOWORK` to the `go.work` found at `modRoot` or above it inside `src`, and leaves `-mod` to Go. The graph classifies what `go list` reports: every workspace module is a main module and builds from `src`; a non-main module without a directory is vendored and builds from `vendor/` inside `src`. Module info lists other main modules as `(devel)` and records no checksums for vendored modules; a vendored cgo package maps its vendor directory to `/_/vendor`.

**Tech Stack:** Go (module `go 1.23`; tests use nixpkgs' Go 1.26.7), Nix 2.26.1, nixpkgs `nixos-26.05`, bash for `tests/run.sh`.

**Spec:** `docs/superpowers/specs/2026-10-12-workspace-vendor-design.md`

## Global Constraints

- Branch `workspace-vendor`. Each commit message is conventional (`feat(graph): …`, `test: …`, `docs: …`) and ends with:
  ```
  Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
  Claude-Session: https://claude.ai/code/session_01AdA5UEs8Aiwy6pwwGeaNzo
  ```
- Remove every binary you generate; probe builds go to a `mktemp -d` directory that is deleted.
- `buildGoApplication` arguments: `useGoWork ? true`, `useVendor ? true`. Resolver JSON: `goWork` and `vendor`, both false when absent (module mode with `-mod=readonly`, as before).
- Messages, verbatim:
  - `modRoot "<modRoot>": no go.mod in <dir>, and no go.work in it or above it inside src` (the part after the comma only when `goWork` is on)
  - `gonixgo used the workspace <go.work path>; set useGoWork = false to build without it`
  - `gonixgo let go use vendor/; set useVendor = false to fetch modules instead`
  - `<import path>: vendored, but its directory <dir> is not under a vendor directory inside src <src>`
- A vendored package's compile is named `gopkg-<import path>-<version>`, the version being the replacement's for a module replaced by another version and the required one otherwise.
- Unit tests: `go test ./...`. Integration: `tests/run.sh`, or `tests/run.sh <function> [args]` for one check or section.

## Review Focus

1. A dependency only a main-module test imports, in vendor mode, builds from `vendor/`: Task 4, `TestRunVendored`.
2. The workspace root, which has no `go.mod`, works as `modRoot` with `subPackages` naming another directory: Task 4, `TestRunWorkspaceRoot`, and Task 5, `check_workspace_root`.
3. A checksum held only by `go.work.sum` or another module's `go.sum` reaches module info: Task 4, `TestReadSumsWorkspace`, and Task 5's module info check.
4. A package `go list` calls vendored but whose directory is not under a `vendor` directory inside `src` is an error, not a wrong trim path: Task 2, `TestBuildVendoredOutsideVendor`.

---

### Task 1: `go list` options for workspaces and vendoring

**Files:**
- Modify: `internal/golist/golist.go:76-160` (`Options`, `environWith`; new `WorkUses`)
- Test: `internal/golist/golist_test.go`

**Interfaces:**
- Produces:
  - `golist.Options.GOWORK string` (the `go.work` to use, `""` for `GOWORK=off`) and `golist.Options.IgnoreVendor bool` (`GOFLAGS=-mod=readonly` when true, empty otherwise).
  - `func WorkUses(o Options, path string) ([]string, error)`: the directories the `go.work` at `path` uses, absolute.

- [ ] **Step 1: Write the failing tests**

Append to `internal/golist/golist_test.go`:

```go
// The go.work to use and whether to ignore vendor/ reach every go command.
func TestOptionsWorkspaceAndVendor(t *testing.T) {
	dir := testutil.WriteTree(t, appFiles)
	work := filepath.Join(dir, "go.work")
	if err := os.WriteFile(work, []byte("go 1.22\n\nuse .\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	o := Options{Go: testutil.Go(t), Dir: dir}
	env, err := Env(o, "GOWORK", "GOFLAGS")
	if err != nil {
		t.Fatal(err)
	}
	if env["GOWORK"] != "off" || env["GOFLAGS"] != "" {
		t.Errorf("by default go env = %v, want GOWORK off and GOFLAGS empty", env)
	}
	o.GOWORK, o.IgnoreVendor = work, true
	if env, err = Env(o, "GOWORK", "GOFLAGS"); err != nil {
		t.Fatal(err)
	}
	if env["GOWORK"] != work || env["GOFLAGS"] != "-mod=readonly" {
		t.Errorf("go env = %v, want GOWORK %s and GOFLAGS -mod=readonly", env, work)
	}
}

// WorkUses gives the directories a go.work uses, as absolute paths.
func TestWorkUses(t *testing.T) {
	dir := testutil.WriteTree(t, map[string]string{
		"go.work":    "go 1.22\n\nuse (\n\t./app\n\t./lib\n)\n",
		"app/go.mod": "module example.com/app\n\ngo 1.22\n",
		"lib/go.mod": "module example.com/lib\n\ngo 1.22\n",
	})
	work := filepath.Join(dir, "go.work")
	got, err := WorkUses(Options{Go: testutil.Go(t), Dir: dir, GOWORK: work}, work)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{filepath.Join(dir, "app"), filepath.Join(dir, "lib")}; !reflect.DeepEqual(got, want) {
		t.Errorf("WorkUses = %v, want %v", got, want)
	}
}
```

In `TestEnvIgnoresCallerBuildConfiguration`, the zero `Options` no longer pins `-mod=readonly`: change its expectation map to

```go
	for k, want := range map[string]string{"GOFLAGS": "", "GOWORK": "off", "GOTOOLCHAIN": "local"} {
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/golist/`
Expected: the build fails with `unknown field GOWORK in struct literal of type Options` and `undefined: WorkUses`.

- [ ] **Step 3: Implement**

In `internal/golist/golist.go`, add to `Options` after `GOCACHE`:

```go
	GOWORK       string // the go.work to use, "" for none (GOWORK=off)
	IgnoreVendor bool   // ignore vendor/ (-mod=readonly) instead of letting go choose
```

In `environWith`, start `set` with `"GOFLAGS": ""` instead of `"GOFLAGS": "-mod=readonly"`, keep `"GOWORK": "off"`, and after the loop over `extra` add:

```go
	if o.IgnoreVendor {
		set["GOFLAGS"] = "-mod=readonly"
	}
	if o.GOWORK != "" {
		set["GOWORK"] = o.GOWORK
	}
```

Add after `Env`:

```go
// WorkUses returns the directories the go.work at path uses, absolute.
func WorkUses(o Options, path string) ([]string, error) {
	out, err := o.command("work", "edit", "-json", path).Output()
	if err != nil {
		return nil, fmt.Errorf("go work edit -json %s: %w", path, err)
	}
	var work struct{ Use []struct{ DiskPath string } }
	if err := json.Unmarshal(out, &work); err != nil {
		return nil, fmt.Errorf("decoding go work edit output: %w", err)
	}
	dirs := make([]string, 0, len(work.Use))
	for _, u := range work.Use {
		dir := filepath.FromSlash(u.DiskPath)
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(filepath.Dir(path), dir)
		}
		dirs = append(dirs, dir)
	}
	return dirs, nil
}
```

Add `"encoding/json"` and `"path/filepath"` to the imports if missing.

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/golist/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/golist/
git commit -q -F - <<'EOF'
feat(golist): take the go.work to use and whether to ignore vendor/

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01AdA5UEs8Aiwy6pwwGeaNzo
EOF
```

---

### Task 2: Vendored packages and workspace modules in the graph

**Files:**
- Modify: `internal/graph/pkg.go` (`newPackage`; new `fromSrc`, `outside`, `builtVersion`)
- Modify: `internal/graph/graph.go` (`Package.Local` comment, `Package.VendorDir`, `Graph.DepModules` comment, `addDepModule`, `newBinary`)
- Create: `internal/graph/workvendor_test.go`

**Interfaces:**
- Produces:
  - `Package.VendorDir string`: a vendored package's vendor directory relative to the source root, `""` otherwise.
  - Vendored package rule: `!m.Main && m.Dir == ""`.
  - Module info: main modules recorded as `(devel)` without sum; vendored modules without checksums; a binary leaves out its own module.

- [ ] **Step 1: Write the failing tests**

Create `internal/graph/workvendor_test.go`:

```go
package graph

import (
	"reflect"
	"strings"
	"testing"

	"github.com/draganm/gonixgo/internal/golist"
)

// The program example.com/vendored in /src builds its dependencies from
// /src/vendor. go list reports a vendored module without Dir.
var (
	vendoredMain = &golist.Module{Path: "example.com/vendored", Main: true, Dir: "/src", GoVersion: "1.22"}
	cmpVendored  = &golist.Module{Path: "github.com/google/go-cmp", Version: "v0.6.0", GoVersion: "1.13"}
	// cdepVendored is replaced by the directory ./cdep, and vendored.
	cdepVendored = &golist.Module{Path: "example.com/cdep", Version: "v0.0.0-00010101000000-000000000000", GoVersion: "1.21",
		Replace: &golist.Module{Path: "./cdep", Dir: "/src/cdep", GoVersion: "1.21"}}
	// forkVendored is replaced by another version, and vendored.
	forkVendored = &golist.Module{Path: "github.com/a/b", Version: "v1.0.0", GoVersion: "1.20",
		Replace: &golist.Module{Path: "github.com/fork/b", Version: "v1.0.1", GoVersion: "1.20"}}

	// go.sum's checksums, which vendor mode leaves out of module info.
	vendoredSums = map[string]string{"github.com/google/go-cmp@v0.6.0": "h1:cmp6", "github.com/fork/b@v1.0.1": "h1:fork"}
)

func vendoredPackages() []golist.Package {
	return []golist.Package{
		std("fmt"),
		{ImportPath: "github.com/google/go-cmp/cmp", Name: "cmp", Dir: "/src/vendor/github.com/google/go-cmp/cmp",
			Module: cmpVendored, DepOnly: true, GoFiles: []string{"compare.go"}},
		{ImportPath: "example.com/cdep", Name: "cdep", Dir: "/src/vendor/example.com/cdep", Module: cdepVendored, DepOnly: true,
			CgoFiles: []string{"cdep.go"}},
		{ImportPath: "github.com/a/b/c", Name: "c", Dir: "/src/vendor/github.com/a/b/c", Module: forkVendored, DepOnly: true,
			GoFiles: []string{"c.go"}},
		{ImportPath: "example.com/vendored", Name: "main", Dir: "/src", Module: vendoredMain, GoFiles: []string{"main.go"},
			Imports: []string{"example.com/cdep", "fmt", "github.com/a/b/c", "github.com/google/go-cmp/cmp"}},
	}
}

func buildVendored(t *testing.T, pkgs []golist.Package) *Graph {
	t.Helper()
	g, err := Build(Input{Packages: pkgs, Src: "/src", Env: testEnv, Sums: vendoredSums})
	if err != nil {
		t.Fatal(err)
	}
	return g
}

// A vendored package builds from src, from its directory under vendor/,
// and nothing is fetched.
func TestBuildVendoredPackage(t *testing.T) {
	g := buildVendored(t, vendoredPackages())
	want := &Package{
		ImportPath: "github.com/google/go-cmp/cmp", Name: "gopkg-github.com-google-go-cmp-cmp-v0.6.0",
		SrcName: "gosrc-github.com-google-go-cmp-cmp", Local: true, ModulePath: "github.com/google/go-cmp",
		Subdir: "vendor/github.com/google/go-cmp/cmp", TrimTo: "github.com/google/go-cmp@v0.6.0/cmp", Lang: "go1.13",
		GoFiles: []string{"compare.go"}, SrcFiles: []string{"vendor/github.com/google/go-cmp/cmp/compare.go"},
		VendorDir: "vendor",
	}
	if got := g.Packages["github.com/google/go-cmp/cmp"]; !reflect.DeepEqual(got, want) {
		t.Errorf("cmp =\n%+v\nwant\n%+v", got, want)
	}
	if len(g.Modules) != 0 {
		t.Errorf("modules = %v, want none", g.Modules)
	}
}

// Replaced modules are vendored too, a directory replacement included.
func TestBuildVendoredReplacements(t *testing.T) {
	g := buildVendored(t, vendoredPackages())
	cdep := g.Packages["example.com/cdep"]
	if !cdep.Local || cdep.Subdir != "vendor/example.com/cdep" || cdep.VendorDir != "vendor" || cdep.Cgo == nil ||
		cdep.Name != "gopkg-example.com-cdep-v0.0.0-00010101000000-000000000000" ||
		cdep.TrimTo != "example.com/cdep@v0.0.0-00010101000000-000000000000" {
		t.Errorf("cdep = %+v", cdep)
	}
	fork := g.Packages["github.com/a/b/c"]
	if !fork.Local || fork.ModuleKey != "" || fork.Name != "gopkg-github.com-a-b-c-v1.0.1" || fork.TrimTo != "github.com/a/b@v1.0.0/c" {
		t.Errorf("fork package = %+v", fork)
	}
}

// Vendor mode records no checksums.
func TestBuildVendoredModuleInfo(t *testing.T) {
	g := buildVendored(t, vendoredPackages())
	want := "path\texample.com/vendored\n" +
		"mod\texample.com/vendored\t(devel)\t\n" +
		"dep\texample.com/cdep\tv0.0.0-00010101000000-000000000000\n" +
		"=>\t./cdep\t(devel)\t\n\n" +
		"dep\tgithub.com/a/b\tv1.0.0\n" +
		"=>\tgithub.com/fork/b\tv1.0.1\t\n\n" +
		"dep\tgithub.com/google/go-cmp\tv0.6.0\t\n" +
		"build\t-buildmode=exe\n"
	if got := g.Bins[0].Modinfo; !strings.HasPrefix(got, want) {
		t.Errorf("module info =\n%s\nwant it to start with\n%s", got, want)
	}
}

// A package go list calls vendored must be under a vendor directory
// inside src.
func TestBuildVendoredOutsideVendor(t *testing.T) {
	for _, dir := range []string{"/src/third_party/github.com/google/go-cmp/cmp", "/elsewhere/vendor/github.com/google/go-cmp/cmp"} {
		pkgs := vendoredPackages()
		pkgs[1].Dir = dir
		_, err := Build(Input{Packages: pkgs, Src: "/src", Env: testEnv})
		want := "github.com/google/go-cmp/cmp: vendored, but its directory " + dir + " is not under a vendor directory inside src /src"
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("dir %s: err = %v, want %q", dir, err, want)
		}
	}
}

// The workspace /src holds three modules. The programs app and tool share
// lib, and each imports a package of the other's module.
var (
	wsApp  = &golist.Module{Path: "example.com/work/app", Main: true, Dir: "/src/app", GoVersion: "1.22"}
	wsTool = &golist.Module{Path: "example.com/work/tool", Main: true, Dir: "/src/tool", GoVersion: "1.22"}
	wsLib  = &golist.Module{Path: "example.com/work/lib", Main: true, Dir: "/src/lib", GoVersion: "1.21"}
)

func workspacePackages() []golist.Package {
	return []golist.Package{
		std("fmt"),
		{ImportPath: "example.com/work/lib", Name: "lib", Dir: "/src/lib", Module: wsLib, DepOnly: true,
			GoFiles: []string{"lib.go"}, TestGoFiles: []string{"lib_test.go"}},
		{ImportPath: "example.com/work/app/util", Name: "util", Dir: "/src/app/util", Module: wsApp, DepOnly: true,
			GoFiles: []string{"util.go"}},
		{ImportPath: "example.com/work/tool/shared", Name: "shared", Dir: "/src/tool/shared", Module: wsTool, DepOnly: true,
			GoFiles: []string{"shared.go"}},
		{ImportPath: "example.com/work/app", Name: "main", Dir: "/src/app", Module: wsApp, GoFiles: []string{"main.go"},
			Imports: []string{"example.com/work/lib", "example.com/work/tool/shared", "fmt"}},
		{ImportPath: "example.com/work/tool", Name: "main", Dir: "/src/tool", Module: wsTool, GoFiles: []string{"main.go"},
			Imports: []string{"example.com/work/app/util", "example.com/work/lib"}},
	}
}

// Workspace modules build from src with their own go directive and their
// import paths as trim paths, and their tests run.
func TestBuildWorkspacePackages(t *testing.T) {
	g := build(t, workspacePackages())
	lib := g.Packages["example.com/work/lib"]
	if !lib.Local || lib.Name != "golocal-example.com-work-lib" || lib.Subdir != "lib" ||
		lib.TrimTo != "example.com/work/lib" || lib.Lang != "go1.21" {
		t.Errorf("lib = %+v", lib)
	}
	if want := []string{"example.com/work/lib"}; !reflect.DeepEqual(g.Tested, want) {
		t.Errorf("Tested = %v, want %v", g.Tested, want)
	}
}

// Each program lists the other workspace modules it uses as (devel), and
// not its own.
func TestBuildWorkspaceModuleInfo(t *testing.T) {
	g := build(t, workspacePackages())
	want := map[string]string{
		"app": "path\texample.com/work/app\nmod\texample.com/work/app\t(devel)\t\n" +
			"dep\texample.com/work/lib\t(devel)\t\ndep\texample.com/work/tool\t(devel)\t\nbuild\t",
		"tool": "path\texample.com/work/tool\nmod\texample.com/work/tool\t(devel)\t\n" +
			"dep\texample.com/work/app\t(devel)\t\ndep\texample.com/work/lib\t(devel)\t\nbuild\t",
	}
	if len(g.Bins) != 2 {
		t.Fatalf("%d binaries, want 2", len(g.Bins))
	}
	for _, bin := range g.Bins {
		if !strings.HasPrefix(bin.Modinfo, want[bin.Name]) {
			t.Errorf("%s's module info =\n%s\nwant it to start with\n%s", bin.Name, bin.Modinfo, want[bin.Name])
		}
	}
}
```

`TestBuildWorkspacePackages` already passes: it pins what workspace mode relies on.

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/graph/`
Expected: the build fails with `unknown field VendorDir in struct literal of type Package`.

- [ ] **Step 3: Vendored packages in `newPackage`**

In `internal/graph/pkg.go`, replace the part of `newPackage` from the comment `// The main module, and a module replaced by a directory, build from` up to (not including) `// Any other module is fetched:` with:

```go
	switch {
	case !m.Main && m.Dir == "":
		// Vendored: go list reports no directory for the module, whose
		// packages are under vendor/. They build from src.
		vendor := strings.TrimSuffix(p.Dir, string(filepath.Separator)+filepath.FromSlash(p.ImportPath))
		rel, err := filepath.Rel(src, vendor)
		if vendor == p.Dir || filepath.Base(vendor) != "vendor" || err != nil || outside(rel) {
			return nil, nil, fmt.Errorf("%s: vendored, but its directory %s is not under a vendor directory inside src %s", p.ImportPath, p.Dir, src)
		}
		pkg.Name = storepath.SanitizeName("gopkg-" + p.ImportPath + "-" + builtVersion(m))
		pkg.VendorDir = slashDir(rel)
		if err := fromSrc(pkg, p, src, stat); err != nil {
			return nil, nil, err
		}
		return pkg, nil, nil
	case m.Main || m.Replace != nil && m.Replace.Version == "":
		// The main modules, and a module replaced by a directory, build
		// from src.
		if rel, err := filepath.Rel(src, p.Dir); err != nil || outside(rel) {
			if !m.Main {
				return nil, nil, fmt.Errorf("replace %s => %s: %s is outside src %s", m.Path, m.Replace.Path, m.Replace.Dir, src)
			}
			return nil, nil, fmt.Errorf("%s: directory %s is outside src %s", p.ImportPath, p.Dir, src)
		}
		pkg.Name = storepath.SanitizeName("golocal-" + p.ImportPath)
		if err := fromSrc(pkg, p, src, stat); err != nil {
			return nil, nil, err
		}
		return pkg, nil, nil
	}

```

In the fetched part below, replace the condition
`if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {` with
`if err != nil || outside(rel) {`.

Add after `newPackage`:

```go
// fromSrc sets up pkg, the node of p, to build from a filtered copy of src.
func fromSrc(pkg *Package, p *golist.Package, src string, stat func(string) (isDir, exists bool)) error {
	rel, err := filepath.Rel(src, p.Dir)
	if err != nil || outside(rel) {
		return fmt.Errorf("%s: directory %s is outside src %s", p.ImportPath, p.Dir, src)
	}
	pkg.Local = true
	pkg.SrcName = storepath.SanitizeName("gosrc-" + p.ImportPath)
	pkg.Subdir = slashDir(rel)
	pkg.SrcFiles = srcFiles(pkg.Subdir, p)
	if pkg.Cgo != nil {
		return addNamedPaths(pkg, p, src, stat)
	}
	return nil
}

// outside reports whether rel, a path relative to some directory, leaves
// that directory.
func outside(rel string) bool {
	return rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// builtVersion is the version of module m whose files are built: the
// replacement's for a module replaced by another version, the required
// one otherwise.
func builtVersion(m *golist.Module) string {
	if m.Replace != nil && m.Replace.Version != "" {
		return m.Replace.Version
	}
	return m.Version
}
```

- [ ] **Step 4: Module info in `graph.go`**

In `internal/graph/graph.go`, change the `Local` field's comment and add `VendorDir` after `Subdir`:

```go
	Local      bool   // built from src: a main module's package, a directory replacement's, or a vendored one
```

```go
	VendorDir  string // vendored: the vendor directory relative to the source root, "" otherwise
```

Replace the `DepModules` comment with:

```go
	// DepModules are the module info entries of the modules that provide
	// the program's packages, by the path they are required under. A
	// binary leaves out its own module.
```

Replace `addDepModule` with:

```go
// addDepModule records the module info entry of m, a module that provides
// a package: its path and version as required, and for a replaced module
// the replacement's path, version and sum. A main module, which only a
// workspace has more than one of, shows as (devel), as does a directory
// replacement. Vendor mode records no checksums.
func (g *Graph) addDepModule(m *golist.Module, sums map[string]string) {
	d := modinfo.Module{Path: m.Path, Version: m.Version}
	vendored := !m.Main && m.Dir == ""
	switch r := m.Replace; {
	case m.Main:
		d.Version = "(devel)"
	case r == nil:
		if !vendored {
			d.Sum = sums[m.Path+"@"+m.Version]
		}
	case r.Version == "":
		d.Replace = &modinfo.Module{Path: r.Path, Version: "(devel)"}
	default:
		d.Replace = &modinfo.Module{Path: r.Path, Version: r.Version}
		if !vendored {
			d.Replace.Sum = sums[r.Path+"@"+r.Version]
		}
	}
	g.DepModules[m.Path] = d
}
```

In `newBinary`, make the loop skip the binary's own module:

```go
		if !ok || seen[path] || path == p.Module.Path {
			continue
		}
```

Run `gofmt -w internal/graph/`.

- [ ] **Step 5: Run the tests**

Run: `go test ./internal/graph/`
Expected: PASS, the existing tests included.

- [ ] **Step 6: Commit**

```bash
git add internal/graph/
git commit -q -F - <<'EOF'
feat(graph): build vendored packages from src and list workspace modules

A non-main module without a directory is vendored: its packages build
from vendor/ inside src, and module info records no checksums for it.
Every main module gets a module info entry, so a workspace's other
modules appear as (devel), and a binary leaves out its own.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01AdA5UEs8Aiwy6pwwGeaNzo
EOF
```

---

### Task 3: Vendored cgo packages' C paths

**Files:**
- Modify: `internal/compile/compile.go:21-36` (`Manifest.VendorDir`), `internal/compile/cgo.go:147-153` (the prefix map)
- Modify: `internal/emit/emit.go` (`compileNode`: `vendorDir`)
- Modify: `nix/builders.nix:74-85` (`compile`: `vendorDir`)
- Test: `internal/compile/cgo_test.go`, `internal/emit/emit_test.go`

**Interfaces:**
- Consumes: `Package.VendorDir` (Task 2).
- Produces: compile node argument `vendorDir` (relative); manifest field `vendorDir` (absolute, `"${src}/${vendorDir}"`), `compile.Manifest.VendorDir string`.

- [ ] **Step 1: Write the failing tests**

Append to `internal/compile/cgo_test.go`:

```go
// A vendored cgo package's C files are recorded under /_/vendor, as cmd/go
// records them: it maps the vendor directory, not the module's.
func TestRunCgoVendored(t *testing.T) {
	goBin := testutil.Go(t)
	std := testutil.CgoStdImportcfg(t, goBin)
	files := map[string]string{}
	for name, content := range cgoFiles {
		files["vendor/example.com/app/"+name] = content
	}
	src := testutil.WriteTree(t, files)
	m := cgoManifest(goBin, filepath.Join(src, "vendor", "example.com", "app"), std)
	m.TrimTo = "example.com/app@v1.0.0/cadd"
	m.VendorDir = filepath.Join(src, "vendor")
	out := t.TempDir()
	if err := Run(m, out, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	archive := readArchive(t, out)
	if !bytes.Contains(archive, []byte("/_/vendor/example.com/app/cadd")) {
		t.Error("pkg.a does not name the vendored source directory /_/vendor/example.com/app/cadd")
	}
	if bytes.Contains(archive, []byte(src)) {
		t.Errorf("pkg.a names the source directory %s", src)
	}
}
```

Append to `internal/emit/emit_test.go`:

```go
// A vendored cgo package's compile is told where its vendor directory is.
func TestNixVendoredCgo(t *testing.T) {
	g := cgoGraph()
	cadd := g.Packages["example.com/app/internal/cadd"]
	cadd.Subdir, cadd.VendorDir = "vendor/example.com/app/internal/cadd", "vendor"
	var buf bytes.Buffer
	if err := Nix(&buf, g); err != nil {
		t.Fatal(err)
	}
	if want := "      vendorDir = \"vendor\";\n"; !strings.Contains(buf.String(), want) {
		t.Errorf("output lacks %q:\n%s", want, buf.String())
	}
	if n := strings.Count(buf.String(), "vendorDir"); n != 1 {
		t.Errorf("vendorDir appears %d times, want once:\n%s", n, buf.String())
	}
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/compile/ ./internal/emit/`
Expected: the compile package fails to build with `m.VendorDir undefined`; `TestNixVendoredCgo` fails with "output lacks".

- [ ] **Step 3: Implement**

In `internal/compile/compile.go`, add to `Manifest` after `TrimTo`:

```go
	VendorDir  string              `json:"vendorDir"`  // a vendored package's vendor directory, "" otherwise
```

In `internal/compile/cgo.go`, replace

```go
	var srcMap cc.PathMap
	if m.TrimTo != "" {
		rootDir, rootName := trimRoot(m.SrcDir, m.TrimTo)
		srcMap = cc.PathMap{From: rootDir, To: "/_/" + rootName}
	}
```

with

```go
	var srcMap cc.PathMap
	switch {
	case m.TrimTo == "":
	case m.VendorDir != "":
		// cmd/go maps the vendor directory itself for vendored packages.
		srcMap = cc.PathMap{From: m.VendorDir, To: "/_/vendor"}
	default:
		rootDir, rootName := trimRoot(m.SrcDir, m.TrimTo)
		srcMap = cc.PathMap{From: rootDir, To: "/_/" + rootName}
	}
```

In `internal/emit/emit.go`, `compileNode`, right after the line that prints `deps`:

```go
	if p.VendorDir != "" && p.Cgo != nil {
		// The C compiler maps a vendored package's vendor directory.
		attr(b, 3, "vendorDir", quote(p.VendorDir))
	}
```

In `nix/builders.nix`, `compile`, add `vendorDir ? null` to the argument set (before `...`) and extend the manifest's tail:

```nix
      } // lib.optionalAttrs (testMain != null) { inherit testMain; }
        // lib.optionalAttrs (vendorDir != null) { vendorDir = "${src}/${vendorDir}"; };
```

Run `gofmt -w internal/compile/ internal/emit/`.

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/compile/ ./internal/emit/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/compile/ internal/emit/ nix/builders.nix
git commit -q -F - <<'EOF'
feat(compile): record vendored C files under /_/vendor, as cmd/go does

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01AdA5UEs8Aiwy6pwwGeaNzo
EOF
```

---

### Task 4: The resolver in workspace and vendor mode

**Files:**
- Modify: `internal/resolve/resolve.go` (`Args`, `Run`, `moduleDir`; new `findWorkFile`, `readSums`, `listFailed`, `hasVendor`)
- Test: `internal/resolve/resolve_test.go`

**Interfaces:**
- Consumes: `golist.Options.GOWORK`, `golist.Options.IgnoreVendor`, `golist.WorkUses` (Task 1); the graph's vendored and workspace handling (Task 2).
- Produces: `resolve.Args.GoWork bool \`json:"goWork"\``, `resolve.Args.Vendor bool \`json:"vendor"\``.

- [ ] **Step 1: Write the failing tests**

Add `"maps"`, `"os"`, `"os/exec"` and `"path/filepath"` to the imports of `internal/resolve/resolve_test.go` where missing, and `"github.com/draganm/gonixgo/internal/golist"`. Append:

```go
// goCmd runs the go command in dir, offline.
func goCmd(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command(testutil.Go(t), args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOFLAGS=", "GOPROXY=off", "GOTOOLCHAIN=local")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go %v: %v\n%s", args, err, out)
	}
}

// workspaceFiles is a workspace of two modules: app's program imports lib.
var workspaceFiles = map[string]string{
	"go.work":     "go 1.22\n\nuse (\n\t./app\n\t./lib\n)\n",
	"app/go.mod":  "module example.com/work/app\n\ngo 1.22\n",
	"app/main.go": "package main\n\nimport (\n\t\"fmt\"\n\n\t\"example.com/work/lib\"\n)\n\nfunc main() { fmt.Println(lib.Name) }\n",
	"lib/go.mod":  "module example.com/work/lib\n\ngo 1.21\n",
	"lib/lib.go":  "package lib\n\nconst Name = \"lib\"\n",
}

// A go.work above modRoot inside src is used: lib builds from src, and
// module info lists it as (devel).
func TestRunWorkspace(t *testing.T) {
	out, err := run(t, Args{Src: testutil.WriteTree(t, workspaceFiles), ModRoot: "app", SubPackages: []string{"."}, GoWork: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`src = b.localDir { name = "gosrc-example.com-work-lib"; files = [ "lib/lib.go" ]; };`,
		`dep\texample.com/work/lib\t(devel)\t\n`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

// The workspace root, which has no go.mod, can be modRoot.
func TestRunWorkspaceRoot(t *testing.T) {
	out, err := run(t, Args{Src: testutil.WriteTree(t, workspaceFiles), ModRoot: ".", SubPackages: []string{"app"}, GoWork: true})
	if err != nil {
		t.Fatal(err)
	}
	if want := `"app" = b.link {`; !strings.Contains(out, want) {
		t.Errorf("output lacks %q:\n%s", want, out)
	}
}

// Without goWork, or with the go.work above src, the build is in module
// mode, where nothing provides lib.
func TestRunWorkspaceOff(t *testing.T) {
	root := testutil.WriteTree(t, workspaceFiles)
	for name, a := range map[string]Args{
		"goWork off": {Src: root, ModRoot: "app"},
		"above src":  {Src: filepath.Join(root, "app"), ModRoot: ".", GoWork: true},
	} {
		a.SubPackages = []string{"."}
		_, err := run(t, a)
		var loadErr *graph.LoadError
		if !errors.As(err, &loadErr) || !strings.Contains(err.Error(), "example.com/work/lib") {
			t.Errorf("%s: err = %v, want a load error about example.com/work/lib", name, err)
		}
	}
}

// With goWork, modRoot needs a go.mod or a go.work at or above it inside
// src.
func TestRunModRootWithoutGoModOrGoWork(t *testing.T) {
	_, err := run(t, Args{Src: testutil.WriteTree(t, appFiles), ModRoot: "internal", SubPackages: []string{"."}, GoWork: true})
	if err == nil || !strings.Contains(err.Error(), `modRoot "internal": no go.mod in `) ||
		!strings.HasSuffix(err.Error(), ", and no go.work in it or above it inside src") {
		t.Errorf("err = %v", err)
	}
}

// In workspace mode the checksums come from go.work.sum and every used
// module's go.sum; in module mode from modRoot's only.
func TestReadSumsWorkspace(t *testing.T) {
	files := maps.Clone(workspaceFiles)
	files["go.work.sum"] = "example.com/x v1.0.0 h1:x\n"
	files["lib/go.sum"] = "example.com/y v1.0.0 h1:y\n"
	files["app/go.sum"] = "example.com/z v1.0.0 h1:z\n"
	root := testutil.WriteTree(t, files)
	o := golist.Options{Go: testutil.Go(t), Dir: filepath.Join(root, "app")}
	got, err := readSums(o, filepath.Join(root, "go.work"))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"example.com/x@v1.0.0": "h1:x", "example.com/y@v1.0.0": "h1:y", "example.com/z@v1.0.0": "h1:z"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("workspace sums = %v, want %v", got, want)
	}
	if got, err = readSums(o, ""); err != nil || !reflect.DeepEqual(got, map[string]string{"example.com/z@v1.0.0": "h1:z"}) {
		t.Errorf("module sums = %v, %v; want app's only", got, err)
	}
}

// vendoredFiles is a module whose dependencies go.mod replaces by
// directories, which go mod vendor copies into vendor/. Only the main
// package's test imports testdep.
var vendoredFiles = map[string]string{
	"go.mod": "module example.com/vendored\n\ngo 1.22\n\nrequire (\n" +
		"\texample.com/dep v0.0.0-00010101000000-000000000000\n" +
		"\texample.com/testdep v0.0.0-00010101000000-000000000000\n)\n\n" +
		"replace example.com/dep => ./dep\n\nreplace example.com/testdep => ./testdep\n",
	"main.go":            "package main\n\nimport (\n\t\"fmt\"\n\n\t\"example.com/dep\"\n)\n\nfunc main() { fmt.Println(dep.Name) }\n",
	"main_test.go":       "package main\n\nimport (\n\t\"testing\"\n\n\t\"example.com/testdep\"\n)\n\nfunc TestName(t *testing.T) { _ = testdep.Name }\n",
	"dep/go.mod":         "module example.com/dep\n\ngo 1.21\n",
	"dep/dep.go":         "package dep\n\nconst Name = \"dep\"\n",
	"testdep/go.mod":     "module example.com/testdep\n\ngo 1.21\n",
	"testdep/testdep.go": "package testdep\n\nconst Name = \"testdep\"\n",
}

func vendoredTree(t *testing.T) string {
	t.Helper()
	src := testutil.WriteTree(t, vendoredFiles)
	goCmd(t, src, "mod", "vendor")
	return src
}

// With vendor/ the dependencies, a test-only one included, build from it,
// and nothing is fetched.
func TestRunVendored(t *testing.T) {
	out, err := run(t, Args{Src: vendoredTree(t), ModRoot: ".", SubPackages: []string{"."}, Vendor: true, DoCheck: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"  modules = {\n  };\n",
		`name = "gopkg-example.com-dep-v0.0.0-00010101000000-000000000000";`,
		`src = b.localDir { name = "gosrc-example.com-dep"; files = [ "vendor/example.com/dep/dep.go" ]; };`,
		`src = b.localDir { name = "gosrc-example.com-testdep"; files = [ "vendor/example.com/testdep/testdep.go" ]; };`,
		`dep\texample.com/dep\tv0.0.0-00010101000000-000000000000\n=>\t./dep\t(devel)\t\n\n`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

// Without vendor, vendor/ is ignored: the replacements build from their
// own directories.
func TestRunVendorOff(t *testing.T) {
	out, err := run(t, Args{Src: vendoredTree(t), ModRoot: ".", SubPackages: []string{"."}})
	if err != nil {
		t.Fatal(err)
	}
	if want := `src = b.localDir { name = "gosrc-example.com-dep"; files = [ "dep/dep.go" ]; };`; !strings.Contains(out, want) {
		t.Errorf("output lacks %q:\n%s", want, out)
	}
}

// go work vendor puts vendor/ next to go.work, and the workspace builds
// from it.
func TestRunWorkspaceVendored(t *testing.T) {
	files := maps.Clone(workspaceFiles)
	files["go.work"] = "go 1.22\n\nuse (\n\t./app\n\t./lib\n)\n\nreplace example.com/dep => ./dep\n"
	files["lib/go.mod"] = "module example.com/work/lib\n\ngo 1.21\n\nrequire example.com/dep v0.0.0-00010101000000-000000000000\n"
	files["lib/lib.go"] = "package lib\n\nimport \"example.com/dep\"\n\nconst Name = dep.Name\n"
	files["dep/go.mod"] = "module example.com/dep\n\ngo 1.21\n"
	files["dep/dep.go"] = "package dep\n\nconst Name = \"dep\"\n"
	root := testutil.WriteTree(t, files)
	goCmd(t, root, "work", "vendor")
	out, err := run(t, Args{Src: root, ModRoot: "app", SubPackages: []string{"."}, GoWork: true, Vendor: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`files = [ "vendor/example.com/dep/dep.go" ]`, `files = [ "lib/lib.go" ]`} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

// When go list fails as a whole, the error names the go.work and vendor/
// that gonixgo let go use, and how to turn each off.
func TestRunListFailureLines(t *testing.T) {
	files := maps.Clone(workspaceFiles)
	files["go.work"] = "go 1.22\n\nuse (\n\t./app\n\t./lib\n\t../outside\n)\n"
	root := testutil.WriteTree(t, files)
	real, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	_, err = run(t, Args{Src: root, ModRoot: "app", SubPackages: []string{"."}, GoWork: true, Vendor: true})
	want := "\ngonixgo used the workspace " + filepath.Join(real, "go.work") + "; set useGoWork = false to build without it"
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("err = %v, want it to contain %q", err, want)
	}

	// A requirement that vendor/modules.txt does not know makes vendor/
	// out of date.
	src := vendoredTree(t)
	goMod := filepath.Join(src, "go.mod")
	data, err := os.ReadFile(goMod)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(goMod, append(data, "\nrequire example.com/extra v1.0.0\n"...), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = run(t, Args{Src: src, ModRoot: ".", SubPackages: []string{"."}, Vendor: true})
	if want := "\ngonixgo let go use vendor/; set useVendor = false to fetch modules instead"; err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("err = %v, want it to contain %q", err, want)
	}
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/resolve/`
Expected: the build fails with `unknown field GoWork in struct literal of type Args` and `undefined: readSums`.

- [ ] **Step 3: Implement**

In `internal/resolve/resolve.go`, add `"maps"` to the imports and to `Args` after `DoCheck`:

```go
	GoWork      bool     `json:"goWork"`     // use the go.work at modRoot or above it inside src
	Vendor      bool     `json:"vendor"`     // let go use vendor/, as go build does
```

In `Run`, replace

```go
	dir, err := moduleDir(src, a.ModRoot)
	if err != nil {
		return err
	}
```

with

```go
	dir, workFile, err := moduleDir(src, a.ModRoot, a.GoWork)
	if err != nil {
		return err
	}
	// A go command that fails as a whole may be about the go.work or the
	// vendor/ that gonixgo let it use.
	failed := func(err error) error { return listFailed(err, dir, workFile, a.Vendor) }
```

and add to the `golist.Options` literal:

```go
		GOWORK:       workFile,
		IgnoreVendor: !a.Vendor,
```

Wrap the errors of both `golist.Env` calls and of `golist.List` with `failed`: `return failed(err)`. Replace the reading of `go.sum`

```go
	sums := map[string]string{}
	if data, err := os.ReadFile(filepath.Join(o.Dir, "go.sum")); err == nil {
		sums = modcache.ParseGoSum(data)
	}
```

with

```go
	sums, err := readSums(o, workFile)
	if err != nil {
		return failed(err)
	}
```

and the `addTests` error handling with

```go
	if a.DoCheck && len(g.Tested) > 0 {
		if err := addTests(g, o, graph.Input{Src: src, Env: env, Tags: a.Tags, Sums: sums}); err != nil {
			var loadErr *graph.LoadError
			if !errors.As(err, &loadErr) {
				err = failed(err)
			}
			return err
		}
	}
```

Replace `moduleDir` with:

```go
// moduleDir is the directory go list runs in, modRoot inside src ("" is
// src itself), and with work set the go.work to use: the first one in it
// or a directory above it, up to src. It needs a go.mod or a go.work.
func moduleDir(src, modRoot string, work bool) (dir, workFile string, err error) {
	rel := filepath.FromSlash(modRoot)
	if rel == "" {
		rel = "."
	}
	if !filepath.IsLocal(rel) {
		return "", "", fmt.Errorf("modRoot %q must be a directory inside src", modRoot)
	}
	dir = filepath.Join(src, rel)
	if work {
		workFile = findWorkFile(src, dir)
	}
	if _, err := os.Stat(filepath.Join(dir, "go.mod")); err != nil && workFile == "" {
		msg := fmt.Sprintf("modRoot %q: no go.mod in %s", modRoot, dir)
		if work {
			msg += ", and no go.work in it or above it inside src"
		}
		return "", "", errors.New(msg)
	}
	return dir, workFile, nil
}

// findWorkFile returns the first go.work in dir or a directory above it,
// up to and including src, or "" if there is none. dir is inside src.
func findWorkFile(src, dir string) string {
	for {
		f := filepath.Join(dir, "go.work")
		if info, err := os.Stat(f); err == nil && !info.IsDir() {
			return f
		}
		parent := filepath.Dir(dir)
		if dir == src || parent == dir {
			return ""
		}
		dir = parent
	}
}

// readSums reads the checksums go.sum files hold: modRoot's in module
// mode; in workspace mode go.work.sum's and those of every directory the
// go.work uses.
func readSums(o golist.Options, workFile string) (map[string]string, error) {
	files := []string{filepath.Join(o.Dir, "go.sum")}
	if workFile != "" {
		uses, err := golist.WorkUses(o, workFile)
		if err != nil {
			return nil, err
		}
		files = []string{filepath.Join(filepath.Dir(workFile), "go.work.sum")}
		for _, dir := range uses {
			files = append(files, filepath.Join(dir, "go.sum"))
		}
	}
	sums := map[string]string{}
	for _, f := range files {
		// A module without go.sum has no checksums to give.
		if data, err := os.ReadFile(f); err == nil {
			maps.Copy(sums, modcache.ParseGoSum(data))
		}
	}
	return sums, nil
}

// listFailed adds to err, a go command that failed as a whole, the
// go.work and vendor/ gonixgo let go use, and how to turn each off.
func listFailed(err error, dir, workFile string, vendor bool) error {
	var extra string
	if workFile != "" {
		extra += "\ngonixgo used the workspace " + workFile + "; set useGoWork = false to build without it"
	}
	if vendor && hasVendor(dir, workFile) {
		extra += "\ngonixgo let go use vendor/; set useVendor = false to fetch modules instead"
	}
	if extra == "" {
		return err
	}
	return fmt.Errorf("%w%s", err, extra)
}

// hasVendor reports whether vendor/modules.txt is in dir or next to the
// go.work.
func hasVendor(dir, workFile string) bool {
	paths := []string{filepath.Join(dir, "vendor", "modules.txt")}
	if workFile != "" {
		paths = append(paths, filepath.Join(filepath.Dir(workFile), "vendor", "modules.txt"))
	}
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	return false
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/resolve/`
Expected: PASS, the existing tests included (they leave `GoWork` and `Vendor` false: module mode with `-mod=readonly`, as before).

- [ ] **Step 5: Run the whole suite and commit**

Run: `go test ./...`
Expected: PASS.

```bash
git add internal/resolve/
git commit -q -F - <<'EOF'
feat(resolve): resolve in workspace and vendor mode as go build does

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01AdA5UEs8Aiwy6pwwGeaNzo
EOF
```

---

### Task 5: `useGoWork` and `useVendor`, and the `workspace` fixture

**Files:**
- Modify: `nix/build-go-application.nix:3-50` (arguments; the resolver's JSON)
- Create: `tests/fixtures/workspace/go.work`, `app/go.mod`, `app/main.go`, `app/main_test.go`, `lib/go.mod`, `lib/go.sum`, `lib/lib.go`, `lib/lib_test.go`
- Modify: `tests/fixtures.nix` (`workspace`, `workspaceWith`)
- Modify: `tests/run.sh` (`workspace_with`, `check_workspace_root`, `check_workspace_off`, `workspace_checks`)

**Interfaces:**
- Consumes: `resolve.Args.GoWork` and `.Vendor` (Task 4); `check_modinfo_in` (`tests/run.sh`).
- Produces: `buildGoApplication` arguments `useGoWork ? true` and `useVendor ? true`; `fixtures.workspace`, `fixtures.workspaceWith`.

- [ ] **Step 1: The fixture's sources**

`tests/fixtures/workspace/go.work`:

```
go 1.22

use (
	./app
	./lib
)
```

`tests/fixtures/workspace/app/go.mod`:

```
module example.com/work/app

go 1.22
```

`tests/fixtures/workspace/app/main.go`:

```go
// Command app prints the source paths the compiler recorded for its own
// file, for lib's, a module of its workspace, and for go-cmp's Equal,
// which lib requires, then what lib's closures saw.
package main

import (
	"fmt"
	"runtime"

	"example.com/work/lib"
)

func main() {
	_, file, _, _ := runtime.Caller(0)
	fmt.Println(file, lib.Where(), lib.CmpFile(), lib.Captured())
}
```

`tests/fixtures/workspace/app/main_test.go`:

```go
package main

import (
	"fmt"
	"testing"

	"example.com/work/lib"
)

// The tests of every workspace module run.
func TestCaptured(t *testing.T) {
	if got := fmt.Sprint(lib.Captured()); got != "[3 3 3]" {
		t.Fatalf("lib.Captured() = %s, want [3 3 3]: lib compiles with its own go directive", got)
	}
}
```

`tests/fixtures/workspace/lib/go.mod`:

```
module example.com/work/lib

go 1.21

require github.com/google/go-cmp v0.6.0
```

`tests/fixtures/workspace/lib/lib.go`:

```go
// Package lib is a module of the workspace, which app uses without a
// requirement or a replace.
package lib

import (
	"reflect"
	"runtime"

	"github.com/google/go-cmp/cmp"
)

// Where returns the path the compiler recorded for this file.
func Where() string {
	_, file, _, _ := runtime.Caller(0)
	return file
}

// CmpFile returns the path the compiler recorded for go-cmp's Equal.
func CmpFile() string {
	fn := runtime.FuncForPC(reflect.ValueOf(cmp.Equal).Pointer())
	file, _ := fn.FileLine(fn.Entry())
	return file
}

// Captured collects a loop variable through closures. Under go 1.21, this
// module's version, every closure sees the variable's last value.
func Captured() []int {
	var fs []func() int
	for i := 0; i < 3; i++ {
		fs = append(fs, func() int { return i })
	}
	var out []int
	for _, f := range fs {
		out = append(out, f())
	}
	return out
}
```

`tests/fixtures/workspace/lib/lib_test.go`:

```go
package lib

import "testing"

// The tests of every workspace module run.
func TestWhere(t *testing.T) {
	if Where() == "" {
		t.Fatal("no file recorded")
	}
}
```

Then write `lib/go.sum` and check the program:

```bash
(cd tests/fixtures/workspace/lib && GOWORK=off GOFLAGS= go mod tidy && cat go.mod go.sum)
(cd tests/fixtures/workspace/app && tmp="$(mktemp -d)" && GOFLAGS=-mod=readonly go build -trimpath -o "$tmp/app" . && "$tmp/app"; rm -rf "$tmp")
```

Expected: `go.mod` as written above; `go.sum` with the two `github.com/google/go-cmp v0.6.0` lines; the program prints `example.com/work/app/main.go example.com/work/lib/lib.go github.com/google/go-cmp@v0.6.0/cmp/compare.go [3 3 3]`.

- [ ] **Step 2: The fixture in Nix, and its checks**

In `tests/fixtures.nix`, add to the `let`:

```nix
  # The workspace fixture's arguments, which run.sh varies through
  # workspaceWith.
  workspaceArgs = {
    pname = "workspace";
    src = ./fixtures/workspace;
    modRoot = "app";
  };
```

and after `monorepoWith`:

```nix
  workspace = goEnv.buildGoApplication workspaceArgs;
  # The workspace fixture with the attributes f returns, given the default
  # arguments, laid over them.
  workspaceWith = f: goEnv.buildGoApplication (workspaceArgs // f workspaceArgs);
```

In `tests/run.sh`, add above the single-check guard:

```bash
# workspace_with <function from the default arguments to the changes> <expression over app>
# Evaluates the expression with app bound to the workspace fixture built
# with the changes.
workspace_with() {
  nix eval --impure --raw "${exec_opt[@]}" --expr "
    let
      flake = builtins.getFlake \"$flake\";
      app = flake.legacyPackages.\${builtins.currentSystem}.fixtures.workspaceWith ($1);
    in $2"
}

# check_workspace_root <expected stdout>
# A workspace builds from its root too, which has no go.mod.
check_workspace_root() {
  local out got
  out="$(nix build --impure --no-link --print-out-paths "${exec_opt[@]}" --expr "
    (builtins.getFlake \"$flake\").legacyPackages.\${builtins.currentSystem}.fixtures.workspaceWith (_: { modRoot = \".\"; subPackages = [ \"app\" ]; })")" ||
    fail "workspace: modRoot = \".\" does not build"
  got="$("$out/bin/app")"
  [ "$got" = "$1" ] || fail "workspace: from the workspace root, app printed '$got', want '$1'"
  echo "ok: workspace: the workspace root, without go.mod, works as modRoot"
}

# useGoWork = false builds in module mode, where nothing provides lib.
check_workspace_off() {
  local msg
  if msg="$(workspace_with '_: { useGoWork = false; }' 'app.drvPath' 2>&1)"; then
    fail "workspace: evaluated with useGoWork = false"
  fi
  case "$msg" in
    *'example.com/work/lib'*) echo "ok: workspace: useGoWork = false builds without the workspace" ;;
    *) fail "workspace: unhelpful error with useGoWork = false: $msg" ;;
  esac
}

# A workspace: app uses lib, a module of the go.work, without a
# requirement; go-cmp's checksum is only in lib's go.sum. The output is the
# source paths go build -trimpath records, and lib's closures under its own
# go 1.21.
workspace_checks() {
  local expected="example.com/work/app/main.go example.com/work/lib/lib.go github.com/google/go-cmp@v0.6.0/cmp/compare.go [3 3 3]"
  check_run workspace app "$expected"
  check_modinfo_in workspace workspace/app app . - GOWORK="$root/tests/fixtures/workspace/go.work"
  check_tested_set workspace "example.com/work/app example.com/work/lib"
  check_no_source_refs workspace
  check_workspace_root "$expected"
  check_workspace_off
}
```

and after `monorepo_checks` in the top-level sequence:

```bash
workspace_checks
```

- [ ] **Step 3: Run the checks to verify they fail**

Run: `tests/run.sh workspace_checks`
Expected: FAIL: evaluation reports that no module provides `example.com/work/lib`, because `buildGoApplication` does not ask the resolver for workspace mode yet.

- [ ] **Step 4: The arguments**

In `nix/build-go-application.nix`, add to the argument set after `packageOverrides ? { }`:

```nix
, useGoWork ? true
, useVendor ? true
```

and to the resolver's JSON, after `cross`:

```nix
      # Workspace and vendor mode as go build chooses them, unless turned off.
      goWork = useGoWork;
      vendor = useVendor;
```

- [ ] **Step 5: Run the checks**

Run: `tests/run.sh workspace_checks`
Expected: six `ok:` lines.

- [ ] **Step 6: Run everything**

Run: `go test ./... && tests/run.sh`
Expected: PASS, then `all integration checks passed`. The other fixtures have neither `go.work` nor `vendor/`, so they resolve as before.

- [ ] **Step 7: Commit**

```bash
git add nix/build-go-application.nix tests/
git commit -q -F - <<'EOF'
feat(nix): build workspaces, with useGoWork and useVendor to opt out

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01AdA5UEs8Aiwy6pwwGeaNzo
EOF
```

---

### Task 6: The `vendored` fixture

**Files:**
- Create: `tests/fixtures/vendored/go.mod`, `go.sum`, `main.go`, `cdep/go.mod`, `cdep/cdep.go`, `vendor/` (from `go mod vendor`)
- Modify: `tests/fixtures.nix` (`vendored`, `vendoredWith`)
- Modify: `tests/run.sh` (`vendored_with`, `check_vendored_modules`, `check_vendored_offline`, `vendored_checks`)

**Interfaces:**
- Consumes: Tasks 1-5; `check_modinfo_in`, `check_stdenv`, `check_no_source_refs` (`tests/run.sh`).
- Produces: `fixtures.vendored`, `fixtures.vendoredWith`.

This fixture checks Tasks 2-5 end to end, `nix/builders.nix`'s `vendorDir` included; its RED is the missing section.

- [ ] **Step 1: Verify the section is missing**

Run: `tests/run.sh vendored_checks`
Expected: FAIL with `vendored_checks: command not found`.

- [ ] **Step 2: The fixture's sources**

`tests/fixtures/vendored/go.mod`:

```
module example.com/vendored

go 1.22

require (
	example.com/cdep v0.0.0-00010101000000-000000000000
	github.com/google/go-cmp v0.6.0
)

replace example.com/cdep => ./cdep
```

`tests/fixtures/vendored/main.go`:

```go
// Command vendored prints the source paths the compiler recorded for
// go-cmp's Equal and for cdep's C code, both built from vendor/.
package main

import (
	"fmt"
	"reflect"
	"runtime"

	"example.com/cdep"
	"github.com/google/go-cmp/cmp"
)

func main() {
	fn := runtime.FuncForPC(reflect.ValueOf(cmp.Equal).Pointer())
	file, _ := fn.FileLine(fn.Entry())
	fmt.Println(file, cdep.Where())
}
```

`tests/fixtures/vendored/cdep/go.mod`:

```
module example.com/cdep

go 1.21
```

`tests/fixtures/vendored/cdep/cdep.go`:

```go
// Package cdep is a cgo package that go.mod replaces by a directory and
// go mod vendor copies into vendor/.
package cdep

// static const char *where(void) { return __FILE__; }
import "C"

// Where returns the path the C compiler recorded for this file.
func Where() string { return C.GoString(C.where()) }
```

Then:

```bash
(cd tests/fixtures/vendored && GOWORK=off GOFLAGS= go mod tidy && GOWORK=off GOFLAGS= go mod vendor && cat go.mod && cat go.sum && find vendor -type f | sort)
(cd tests/fixtures/vendored && tmp="$(mktemp -d)" && GOWORK=off GOFLAGS= go build -trimpath -o "$tmp/v" . && "$tmp/v"; rm -rf "$tmp")
```

Expected: `go.mod` as written; `go.sum` with go-cmp's two lines; `vendor/` holding `modules.txt`, go-cmp's `LICENSE` and package files, and `example.com/cdep/cdep.go`; the program prints `github.com/google/go-cmp@v0.6.0/cmp/compare.go /_/vendor/example.com/cdep/cdep.go`.

- [ ] **Step 3: The fixture in Nix, and its checks**

In `tests/fixtures.nix`, add to the `let`:

```nix
  # The vendored fixture's arguments, which run.sh varies through
  # vendoredWith.
  vendoredArgs = {
    pname = "vendored";
    src = ./fixtures/vendored;
  };
```

and after `workspaceWith`:

```nix
  vendored = goEnv.buildGoApplication vendoredArgs;
  # The vendored fixture with the attributes f returns, given the default
  # arguments, laid over them.
  vendoredWith = f: goEnv.buildGoApplication (vendoredArgs // f vendoredArgs);
```

In `tests/run.sh`, add above the single-check guard:

```bash
# vendored_with <function from the default arguments to the changes> <expression over app>
# Evaluates the expression with app bound to the vendored fixture built
# with the changes.
vendored_with() {
  nix eval --impure --raw "${exec_opt[@]}" --expr "
    let
      flake = builtins.getFlake \"$flake\";
      app = flake.legacyPackages.\${builtins.currentSystem}.fixtures.vendoredWith ($1);
    in $2"
}

# check_vendored_modules <changes> <module keys, space separated>
# What the vendored fixture fetches.
check_vendored_modules() {
  local got
  got="$(vendored_with "$1" 'builtins.concatStringsSep " " (builtins.attrNames app.modules)')" ||
    fail "vendored: does not evaluate with $1"
  [ "$got" = "$2" ] || fail "vendored: with $1 the modules are [$got], want [$2]"
  echo "ok: vendored: with $1 the modules are [$2]"
}

# A vendored build fetches nothing, so it evaluates without network or
# module cache.
check_vendored_offline() {
  local cache
  cache="$(mktemp -d)"
  if ! GOPROXY=off GOMODCACHE="$cache" nix eval "${exec_opt[@]}" --raw "$flake#fixtures.vendored.drvPath" >/dev/null; then
    rm -rf "$cache"
    fail "vendored: does not evaluate with GOPROXY=off and an empty module cache"
  fi
  rm -rf "$cache"
  echo "ok: vendored: evaluates with GOPROXY=off and an empty module cache"
}

# A vendored module: go-cmp, and cdep, a cgo package behind a directory
# replace, build from vendor/. The output is the source paths go build
# -trimpath records, cdep's C file under /_/vendor as cmd/go maps it.
vendored_checks() {
  check_run vendored vendored "github.com/google/go-cmp@v0.6.0/cmp/compare.go /_/vendor/example.com/cdep/cdep.go"
  check_modinfo_in vendored vendored vendored . cgoShell GOFLAGS=
  check_vendored_modules '_: { }' ""
  check_vendored_modules '_: { useVendor = false; }' "github.com/google/go-cmp@v0.6.0"
  check_vendored_offline
  check_stdenv 'fixtures.vendored.packages."example.com/cdep"' true
  check_no_source_refs vendored
}
```

and after `workspace_checks` in the top-level sequence:

```bash
vendored_checks
```

- [ ] **Step 4: Run the section**

Run: `tests/run.sh vendored_checks`
Expected: seven `ok:` lines.

- [ ] **Step 5: Run everything**

Run: `go test ./... && tests/run.sh`
Expected: PASS, then `all integration checks passed`.

- [ ] **Step 6: Commit**

```bash
git add tests/
git commit -q -F - <<'EOF'
test: add the vendored fixture

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01AdA5UEs8Aiwy6pwwGeaNzo
EOF
```

---

### Task 7: Documentation

**Files:**
- Modify: `README.md` (the `buildGoApplication` table, the monorepo section, a Vendoring section, "Not yet supported")
- Modify: `docs/superpowers/specs/2026-10-03-gonixgo-design.md` (lines 286-287, 540-541)

**Interfaces:**
- Consumes: the behaviour of Tasks 1-6.

- [ ] **Step 1: README table**

Replace the `modRoot` row and add two rows after `subPackages`:

```markdown
| `modRoot` | `"."` | Directory of `go.mod` inside `src`, or of a workspace's `go.work`. |
| `subPackages` | `[ "." ]` | Main packages to build, relative to `modRoot`. |
| `useGoWork` | `true` | Use the `go.work` at `modRoot` or above it inside `src`; see [Monorepos and `replace`](#monorepos-and-replace). `false` builds in module mode, as `GOWORK=off`. |
| `useVendor` | `true` | Build from `vendor/` whenever `go build` would; see [Vendoring](#vendoring). `false` fetches modules instead, as `-mod=readonly`. |
```

- [ ] **Step 2: README sections**

In "Monorepos and `replace`", replace the paragraph that begins "`go.work` is ignored" with:

```markdown
A `go.work` at `modRoot` or in a directory above it, inside `src`, puts the
build in workspace mode, as it does `go build`. Every module it uses builds
from `src` with its own `go` directive, and the tests of their packages that
the program contains run. A binary's module info lists the other workspace
modules as `(devel)`. `modRoot` may then be the workspace root, with
`subPackages` naming the programs. A `go.work` meant for local development,
or one that uses directories outside `src`, is turned off with
`useGoWork = false`.

A directory `replace` that leaves `src` fails during evaluation: make `src`
the directory that holds both modules, and `modRoot` the one with the
program.
```

After that section, add:

```markdown
### Vendoring

With a `vendor/` directory, from `go mod vendor` or, in a workspace,
`go work vendor`, the dependencies build from it whenever `go build` would:
when `vendor/modules.txt` is there and the `go` directive is 1.14 or later.
Nothing is fetched, so evaluation needs no network and no module cache, and
module info records no checksums, as under `go build`.

An out-of-date `vendor/` fails evaluation with Go's "inconsistent vendoring"
message: run `go mod vendor`, or set `useVendor = false` to fetch the modules
instead.
```

- [ ] **Step 3: README "Not yet supported"**

Replace its first paragraph with:

```markdown
Packages with SWIG, Fortran or `.syso` files; `.syso` support may come
later. They are rejected during evaluation with a message naming them.
```

- [ ] **Step 4: The 2026-10-03 design**

In `docs/superpowers/specs/2026-10-03-gonixgo-design.md`, "Environment for `go list`", replace `Set by the tool: \`GOENV=off\`, \`GOFLAGS=-mod=readonly\`, \`GOWORK=off\`,` with:

```markdown
Set by the tool: `GOENV=off`; `GOFLAGS`, empty or `-mod=readonly` with
`useVendor = false`; `GOWORK`, the `go.work` inside `src` or `off` (see the
[workspace and vendoring design](2026-10-12-workspace-vendor-design.md));
```

and in "Not in this version" drop `` `go.work` (forced off), `vendor/` directories (ignored; modules come from the module cache), `` so the list starts with `PGO`.

- [ ] **Step 5: Check and commit**

Run: `git diff --check && echo clean`
Expected: `clean`.

```bash
git add README.md docs/superpowers/specs/2026-10-03-gonixgo-design.md
git commit -q -F - <<'EOF'
docs: document workspaces and vendoring

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01AdA5UEs8Aiwy6pwwGeaNzo
EOF
```
