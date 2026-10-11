# Cross-compilation and Monorepo Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build Go programs for other platforms with the cached native Go, and build programs whose `go.mod` replaces dependencies with other versions or with directories inside `src`.

**Architecture:** The target comes from `pkgs.stdenv.hostPlatform.go` and reaches the resolver, the standard library and every compile and link manifest; cgo defaults to off when nixpkgs' build and host platforms differ. The resolver classifies replaced modules: a version replacement is fetched under the replacement's path and version, a directory replacement inside `src` builds like a local package, and both keep the trim path and module info that `go build -trimpath` gives them.

**Tech Stack:** Go (gonixgo's module says `go 1.23`; tests use nixpkgs' Go 1.26.7), Nix 2.26.1, nixpkgs `nixos-26.05`, bash for `tests/run.sh`.

**Spec:** `docs/superpowers/specs/2026-10-10-cross-monorepo-design.md`

## Global Constraints

- Branch `cross-monorepo`. Each commit message is conventional (`feat(graph): …`, `test: …`, `docs: …`) and ends with:
  ```
  Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
  Claude-Session: https://claude.ai/code/session_01AdA5UEs8Aiwy6pwwGeaNzo
  ```
- Remove every binary you generate. Probe builds go to a `mktemp -d` directory that is deleted afterwards.
- A native build keeps the same Go, standard library and C toolchain derivations.
- Messages, verbatim:
  - `modRoot "<modRoot>" must be a directory inside src`
  - `modRoot "<modRoot>": no go.mod in <dir>`
  - `replace <path> => <replacement as written>: <dir> is outside src <src>`
  - `a directory replace must point inside src; set src to a directory that holds both modules and modRoot to the one with go.mod`
  - `cgo is off in a cross build; set CGO_ENABLED = 1 to build cgo packages`
- The standard library's name: `go-stdlib-<version>-<goos>-<goarch>[v<GOARM>][-cgo]`.
- Compile and link manifests carry `goarm` only when `GOARM` is set.
- Unit tests: `go test ./...`. Integration: `tests/run.sh`, which needs `builtins.exec` and, on a first run, the network; after Task 5, `tests/run.sh <function> [args]` runs one check or section.
- Comments and names follow the surrounding code: full sentences, plain words, no jargon.

## Review Focus

1. Two required modules replaced by the same module version share one fetch and keep their own trim paths: Task 2, `TestBuildSharedReplacement`.
2. A cgo package in a directory-replaced module gets the C prefix map `/_/<path>@<required version>`: Task 2, a `TestTrimRoot` case.
3. A directory replacement written as an absolute path inside `src` is local and appears in module info as written: Task 2, `TestBuildAbsoluteDirectoryReplacement`.
4. `modRoot` spelt `./services/api/`, `services//api` or `""` resolves like `services/api` or `.`: Task 4, `TestRunModRootSpellings`.

---

### Task 1: Module info for replaced modules

**Files:**
- Modify: `internal/modinfo/modinfo.go:11-52`
- Test: `internal/modinfo/modinfo_test.go`

**Interfaces:**
- Produces: `modinfo.Module{Path, Version, Sum string; Replace *Module}`. `Info.String()` writes a replaced module as `dep\t<path>\t<version>\n=>\t<path>\t<version>\t<sum>\n\n`.

- [ ] **Step 1: Write the failing test**

Add `"runtime/debug"` to the imports of `internal/modinfo/modinfo_test.go` and append:

```go
// toDebug converts m to the runtime/debug form.
func toDebug(m Module) *debug.Module {
	d := &debug.Module{Path: m.Path, Version: m.Version, Sum: m.Sum}
	if m.Replace != nil {
		d.Replace = toDebug(*m.Replace)
	}
	return d
}

// Replaced modules come out as runtime/debug writes them, which is what
// go build embeds: the dep line without a sum, the => line, an empty line.
func TestStringWithReplacements(t *testing.T) {
	info := Info{
		Path: "example.com/app",
		Main: Module{Path: "example.com/app", Version: "(devel)"},
		Deps: []Module{
			{Path: "example.com/lib", Version: "v0.0.0-00010101000000-000000000000", Replace: &Module{Path: "../lib", Version: "(devel)"}},
			{Path: "github.com/a/b", Version: "v1.0.0", Replace: &Module{Path: "github.com/fork/b", Version: "v1.0.1", Sum: "h1:fork"}},
			{Path: "github.com/c/d", Version: "v2.0.0", Sum: "h1:cd"},
		},
		Settings: Settings(goldenEnv, nil, ""),
	}
	bi := debug.BuildInfo{Path: info.Path, Main: *toDebug(info.Main)}
	for _, d := range info.Deps {
		bi.Deps = append(bi.Deps, toDebug(d))
	}
	for _, s := range info.Settings {
		bi.Settings = append(bi.Settings, debug.BuildSetting{Key: s.Key, Value: s.Value})
	}
	got := info.String()
	if want := bi.String(); got != want {
		t.Fatalf("String() =\n%q\nwant what runtime/debug writes:\n%q", got, want)
	}
	if want := "dep\texample.com/lib\tv0.0.0-00010101000000-000000000000\n=>\t../lib\t(devel)\t\n\n"; !strings.Contains(got, want) {
		t.Errorf("String() lacks %q:\n%q", want, got)
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/modinfo/ -run TestStringWithReplacements`
Expected: the build fails with `unknown field Replace in struct literal of type Module`.

- [ ] **Step 3: Implement**

In `internal/modinfo/modinfo.go`, replace the `Module` type with:

```go
// Module is one mod or dep line.
type Module struct {
	Path    string
	Version string
	Sum     string
	Replace *Module // the module that replaces this one, nil for none
}
```

In `Info.String`, replace the two `Fprintf` calls for `mod` and `dep` lines with:

```go
	fmt.Fprintf(&b, "path\t%s\n", i.Path)
	writeModule(&b, "mod", i.Main)
	for _, d := range i.Deps {
		writeModule(&b, "dep", d)
	}
```

and add below `String`:

```go
// writeModule writes m as runtime/debug.BuildInfo.String does. A replaced
// module's line has no sum; the replacement's => line follows, then an
// empty line.
func writeModule(b *strings.Builder, word string, m Module) {
	fmt.Fprintf(b, "%s\t%s\t%s", word, m.Path, m.Version)
	if m.Replace == nil {
		fmt.Fprintf(b, "\t%s", m.Sum)
	} else {
		b.WriteString("\n")
		writeModule(b, "=>", *m.Replace)
	}
	b.WriteString("\n")
}
```

- [ ] **Step 4: Run the package's tests**

Run: `go test ./internal/modinfo/`
Expected: PASS, `TestStringGolden` and `TestStringWithDepsAndTags` included.

- [ ] **Step 5: Commit**

```bash
git add internal/modinfo/
git commit -q -F - <<'EOF'
feat(modinfo): write replaced modules as go build does

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01AdA5UEs8Aiwy6pwwGeaNzo
EOF
```

---

### Task 2: Replaced modules in the graph

**Files:**
- Modify: `internal/graph/pkg.go:15-98` (`newPackage`; new `trimTo`)
- Modify: `internal/graph/graph.go` (`Graph.DepModules`, `Build`, `addDepModule`, `newBinary`, `LoadError.Error`, `replaceHint`)
- Modify: `internal/graph/tests.go:137-144` (test copies use `trimTo`)
- Modify: `internal/graph/graph_test.go:227-228` (the old rejection of `replace`)
- Create: `internal/graph/replace_test.go`
- Modify: `internal/compile/cgo_test.go:237-246` (one `TestTrimRoot` case)

**Interfaces:**
- Consumes: `modinfo.Module.Replace` (Task 1).
- Produces:
  - `func trimTo(m *golist.Module, importPath string) string`.
  - `Graph.DepModules map[string]modinfo.Module`, keyed by the module path of the `require` line.
  - `const replaceHint = "a directory replace must point inside src; set src to a directory that holds both modules and modRoot to the one with go.mod"`. `LoadError.Error()` appends it when a problem contains `replacement directory` or starts with `replace `.
  - The outside-`src` problem: `replace <m.Path> => <m.Replace.Path>: <m.Replace.Dir> is outside src <src>`.

- [ ] **Step 1: Write the failing tests**

Create `internal/graph/replace_test.go` (new names must not collide with the existing `mainMod`, `colorMod`, `isattyMod`, `sysMod`, `cmpMod`, `testEnv`, `sums`):

```go
package graph

import (
	"errors"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/draganm/gonixgo/internal/golist"
)

// The program example.com/app lives in /src/app and replaces three of the
// modules it requires.
var (
	appMod = &golist.Module{Path: "example.com/app", Main: true, Dir: "/src/app", GoVersion: "1.22"}
	// libMod is required at v1.2.3 and replaced by the directory ../lib.
	libMod = &golist.Module{Path: "example.com/lib", Version: "v1.2.3", Dir: "/src/lib", GoVersion: "1.21",
		Replace: &golist.Module{Path: "../lib", Dir: "/src/lib", GoVersion: "1.21"}}
	// cmpReplaced is required at v0.6.0 and replaced by v0.7.0.
	cmpReplaced = &golist.Module{Path: "github.com/google/go-cmp", Version: "v0.6.0", Dir: "/mod/github.com/google/go-cmp@v0.7.0", GoVersion: "1.21",
		Replace: &golist.Module{Path: "github.com/google/go-cmp", Version: "v0.7.0", Dir: "/mod/github.com/google/go-cmp@v0.7.0", GoVersion: "1.21", Sum: "h1:cmp7"}}
	// forkMod is required at v1.0.0 and replaced by a fork under another
	// path.
	forkMod = &golist.Module{Path: "github.com/a/b", Version: "v1.0.0", Dir: "/mod/github.com/fork/b@v1.0.1", GoVersion: "1.20",
		Replace: &golist.Module{Path: "github.com/fork/b", Version: "v1.0.1", Dir: "/mod/github.com/fork/b@v1.0.1", GoVersion: "1.20", Sum: "h1:fork"}}

	// go.sum holds lines for the replacements only.
	replaceSums = map[string]string{"github.com/google/go-cmp@v0.7.0": "h1:cmp7", "github.com/fork/b@v1.0.1": "h1:fork"}
)

// replacedPackages is go list -deps output for example.com/app, which
// imports a package of each replaced module.
func replacedPackages() []golist.Package {
	return []golist.Package{
		std("fmt"),
		{ImportPath: "example.com/lib/sub", Name: "sub", Dir: "/src/lib/sub", Module: libMod, DepOnly: true,
			GoFiles: []string{"sub.go"}, TestGoFiles: []string{"sub_test.go"}, Imports: []string{"fmt"}},
		{ImportPath: "github.com/google/go-cmp/cmp", Name: "cmp", Dir: cmpReplaced.Dir + "/cmp", Module: cmpReplaced, DepOnly: true,
			GoFiles: []string{"compare.go"}},
		{ImportPath: "github.com/a/b/c", Name: "c", Dir: forkMod.Dir + "/c", Module: forkMod, DepOnly: true,
			GoFiles: []string{"c.go"}},
		{ImportPath: "example.com/app", Name: "main", Dir: "/src/app", Module: appMod,
			GoFiles: []string{"main.go"}, TestGoFiles: []string{"main_test.go"},
			Imports: []string{"example.com/lib/sub", "fmt", "github.com/a/b/c", "github.com/google/go-cmp/cmp"}},
	}
}

func buildReplaced(t *testing.T, pkgs []golist.Package) *Graph {
	t.Helper()
	g, err := Build(Input{Packages: pkgs, Src: "/src", Env: testEnv, Sums: replaceSums})
	if err != nil {
		t.Fatal(err)
	}
	return g
}

// A module replaced by a directory inside src builds like the main
// module's packages, but compiles with its own go directive, keeps the
// path and version it is required at in its trim path, and is not tested.
func TestBuildDirectoryReplacement(t *testing.T) {
	g := buildReplaced(t, replacedPackages())
	want := &Package{
		ImportPath: "example.com/lib/sub", Name: "golocal-example.com-lib-sub", SrcName: "gosrc-example.com-lib-sub",
		Local: true, ModulePath: "example.com/lib", Subdir: "lib/sub", TrimTo: "example.com/lib@v1.2.3/sub", Lang: "go1.21",
		GoFiles: []string{"sub.go"}, SrcFiles: []string{"lib/sub/sub.go"},
	}
	if got := g.Packages["example.com/lib/sub"]; !reflect.DeepEqual(got, want) {
		t.Errorf("lib/sub =\n%+v\nwant\n%+v", got, want)
	}
	if want := []string{"example.com/app"}; !reflect.DeepEqual(g.Tested, want) {
		t.Errorf("Tested = %v, want %v", g.Tested, want)
	}
}

// A module replaced by another version is fetched at that version and
// keeps the version it is required at in its trim path.
func TestBuildVersionReplacement(t *testing.T) {
	g := buildReplaced(t, replacedPackages())
	wantPkg := &Package{
		ImportPath: "github.com/google/go-cmp/cmp", Name: "gopkg-github.com-google-go-cmp-cmp-v0.7.0",
		ModuleKey: "github.com/google/go-cmp@v0.7.0", ModulePath: "github.com/google/go-cmp", Subdir: "cmp",
		TrimTo: "github.com/google/go-cmp@v0.6.0/cmp", Lang: "go1.21", GoFiles: []string{"compare.go"},
	}
	if got := g.Packages["github.com/google/go-cmp/cmp"]; !reflect.DeepEqual(got, wantPkg) {
		t.Errorf("cmp =\n%+v\nwant\n%+v", got, wantPkg)
	}
	wantMod := &Module{
		Key: "github.com/google/go-cmp@v0.7.0", Path: "github.com/google/go-cmp", Version: "v0.7.0",
		Dir: "/mod/github.com/google/go-cmp@v0.7.0", Sum: "h1:cmp7", Name: "gomod-github.com-google-go-cmp-v0.7.0",
	}
	if got := g.Modules["github.com/google/go-cmp@v0.7.0"]; !reflect.DeepEqual(got, wantMod) {
		t.Errorf("cmp module =\n%+v\nwant\n%+v", got, wantMod)
	}
}

// A fork under another path is fetched under the fork's path; the
// package keeps the original path everywhere else.
func TestBuildForkReplacement(t *testing.T) {
	g := buildReplaced(t, replacedPackages())
	wantPkg := &Package{
		ImportPath: "github.com/a/b/c", Name: "gopkg-github.com-a-b-c-v1.0.1",
		ModuleKey: "github.com/fork/b@v1.0.1", ModulePath: "github.com/a/b", Subdir: "c",
		TrimTo: "github.com/a/b@v1.0.0/c", Lang: "go1.20", GoFiles: []string{"c.go"},
	}
	if got := g.Packages["github.com/a/b/c"]; !reflect.DeepEqual(got, wantPkg) {
		t.Errorf("fork package =\n%+v\nwant\n%+v", got, wantPkg)
	}
	wantMod := &Module{
		Key: "github.com/fork/b@v1.0.1", Path: "github.com/fork/b", Version: "v1.0.1",
		Dir: "/mod/github.com/fork/b@v1.0.1", Sum: "h1:fork", Name: "gomod-github.com-fork-b-v1.0.1",
	}
	if got := g.Modules["github.com/fork/b@v1.0.1"]; !reflect.DeepEqual(got, wantMod) {
		t.Errorf("fork module =\n%+v\nwant\n%+v", got, wantMod)
	}
	if keys := slices.Sorted(maps.Keys(g.Modules)); !reflect.DeepEqual(keys, []string{"github.com/fork/b@v1.0.1", "github.com/google/go-cmp@v0.7.0"}) {
		t.Errorf("modules = %v, want the fork and go-cmp v0.7.0 only", keys)
	}
}

// Two modules replaced by the same module version share its fetch.
func TestBuildSharedReplacement(t *testing.T) {
	other := &golist.Module{Path: "github.com/x/b", Version: "v0.9.0", Dir: forkMod.Dir, GoVersion: "1.20", Replace: forkMod.Replace}
	pkgs := append(replacedPackages(), golist.Package{ImportPath: "github.com/x/b/c", Name: "c", Dir: forkMod.Dir + "/c",
		Module: other, DepOnly: true, GoFiles: []string{"c.go"}})
	g := buildReplaced(t, pkgs)
	a, b := g.Packages["github.com/a/b/c"], g.Packages["github.com/x/b/c"]
	if a.ModuleKey != "github.com/fork/b@v1.0.1" || b.ModuleKey != a.ModuleKey || len(g.Modules) != 2 {
		t.Errorf("module keys %q and %q, %d modules; want both on the fork's one fetch", a.ModuleKey, b.ModuleKey, len(g.Modules))
	}
	if b.TrimTo != "github.com/x/b@v0.9.0/c" {
		t.Errorf("TrimTo = %q, want github.com/x/b@v0.9.0/c", b.TrimTo)
	}
}

// A directory replacement written as an absolute path inside src is local
// too, and module info shows the path as written.
func TestBuildAbsoluteDirectoryReplacement(t *testing.T) {
	pkgs := replacedPackages()
	pkgs[1].Module = &golist.Module{Path: "example.com/lib", Version: "v1.2.3", Dir: "/src/lib", GoVersion: "1.21",
		Replace: &golist.Module{Path: "/src/lib", Dir: "/src/lib", GoVersion: "1.21"}}
	g := buildReplaced(t, pkgs)
	if lib := g.Packages["example.com/lib/sub"]; !lib.Local || lib.TrimTo != "example.com/lib@v1.2.3/sub" {
		t.Errorf("lib/sub = %+v", lib)
	}
	if want := "dep\texample.com/lib\tv1.2.3\n=>\t/src/lib\t(devel)\t\n\n"; !strings.Contains(g.Bins[0].Modinfo, want) {
		t.Errorf("module info lacks %q:\n%s", want, g.Bins[0].Modinfo)
	}
}

// Module info lists each replaced module under the path and version it is
// required at, followed by its replacement, as go build does.
func TestBuildReplacedModuleInfo(t *testing.T) {
	g := buildReplaced(t, replacedPackages())
	want := "path\texample.com/app\n" +
		"mod\texample.com/app\t(devel)\t\n" +
		"dep\texample.com/lib\tv1.2.3\n" +
		"=>\t../lib\t(devel)\t\n\n" +
		"dep\tgithub.com/a/b\tv1.0.0\n" +
		"=>\tgithub.com/fork/b\tv1.0.1\th1:fork\n\n" +
		"dep\tgithub.com/google/go-cmp\tv0.6.0\n" +
		"=>\tgithub.com/google/go-cmp\tv0.7.0\th1:cmp7\n\n" +
		"build\t-buildmode=exe\n"
	if got := g.Bins[0].Modinfo; !strings.HasPrefix(got, want) {
		t.Errorf("module info =\n%s\nwant it to start with\n%s", got, want)
	}
}

// A directory replacement outside src is one problem per module, naming
// the directive and both paths, followed by the fix.
func TestBuildDirectoryReplacementOutsideSrc(t *testing.T) {
	pkgs := append(replacedPackages(), golist.Package{ImportPath: "example.com/lib", Name: "lib", Dir: "/src/lib",
		Module: libMod, DepOnly: true, GoFiles: []string{"lib.go"}})
	_, err := Build(Input{Packages: pkgs, Src: "/src/app", Env: testEnv, Sums: replaceSums})
	var loadErr *LoadError
	if !errors.As(err, &loadErr) {
		t.Fatalf("err = %v, want a *LoadError", err)
	}
	if want := []string{"replace example.com/lib => ../lib: /src/lib is outside src /src/app"}; !reflect.DeepEqual(loadErr.Problems, want) {
		t.Errorf("problems = %q, want %q", loadErr.Problems, want)
	}
	if !strings.HasSuffix(err.Error(), "\n"+replaceHint) {
		t.Errorf("message does not end with the hint:\n%s", err)
	}
}

// Go's error for a replacement directory that is not there gets the hint;
// other load errors do not.
func TestBuildMissingReplacementDirectory(t *testing.T) {
	appMain := golist.Package{ImportPath: "example.com/app", Name: "main", Dir: "/src/app", Module: appMod, GoFiles: []string{"main.go"}}
	_, err := Build(Input{Src: "/src", Env: testEnv, Packages: []golist.Package{
		{ImportPath: "example.com/lib/sub", DepOnly: true, Error: &golist.PackageError{Err: "example.com/lib@v1.2.3: replacement directory ../lib does not exist"}},
		appMain,
	}})
	if err == nil || !strings.Contains(err.Error(), "\n"+replaceHint) {
		t.Errorf("err = %v, want the replace hint", err)
	}
	_, err = Build(Input{Src: "/src", Env: testEnv, Packages: []golist.Package{
		{ImportPath: "example.com/app/missing", DepOnly: true, Error: &golist.PackageError{Err: "package example.com/app/missing is not in std"}},
		appMain,
	}})
	if err == nil || strings.Contains(err.Error(), replaceHint) {
		t.Errorf("err = %v, want a load error without the replace hint", err)
	}
}

// A replaced package that go list -test recompiles against the package
// under test keeps the trim path the package has.
func TestTestCopiesOfReplacedPackages(t *testing.T) {
	tests := []struct {
		name string
		p    golist.Package
		want string
	}{
		{"directory", golist.Package{ImportPath: "example.com/lib/sub [example.com/app/p.test]", Name: "sub", Dir: "/src/lib/sub",
			Module: libMod, ForTest: "example.com/app/p", DepOnly: true, GoFiles: []string{"sub.go"}}, "example.com/lib@v1.2.3/sub"},
		{"version", golist.Package{ImportPath: "github.com/google/go-cmp/cmp [example.com/app/p.test]", Name: "cmp", Dir: cmpReplaced.Dir + "/cmp",
			Module: cmpReplaced, ForTest: "example.com/app/p", DepOnly: true, GoFiles: []string{"compare.go"}}, "github.com/google/go-cmp@v0.6.0/cmp"},
		{"fork", golist.Package{ImportPath: "github.com/a/b/c [example.com/app/p.test]", Name: "c", Dir: forkMod.Dir + "/c",
			Module: forkMod, ForTest: "example.com/app/p", DepOnly: true, GoFiles: []string{"c.go"}}, "github.com/a/b@v1.0.0/c"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pkg, _, err := newTestPackage(&tt.p, "/src", nil, osStat)
			if err != nil {
				t.Fatal(err)
			}
			if pkg.TrimTo != tt.want {
				t.Errorf("TrimTo = %q, want %q", pkg.TrimTo, tt.want)
			}
		})
	}
}
```

In `internal/graph/graph_test.go`, `TestBuildRejectsUnsupported`, replace the `"replace"` case (lines 227-228) with:

```go
		{"replace outside src", golist.Package{ImportPath: "github.com/x/y", Name: "y", Dir: "/elsewhere", DepOnly: true, GoFiles: []string{"y.go"},
			Module: &golist.Module{Path: "github.com/x/y", Version: "v1.0.0", Replace: &golist.Module{Path: "../y", Dir: "/elsewhere"}}}, "replace github.com/x/y => ../y: /elsewhere is outside src /src"},
```

In `internal/compile/cgo_test.go`, `TestTrimRoot`, add this case after the `mod@v1.0.0` one. It already passes: it pins that a directory replacement's C prefix map comes out right from the trim path this task gives it.

```go
		// A package of a module replaced by a directory: cmd/go maps the
		// module's directory to its path and required version.
		{"/s/lib/sub", "example.com/lib@v1.2.3/sub", "/s/lib", "example.com/lib@v1.2.3"},
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/graph/`
Expected: the build fails with `undefined: replaceHint`.

- [ ] **Step 3: Classify replaced modules in `newPackage`**

In `internal/graph/pkg.go`, replace `newPackage` (lines 15-98) with:

```go
// newPackage turns one non-standard go list package into a graph node. It
// also returns the module a third-party package is fetched from.
func newPackage(p *golist.Package, src string, byPath map[string]*golist.Package, stat func(string) (isDir, exists bool)) (*Package, *Module, error) {
	m := p.Module
	if m == nil {
		return nil, nil, fmt.Errorf("%s: not part of a module", p.ImportPath)
	}
	if err := unsupported(p); err != nil {
		return nil, nil, err
	}

	pkg := &Package{
		ImportPath: p.ImportPath,
		IsMain:     p.Name == "main",
		ModulePath: m.Path,
		// For a replaced module go list reports the replacement's go
		// directive.
		Lang:    lang(m.GoVersion),
		TrimTo:  trimTo(m, p.ImportPath),
		GoFiles: p.GoFiles,
		SFiles:  p.SFiles,
		Embed:   embedMap(p.EmbedPatterns, p.EmbedFiles),
	}
	deps, err := deps(p, byPath)
	if err != nil {
		return nil, nil, err
	}
	pkg.Deps = deps
	if len(p.CgoFiles) > 0 {
		pkg.Cgo = &Cgo{
			PkgName:   p.Name,
			CgoFiles:  p.CgoFiles,
			CFiles:    p.CFiles,
			CXXFiles:  p.CXXFiles,
			MFiles:    p.MFiles,
			CPPFLAGS:  restoreSrcDir(p.CgoCPPFLAGS, p.Dir),
			CFLAGS:    restoreSrcDir(p.CgoCFLAGS, p.Dir),
			CXXFLAGS:  restoreSrcDir(p.CgoCXXFLAGS, p.Dir),
			LDFLAGS:   restoreSrcDir(p.CgoLDFLAGS, p.Dir),
			PkgConfig: p.CgoPkgConfig,
		}
	}

	// The main module, and a module replaced by a directory, build from
	// src.
	if m.Main || m.Replace != nil && m.Replace.Version == "" {
		rel, err := filepath.Rel(src, p.Dir)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			if !m.Main {
				return nil, nil, fmt.Errorf("replace %s => %s: %s is outside src %s", m.Path, m.Replace.Path, m.Replace.Dir, src)
			}
			return nil, nil, fmt.Errorf("%s: directory %s is outside src %s", p.ImportPath, p.Dir, src)
		}
		pkg.Local = true
		pkg.Name = storepath.SanitizeName("golocal-" + p.ImportPath)
		pkg.SrcName = storepath.SanitizeName("gosrc-" + p.ImportPath)
		pkg.Subdir = slashDir(rel)
		pkg.SrcFiles = srcFiles(pkg.Subdir, p)
		if pkg.Cgo != nil {
			if err := addNamedPaths(pkg, p, src, stat); err != nil {
				return nil, nil, err
			}
		}
		return pkg, nil, nil
	}

	// Any other module is fetched: the module itself, or the module
	// version that replaces it.
	fetched := m
	if m.Replace != nil {
		fetched = m.Replace
	}
	if fetched.Version == "" || m.Dir == "" {
		return nil, nil, fmt.Errorf("%s: module %s has no version or is not in the module cache", p.ImportPath, fetched.Path)
	}
	rel, err := filepath.Rel(m.Dir, p.Dir)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, nil, fmt.Errorf("%s: directory %s is outside module directory %s", p.ImportPath, p.Dir, m.Dir)
	}
	key := fetched.Path + "@" + fetched.Version
	pkg.Name = storepath.SanitizeName("gopkg-" + p.ImportPath + "-" + fetched.Version)
	pkg.ModuleKey = key
	pkg.Subdir = slashDir(rel)
	mod := &Module{
		Key:     key,
		Path:    fetched.Path,
		Version: fetched.Version,
		// For a replaced module go list reports the replacement's
		// directory.
		Dir:  m.Dir,
		Name: storepath.SanitizeName("gomod-" + fetched.Path + "-" + fetched.Version),
	}
	return pkg, mod, nil
}

// trimTo is what -trimpath rewrites the directory of package importPath,
// of module m, to, as cmd/go computes it: the import path in the main
// module, which has no version; elsewhere the module's path and version,
// those of its require line even when it is replaced, followed by the
// rest of the import path.
func trimTo(m *golist.Module, importPath string) string {
	if m.Version == "" {
		return importPath
	}
	return m.Path + "@" + m.Version + strings.TrimPrefix(importPath, m.Path)
}
```

- [ ] **Step 4: Module info entries, the tested set and the hint in `graph.go`**

In `internal/graph/graph.go`:

Add to `Graph`, after `Modules`:

```go
	// DepModules are the module info entries of the modules, other than
	// the main one, that provide the program's packages, by the path they
	// are required under.
	DepModules map[string]modinfo.Module
```

In `Build`, initialise it next to `Modules` (`DepModules: map[string]modinfo.Module{},`) and replace the body of the per-package loop's success path:

```go
		g.Packages[pkg.ImportPath] = pkg
		g.addModule(mod, in.Sums)
		g.addDepModule(p.Module, in.Sums)
		// Only the main module is tested, as under go test ./... in it.
		if p.Module.Main && len(p.TestGoFiles)+len(p.XTestGoFiles) > 0 {
			g.Tested = append(g.Tested, pkg.ImportPath)
		}
```

Add after `addModule`:

```go
// addDepModule records the module info entry of m, a module that provides
// a package, unless m is the main module: its path and version as
// required, and for a replaced module the replacement's path, version and
// sum. A directory replacement shows as written, with the version (devel)
// and no sum.
func (g *Graph) addDepModule(m *golist.Module, sums map[string]string) {
	if m.Main {
		return
	}
	d := modinfo.Module{Path: m.Path, Version: m.Version}
	switch r := m.Replace; {
	case r == nil:
		d.Sum = sums[m.Path+"@"+m.Version]
	case r.Version == "":
		d.Replace = &modinfo.Module{Path: r.Path, Version: "(devel)"}
	default:
		d.Replace = &modinfo.Module{Path: r.Path, Version: r.Version, Sum: sums[r.Path+"@"+r.Version]}
	}
	g.DepModules[m.Path] = d
}
```

In `newBinary`, replace the loop that collects `mods` with:

```go
	var mods []modinfo.Module
	seen := map[string]bool{}
	for _, ip := range deps {
		path := g.Packages[ip].ModulePath
		d, ok := g.DepModules[path]
		if !ok || seen[path] {
			continue
		}
		seen[path] = true
		mods = append(mods, d)
	}
```

Add before `LoadError.Error` and replace that method:

```go
// replaceHint follows load problems that come from a directory
// replacement outside src: Go's, for a directory missing from the store,
// or newPackage's, for one that is there but outside src.
const replaceHint = "a directory replace must point inside src; set src to a directory that holds both modules and modRoot to the one with go.mod"

func (e *LoadError) Error() string {
	var b strings.Builder
	what := "packages"
	if e.Tests {
		what = "tests"
	}
	fmt.Fprintf(&b, "%d problem(s) loading %s:", len(e.Problems), what)
	tidy, replace := false, false
	for _, p := range e.Problems {
		b.WriteString("\n  " + p)
		tidy = tidy || strings.Contains(p, "go.sum")
		replace = replace || strings.Contains(p, "replacement directory") || strings.HasPrefix(p, "replace ")
	}
	if tidy {
		b.WriteString("\nrun `go mod tidy` to update go.sum")
	}
	if replace {
		b.WriteString("\n" + replaceHint)
	}
	if e.Tests {
		b.WriteString("\nset doCheck = false to build without tests")
	}
	return b.String()
}
```

- [ ] **Step 5: Test copies take the shared trim path**

In `internal/graph/tests.go`, `newTestPackage`, replace the `default:` branch (lines 137-143) with:

```go
	default:
		pkg.TrimTo = trimTo(p.Module, base)
		if pkg.Local {
			pkg.SrcName = storepath.SanitizeName("gosrc-" + base)
		}
	}
```

- [ ] **Step 6: Run the tests**

Run: `go test ./internal/graph/ ./internal/compile/`
Expected: PASS. The existing `TestBuildPackages`, `TestBuildBinary` and `TestAddTestsPackages` keep their expectations: `trimTo` gives the old values for unreplaced modules.

- [ ] **Step 7: Commit**

```bash
git add internal/graph/ internal/compile/cgo_test.go
git commit -q -F - <<'EOF'
feat(graph): build modules that go.mod replaces

A version replacement is fetched under the replacement's path and
version; a directory replacement inside src builds from src. Both keep
the trim path and module info that go build -trimpath gives them, and
only the main module is tested.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01AdA5UEs8Aiwy6pwwGeaNzo
EOF
```

---

### Task 3: GOARM in every tool run

**Files:**
- Modify: `internal/gotool/gotool.go:15-98` (`Toolchain.GOARM`, `New`, `environ`)
- Modify: `internal/compile/compile.go:21-41`, `internal/link/link.go:20-37` (manifest `goarm`)
- Modify: `internal/gotool/gotool_test.go`, `internal/cc/cc_test.go:207` (calls of `New`)
- Test: `internal/gotool/gotool_test.go`

**Interfaces:**
- Produces: `gotool.New(goBin, goos, goarch, goarm, workDir string) (*Toolchain, error)`; `Toolchain.GOARM string`; `compile.Manifest.GOARM` and `link.Manifest.GOARM`, both `json:"goarm"`.

- [ ] **Step 1: Write the failing test**

Append to `internal/gotool/gotool_test.go`:

```go
// GOARM reaches every tool run, and the assembler's defines follow it.
func TestNewARMVersion(t *testing.T) {
	tc, err := New(testutil.Go(t), "linux", "arm", "6", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got := envMap(t, tc.environ())["GOARM"]; got != "6" {
		t.Errorf("GOARM = %q in the tool environment, want 6", got)
	}
	want := []string{"-D", "GOOS_linux", "-D", "GOARCH_arm", "-D", "GOARM_6", "-D", "GOARM_5"}
	if got := tc.AsmDefines(); !reflect.DeepEqual(got, want) {
		t.Errorf("AsmDefines = %v, want %v", got, want)
	}

	// Without one, Go's default applies and the environment sets none.
	tc, err = New(testutil.Go(t), "linux", "arm64", "", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := envMap(t, tc.environ())["GOARM"]; ok {
		t.Errorf("GOARM = %q in the tool environment, want none", got)
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/gotool/ -run TestNewARMVersion`
Expected: the build fails with `too many arguments in call to New`.

- [ ] **Step 3: Implement**

In `internal/gotool/gotool.go`, add to `Toolchain` after `GOARCH`:

```go
	GOARM   string // the ARM version, "" for Go's default
```

Change `New`:

```go
// New asks goBin about itself. goos and goarch may be empty for the host,
// and goarm for Go's default. workDir is a scratch directory the
// toolchain may write to.
func New(goBin, goos, goarch, goarm, workDir string) (*Toolchain, error) {
	t := &Toolchain{GOOS: goos, GOARCH: goarch, GOARM: goarm, home: filepath.Join(workDir, "home")}
```

(the rest of `New` is unchanged), and in `environ`, after the `GOARCH` block:

```go
	if t.GOARM != "" {
		env = append(env, "GOARM="+t.GOARM)
	}
```

Update every call of `New`:
- `internal/gotool/gotool_test.go`: `New(testutil.Go(t), "", "", t.TempDir())` → `New(testutil.Go(t), "", "", "", t.TempDir())` (and the same with `work`), and `New(testutil.Go(t), "linux", "amd64", t.TempDir())` → `New(testutil.Go(t), "linux", "amd64", "", t.TempDir())`.
- `internal/cc/cc_test.go:207`: `gotool.New(testutil.Go(t), "", "", t.TempDir())` → `gotool.New(testutil.Go(t), "", "", "", t.TempDir())`.

In `internal/compile/compile.go`, add to `Manifest` after `GOARCH`:

```go
	GOARM      string              `json:"goarm"`      // "" for Go's default
```

and call `gotool.New(m.Go, m.GOOS, m.GOARCH, m.GOARM, workDir)`.

In `internal/link/link.go`, add to `Manifest` after `GOARCH`:

```go
	GOARM      string   `json:"goarm"` // "" for Go's default
```

and call `gotool.New(m.Go, m.GOOS, m.GOARCH, m.GOARM, workDir)`. Run `gofmt -w internal/compile/compile.go internal/link/link.go` so the struct tags line up.

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/gotool/ ./internal/compile/ ./internal/link/ ./internal/cc/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/gotool/ internal/compile/ internal/link/ internal/cc/
git commit -q -F - <<'EOF'
feat(gotool): pass GOARM to every tool run

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01AdA5UEs8Aiwy6pwwGeaNzo
EOF
```

---

### Task 4: The resolver: GOARM, cross builds and `modRoot`

**Files:**
- Modify: `internal/golist/golist.go:76-145` (`Options.GOARM`)
- Modify: `internal/resolve/resolve.go` (`Args`, `Run`, new `moduleDir`)
- Modify: `internal/graph/graph.go` (`LoadError.CrossCgoOff`, `crossCgoHint`)
- Test: `internal/resolve/resolve_test.go`

**Interfaces:**
- Consumes: `graph.LoadError` and its hints (Task 2).
- Produces:
  - `resolve.Args.GOARM string \`json:"goarm"\`` and `resolve.Args.Cross bool \`json:"cross"\``, which `nix/build-go-application.nix` passes in Task 5.
  - `golist.Options.GOARM string`.
  - `graph.LoadError.CrossCgoOff bool`; its message then ends with `cgo is off in a cross build; set CGO_ENABLED = 1 to build cgo packages`.

- [ ] **Step 1: Write the failing tests**

Append to `internal/resolve/resolve_test.go`:

```go
// modRoot must be a directory inside src that holds go.mod.
func TestRunModRootErrors(t *testing.T) {
	src := testutil.WriteTree(t, appFiles)
	for _, tt := range []struct{ modRoot, want string }{
		{"../x", `modRoot "../x" must be a directory inside src`},
		{"/abs", `modRoot "/abs" must be a directory inside src`},
		{"internal", `modRoot "internal": no go.mod in `},
		{"nope", `modRoot "nope": no go.mod in `},
	} {
		if _, err := run(t, Args{Src: src, ModRoot: tt.modRoot, SubPackages: []string{"."}}); err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("modRoot %q: err = %v, want %q", tt.modRoot, err, tt.want)
		}
	}
}

// modRoot means the same however it is spelt, and "" is src itself.
func TestRunModRootSpellings(t *testing.T) {
	files := map[string]string{}
	for name, content := range appFiles {
		files["services/api/"+name] = content
	}
	src := testutil.WriteTree(t, files)
	for _, modRoot := range []string{"services/api", "./services/api/", "services//api"} {
		if _, err := run(t, Args{Src: src, ModRoot: modRoot, SubPackages: []string{"."}}); err != nil {
			t.Errorf("modRoot %q: %v", modRoot, err)
		}
	}
	if _, err := run(t, Args{Src: testutil.WriteTree(t, appFiles), SubPackages: []string{"."}}); err != nil {
		t.Errorf(`modRoot "": %v`, err)
	}
}

// GOARM reaches go env and go list, so module info records it.
func TestRunGOARM(t *testing.T) {
	out, err := run(t, Args{Src: testutil.WriteTree(t, appFiles), ModRoot: ".", SubPackages: []string{"."},
		GOOS: "linux", GOARCH: "arm", GOARM: "6"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `build\tGOARM=6\n`) {
		t.Fatalf("module info does not record GOARM=6:\n%s", out)
	}
}

// A cross build leaves cgo off unless asked for.
func TestRunCrossTurnsCgoOff(t *testing.T) {
	src := testutil.WriteTree(t, appFiles)
	out, err := run(t, Args{Src: src, ModRoot: ".", SubPackages: []string{"."}, GOOS: "linux", GOARCH: "arm64", Cross: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "cgoEnabled = false;") {
		t.Errorf("a cross build resolved with cgo on:\n%s", out)
	}
	on := true
	out, err = run(t, Args{Src: src, ModRoot: ".", SubPackages: []string{"."}, GOOS: "linux", GOARCH: "arm64", Cross: true, CgoEnabled: &on})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "cgoEnabled = true;") {
		t.Errorf("CGO_ENABLED = 1 in a cross build resolved with cgo off:\n%s", out)
	}
}

// When cgo is off only because the build is cross, a package that needs
// it fails with a line saying how to turn it on.
func TestRunCrossCgoHint(t *testing.T) {
	src := testutil.WriteTree(t, cgoAppFiles)
	_, err := run(t, Args{Src: src, ModRoot: ".", SubPackages: []string{"."}, GOOS: "linux", GOARCH: "arm64", Cross: true})
	if err == nil || !strings.HasSuffix(err.Error(), "\ncgo is off in a cross build; set CGO_ENABLED = 1 to build cgo packages") {
		t.Errorf("err = %v, want it to end with the cgo hint", err)
	}
	off := false
	_, err = run(t, Args{Src: src, ModRoot: ".", SubPackages: []string{"."}, GOOS: "linux", GOARCH: "arm64", Cross: true, CgoEnabled: &off})
	if err == nil || strings.Contains(err.Error(), "cgo is off in a cross build") {
		t.Errorf("err = %v, want a load error without the cgo hint", err)
	}
}
```

`TestRunModRootSpellings` passes before and after: it pins the spellings that Step 3's check must keep accepting.

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/resolve/`
Expected: the build fails with `unknown field GOARM in struct literal of type Args` (and `Cross`).

- [ ] **Step 3: Implement**

In `internal/golist/golist.go`, add to `Options` after `GOARCH`:

```go
	GOARM      string            // "" for Go's default
```

and in `environWith`, after the `GOARCH` block:

```go
	if o.GOARM != "" {
		set["GOARM"] = o.GOARM
	}
```

In `internal/graph/graph.go`, give `LoadError` a field and a hint:

```go
// LoadError lists everything that keeps a graph from being built.
type LoadError struct {
	Problems []string
	Tests    bool // the problems are in the -test pass
	// CrossCgoOff says cgo is off only because the build is cross, which
	// may be why packages did not load.
	CrossCgoOff bool
}

// crossCgoHint follows load problems in a cross build that left cgo off.
const crossCgoHint = "cgo is off in a cross build; set CGO_ENABLED = 1 to build cgo packages"
```

and in `LoadError.Error`, after the `replace` block:

```go
	if e.CrossCgoOff {
		b.WriteString("\n" + crossCgoHint)
	}
```

In `internal/resolve/resolve.go`, add `"errors"` to the imports and to `Args` after `GOARCH`:

```go
	GOARM       string   `json:"goarm"`      // "" for Go's default
	Cross       bool     `json:"cross"`      // build and host platforms differ: cgo is off unless asked for
```

In `Run`, replace `dir := filepath.Join(src, filepath.FromSlash(a.ModRoot))` with:

```go
	dir, err := moduleDir(src, a.ModRoot)
	if err != nil {
		return err
	}
```

add `GOARM: a.GOARM,` to the `golist.Options` literal after `GOARCH`, and replace the `if a.CgoEnabled != nil { … }` block with:

```go
	// nixpkgs' Go turns cgo on for every target, so a cross build would
	// need a C toolchain for the target even for pure Go. Unless asked
	// for, cgo is off there.
	crossCgoOff := a.Cross && a.CgoEnabled == nil
	switch {
	case a.CgoEnabled != nil:
		o.CgoEnabled = "0"
		if *a.CgoEnabled {
			o.CgoEnabled = "1"
		}
	case crossCgoOff:
		o.CgoEnabled = "0"
	}
```

Replace the `graph.Build` error check with:

```go
	g, err := graph.Build(graph.Input{Packages: pkgs, Src: src, Env: env, Tags: a.Tags, Sums: sums})
	if err != nil {
		var loadErr *graph.LoadError
		if crossCgoOff && errors.As(err, &loadErr) {
			loadErr.CrossCgoOff = true
		}
		return err
	}
```

and add after `Run`:

```go
// moduleDir is the directory of go.mod: modRoot, which must stay inside
// src. "" means src itself.
func moduleDir(src, modRoot string) (string, error) {
	rel := filepath.FromSlash(modRoot)
	if rel == "" {
		rel = "."
	}
	if !filepath.IsLocal(rel) {
		return "", fmt.Errorf("modRoot %q must be a directory inside src", modRoot)
	}
	dir := filepath.Join(src, rel)
	if _, err := os.Stat(filepath.Join(dir, "go.mod")); err != nil {
		return "", fmt.Errorf("modRoot %q: no go.mod in %s", modRoot, dir)
	}
	return dir, nil
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/resolve/ ./internal/golist/ ./internal/graph/`
Expected: PASS.

- [ ] **Step 5: Run the whole suite and commit**

Run: `go test ./...`
Expected: PASS.

```bash
git add internal/golist/ internal/resolve/ internal/graph/
git commit -q -F - <<'EOF'
feat(resolve): take GOARM, leave cgo off in cross builds, check modRoot

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01AdA5UEs8Aiwy6pwwGeaNzo
EOF
```

---

### Task 5: Cross builds in Nix

**Files:**
- Modify: `nix/mk-go-env.nix`, `nix/stdlib.nix`, `nix/builders.nix:4-125`, `nix/build-go-application.nix:3,36-49`
- Modify: `flake.nix:27-37`
- Modify: `tests/fixtures.nix`
- Modify: `tests/run.sh`

**Interfaces:**
- Consumes: `resolve.Args` `goarm` and `cross` (Task 4); manifest `goarm` (Task 3).
- Produces:
  - `tests/fixtures.nix` takes `{ goEnv, pkgs, mkGoEnv, linuxPkgs }` and defines `helloArgs`, `cgoArgs = p: { … }`, and the fixtures `hello-deps-aarch64-linux`, `hello-deps-armv6l-linux`, `cgo-aarch64-linux` and `hello-deps-x86_64-linux`.
  - `tests/run.sh <function> [args]` runs one check or section. New helpers: `check_modinfo_in <fixture> <dir under tests/fixtures> <binary> <main package> <shell or -> [VAR=value…]`, `check_file_type <fixture> <binary> <text>`, and the section `cross_checks`.

- [ ] **Step 1: Record the native derivations that must not change**

Run:

```bash
nix eval --impure --json --expr 'let e = (builtins.getFlake "path:'"$PWD"'").legacyPackages.${builtins.currentSystem}.goEnv; in [ e.go.drvPath (e.stdlib false).drvPath (e.stdlib true).drvPath ]'
```

Expected: three `.drv` paths. Save the output in the ledger; Step 8 compares against it.

- [ ] **Step 2: The fixtures**

In `flake.nix`, pass the fixtures `mkGoEnv` and a Linux package set:

```nix
          fixtures = import ./tests/fixtures.nix {
            inherit goEnv pkgs mkGoEnv;
            # A platform this machine cannot build for, so that evalPkgs
            # is needed to resolve it.
            linuxPkgs = nixpkgs.legacyPackages.x86_64-linux;
          };
```

In `tests/fixtures.nix`, change the argument line to
`{ goEnv, pkgs, mkGoEnv, linuxPkgs }:`, keep `testsArgs` as it is, and add
after it, before `in`:

```nix
  helloArgs = {
    pname = "hello-deps";
    src = ./fixtures/hello-deps;
  };

  # The cgo fixture's arguments, with its libraries from the package set p.
  cgoArgs = p: {
    pname = "cgofix";
    src = ./fixtures/cgo;
    packageOverrides = {
      # The program prints EXTRA. Setting CGO_CFLAGS replaces its default.
      "example.com/cgofix/internal/cadd".env.CGO_CFLAGS = "-O2 -g -DEXTRA=10";
      "example.com/cgofix/internal/zstd" = {
        buildInputs = [ p.zstd ];
        nativeBuildInputs = [ p.pkg-config ];
      };
      # No pkg-config: the library reaches the link through buildInputs.
      "example.com/cgofix/internal/lz4".buildInputs = [ p.lz4 ];
    };
  };

  # Cross builds take their target from the package set.
  arm64 = pkgs.pkgsCross.aarch64-multiplatform;
  arm64Env = mkGoEnv { pkgs = arm64; };
  armv6Env = mkGoEnv { pkgs = pkgs.pkgsCross.raspberryPi; };
```

Then make the two existing entries `hello-deps = goEnv.buildGoApplication helloArgs;`
and `cgo = goEnv.buildGoApplication (cgoArgs pkgs);`; they are the same
attribute sets as before. After `testsWith`, add:

```nix
  # Linux builds made here with the native Go. They cannot run here.
  hello-deps-aarch64-linux = arm64Env.buildGoApplication helloArgs;
  hello-deps-armv6l-linux = armv6Env.buildGoApplication helloArgs;
  # cgo stays off in a cross build unless asked for, so evaluating this
  # fails on the package that needs it.
  cgo-aarch64-linux = arm64Env.buildGoApplication (cgoArgs arm64);
  # Only evaluated: this machine resolves, a Linux machine would build.
  hello-deps-x86_64-linux =
    (mkGoEnv { pkgs = linuxPkgs; evalPkgs = pkgs; }).buildGoApplication helloArgs;
```

- [ ] **Step 3: The checks**

In `tests/run.sh`, replace `check_modinfo` with these two functions:

```bash
# check_modinfo <fixture> <binary> <main package, relative to the fixture> [shell]
# The module info must match what `go build -trimpath` embeds. A fixture
# whose reference build needs libraries names the flake's shell that has
# them.
check_modinfo() {
  check_modinfo_in "$1" "$1" "$2" "$3" "${4:--}"
}

# check_modinfo_in <fixture> <directory under tests/fixtures> <binary> <main package> <shell, or -> [VAR=value...]
# check_modinfo for a fixture built from another directory, or whose
# reference build needs settings, such as a cross target's.
check_modinfo_in() {
  local fixture="$1" dir="$2" binary="$3" main="$4" shell="$5" out go tmp
  shift 5
  local -a in_shell=()
  out="$(build "fixtures.$fixture")"
  go="$(build "fixtures.$fixture.go")/bin/go"
  if [ "$shell" != - ]; then
    in_shell=(nix develop "$flake#$shell" --command)
  fi
  tmp="$(mktemp -d)"
  (cd "$root/tests/fixtures/$dir" &&
    env GOFLAGS=-mod=readonly GOWORK=off GOTOOLCHAIN=local "$@" \
      ${in_shell[@]+"${in_shell[@]}"} "$go" build -trimpath -buildvcs=false -o "$tmp/ref" "$main")
  if ! diff <("$go" version -m "$out/bin/$binary" | tail -n +2) <("$go" version -m "$tmp/ref" | tail -n +2); then
    rm -rf "$tmp"
    fail "$fixture: $binary module info differs from go build -trimpath (left: gonixgo, right: go build)"
  fi
  rm -rf "$tmp"
  echo "ok: $fixture: $binary module info matches go build"
}
```

Add these functions before the first top-level check (`check_run hello-deps …`):

```bash
# check_file_type <fixture> <binary> <text that file(1) prints for it>
# A cross build's binary is for the target.
check_file_type() {
  local out got
  out="$(build "fixtures.$1")"
  got="$(file -b "$out/bin/$2")"
  case "$got" in
    *"$3"*) echo "ok: $1: $2 is $3" ;;
    *) fail "$1: $2 is '$got', want '$3'" ;;
  esac
}

# check_native_go <pkgsCross attribute>
# A cross build runs the cached native Go and tool, not ones built for the
# target. Nothing is built.
check_native_go() {
  local got
  got="$(nix eval --impure --raw --expr "
    let
      flake = builtins.getFlake \"$flake\";
      pkgs = flake.inputs.nixpkgs.legacyPackages.\${builtins.currentSystem};
      env = flake.lib.mkGoEnv { pkgs = pkgs.pkgsCross.$1; };
      native = flake.legacyPackages.\${builtins.currentSystem}.goEnv;
    in builtins.toJSON (env.go.drvPath == native.go.drvPath && env.tool.drvPath == native.tool.drvPath)")" ||
    fail "pkgsCross.$1: mkGoEnv does not evaluate"
  [ "$got" = true ] || fail "pkgsCross.$1: mkGoEnv's Go or tool is not the native one"
  echo "ok: pkgsCross.$1: mkGoEnv builds with the native Go and tool"
}

# check_stdlib_name <pkgsCross attribute> <end of the standard library's name>
check_stdlib_name() {
  local got
  got="$(nix eval --impure --raw --expr "
    let flake = builtins.getFlake \"$flake\";
    in ((flake.lib.mkGoEnv { pkgs = flake.inputs.nixpkgs.legacyPackages.\${builtins.currentSystem}.pkgsCross.$1; }).stdlib false).name")" ||
    fail "pkgsCross.$1: the standard library does not evaluate"
  case "$got" in
    *-"$2") echo "ok: pkgsCross.$1: the standard library is $got" ;;
    *) fail "pkgsCross.$1: the standard library is $got, want a name ending in -$2" ;;
  esac
}

# A Go that runs on the target instead of the build platform is refused,
# with the fix. riscv64 is a target no build machine here can run.
check_go_runs_on_build_platform() {
  local msg
  if msg="$(nix eval --impure --raw --expr "
    let
      flake = builtins.getFlake \"$flake\";
      cross = flake.inputs.nixpkgs.legacyPackages.\${builtins.currentSystem}.pkgsCross.riscv64;
    in (flake.lib.mkGoEnv { pkgs = cross; go = cross.go; }).go.version" 2>&1)"; then
    fail "mkGoEnv accepted a Go that runs on the target"
  fi
  case "$msg" in
    *'pkgs.pkgsBuildBuild'*) echo "ok: mkGoEnv refuses a Go that does not run on the build platform" ;;
    *) fail "mkGoEnv: unhelpful error for a Go that runs on the target: $msg" ;;
  esac
}

# check_goarm <fixture> <GOARM>
# The ARM version reaches every compile and link.
check_goarm() {
  local got
  got="$(nix eval "${exec_opt[@]}" --raw "$flake#fixtures.$1" --apply '
    app: builtins.concatStringsSep " " (map (d: d.manifest.goarm or "none")
      (builtins.attrValues app.packages ++ builtins.attrValues app.bins))')" ||
    fail "$1 does not evaluate"
  [ "$(tr ' ' '\n' <<<"$got" | sort -u)" = "$2" ] || fail "$1: the manifests have GOARM [$got], want $2 in each"
  echo "ok: $1: every compile and link has GOARM=$2"
}

# A cross build leaves cgo off unless asked for, and a package that needs
# it says how to turn it on. Nothing is built.
check_cross_cgo_off() {
  local msg
  if msg="$(nix eval "${exec_opt[@]}" --raw "$flake#fixtures.cgo-aarch64-linux.drvPath" 2>&1)"; then
    fail "cgo-aarch64-linux: evaluated, so cgo was on"
  fi
  case "$msg" in
    *'cgo is off in a cross build; set CGO_ENABLED = 1 to build cgo packages'*)
      echo "ok: cgo-aarch64-linux: cgo is off, and the error says how to turn it on" ;;
    *) fail "cgo-aarch64-linux: unhelpful error: $msg" ;;
  esac
}

# With evalPkgs this machine resolves a build for a platform it cannot
# build for. Nothing is built.
check_eval_pkgs() {
  local got
  got="$(nix eval "${exec_opt[@]}" --raw "$flake#fixtures.hello-deps-x86_64-linux" --apply '
    app: builtins.concatStringsSep " " (builtins.attrNames (builtins.listToAttrs (map (d: { name = d.system; value = true; })
      ([ app ] ++ builtins.attrValues app.packages ++ builtins.attrValues app.bins))))')" ||
    fail "hello-deps-x86_64-linux does not evaluate"
  [ "$got" = x86_64-linux ] || fail "hello-deps-x86_64-linux: derivations for [$got], want [x86_64-linux]"
  echo "ok: hello-deps-x86_64-linux: resolved here, every derivation for x86_64-linux"
}

# Cross builds: the native Go builds, the target comes from pkgs. Nothing
# built here runs on this machine, so the checks read the binaries.
# The reference builds set CGO_ENABLED=0: nixpkgs' Go would turn it on.
cross_checks() {
  check_native_go aarch64-multiplatform
  check_stdlib_name raspberryPi linux-armv6
  check_go_runs_on_build_platform
  check_file_type hello-deps-aarch64-linux hello "ARM aarch64"
  check_modinfo_in hello-deps-aarch64-linux hello-deps hello . - GOOS=linux GOARCH=arm64 CGO_ENABLED=0
  check_no_source_refs hello-deps-aarch64-linux
  check_file_type hello-deps-armv6l-linux hello "ELF 32-bit LSB executable, ARM"
  check_modinfo_in hello-deps-armv6l-linux hello-deps hello . - GOOS=linux GOARCH=arm GOARM=6 CGO_ENABLED=0
  check_goarm hello-deps-armv6l-linux 6
  check_cross_cgo_off
  check_eval_pkgs
}

# `tests/run.sh <check or section> [arguments]` runs that alone.
if [ $# -gt 0 ]; then
  "$@"
  exit
fi
```

Before the final `check_exec_error` line, add:

```bash
cross_checks
```

- [ ] **Step 4: Run the evaluation-only checks to verify they fail**

These checks build nothing. Do not run `cross_checks` whole before Step 6: with the old default `go`, its builds would compile a cross GCC.

Run: `tests/run.sh check_native_go aarch64-multiplatform`
Expected: `FAIL: pkgsCross.aarch64-multiplatform: mkGoEnv's Go or tool is not the native one`.

Run: `tests/run.sh check_stdlib_name raspberryPi linux-armv6`
Expected: `FAIL: … the standard library is go-stdlib-1.26.7-linux-arm, want a name ending in -linux-armv6`.

Run: `tests/run.sh check_go_runs_on_build_platform`
Expected: `FAIL: mkGoEnv accepted a Go that runs on the target`.

- [ ] **Step 5: The target and the native Go**

Replace `nix/mk-go-env.nix` with:

```nix
# mkGoEnv ties gonixgo to one nixpkgs: that nixpkgs builds the tool and the
# standard library, and performs the Go build. A cross pkgs, such as
# pkgsCross.aarch64-multiplatform, builds for its host platform.
{ pkgs
  # The Go that builds. It runs on the build platform; the target comes
  # from pkgs.
, go ? pkgs.pkgsBuildBuild.go
  # The package set for the evaluating machine, when it differs from the
  # build platform.
, evalPkgs ? null
  # The Go that resolves the graph at evaluation time. It must be the same
  # version as go; by default it is go itself, or evalPkgs.go.
, evalGo ? null
}:
let
  inherit (pkgs) lib;
  buildPkgs = pkgs.buildPackages;
  # What runs on the build platform and builds for it. Natively these are
  # pkgs' own derivations, and cross builds share them.
  nativePkgs = pkgs.pkgsBuildBuild;

  # Derivations run on the build platform and produce code for the target,
  # the platform the program runs on.
  inherit (pkgs.stdenv.buildPlatform) system;
  target = pkgs.stdenv.hostPlatform.go;
  goos = target.GOOS;
  goarch = target.GOARCH;
  # "" except for 32-bit ARM.
  goarm = target.GOARM or "";

  tool = nativePkgs.callPackage ./tool.nix { };

  # The resolver runs on the machine that evaluates.
  evalTool = (if evalPkgs != null then evalPkgs else nativePkgs).callPackage ./tool.nix { };
  evalGo' =
    if evalGo != null then evalGo
    else if evalPkgs != null then evalPkgs.go
    else go;

  # Where go runs, when it says; nixpkgs' Go does.
  goHost = go.stdenv.hostPlatform or null;

  stdlib = import ./stdlib.nix {
    inherit lib go goos goarch goarm;
    inherit (buildPkgs) runCommand;
    # The cgo parts compile with the C compiler for the target.
    inherit (pkgs) runCommandCC;
  };

  builders = import ./builders.nix {
    inherit lib go tool stdlib system goos goarch goarm;
    inherit (buildPkgs) cacert;
    # cgo packages and the binaries that contain them build with the C
    # compiler for the platform the program runs on.
    inherit (pkgs) stdenv;
  };

  buildGoApplication = import ./build-go-application.nix {
    inherit lib go evalTool goos goarch goarm;
    evalGo = evalGo';
    inherit (buildPkgs) runCommand;
    # Whether the build is cross, and whether the build platform can run
    # the tests.
    inherit (pkgs) stdenv;
    mkBuilders = builders;
  };
in
assert lib.assertMsg (goHost == null || pkgs.stdenv.buildPlatform.canExecute goHost)
  "gonixgo: go runs on ${goHost.system} but builds run on ${system}; take go from pkgs.pkgsBuildBuild, such as pkgs.pkgsBuildBuild.go_1_25";
assert lib.assertMsg (evalGo'.version == go.version)
  "gonixgo: evalGo is Go ${evalGo'.version} but the build uses Go ${go.version}; they must be the same version";
{
  inherit buildGoApplication tool go stdlib builders;
}
```

In `nix/stdlib.nix`, take `goarm`, put it in the name and the environment:

```nix
{ lib, go, runCommand, runCommandCC, goos, goarch, goarm }:

cgoEnabled:
let
  # The cgo parts of the standard library need a C compiler for the target.
  run = if cgoEnabled then runCommandCC else runCommand;
  # Builds for two ARM versions get standard libraries of different names.
  arm = lib.optionalString (goarm != "") "v${goarm}";
in
run "go-stdlib-${go.version}-${goos}-${goarch}${arm}${lib.optionalString cgoEnabled "-cgo"}"
{
  nativeBuildInputs = [ go ];
  env = {
    GOOS = goos;
    GOARCH = goarch;
    CGO_ENABLED = if cgoEnabled then "1" else "0";
  } // lib.optionalAttrs (goarm != "") { GOARM = goarm; };
}
```

(the build script is unchanged).

In `nix/builders.nix`, take `goarm` in the first argument set (`{ lib, cacert, stdenv, go, tool, stdlib, system, goos, goarch, goarm }:`), add to the `let`:

```nix
  # The target as the manifests carry it: GOARM only for 32-bit ARM, so
  # the manifests for other targets stay as they were.
  target = { inherit goos goarch; } // lib.optionalAttrs (goarm != "") { inherit goarm; };
```

and build both manifests on it. In `compile`:

```nix
      manifest = target // {
        go = goBin;
        inherit importPath isMain lang goFiles sFiles embed;
```

(the rest of the attribute set is unchanged), and in `link`:

```nix
      manifest = target // {
        go = goBin;
        inherit binName modinfo godebug ldflags;
```

In `nix/build-go-application.nix`, take `goarm` (`{ lib, runCommand, stdenv, go, evalGo, evalTool, mkBuilders, goos, goarch, goarm }:`) and pass it to the resolver: `inherit modRoot subPackages tags goos goarch goarm;`.

- [ ] **Step 6: Run the evaluation-only checks again**

Run: `tests/run.sh check_native_go aarch64-multiplatform && tests/run.sh check_stdlib_name raspberryPi linux-armv6 && tests/run.sh check_go_runs_on_build_platform`
Expected: three `ok:` lines.

- [ ] **Step 7: Verify the cross flag is missing**

Run: `tests/run.sh check_cross_cgo_off`
Expected: `FAIL: cgo-aarch64-linux: evaluated, so cgo was on`. The resolver does not yet know the build is cross, so nixpkgs' Go turned cgo on.

- [ ] **Step 8: Tell the resolver about cross builds**

In `nix/build-go-application.nix`, add to the resolver's JSON, after `doCheck = runTests;`:

```nix
      # A build for another platform: cgo is off there unless asked for.
      cross = stdenv.buildPlatform != stdenv.hostPlatform;
```

Run: `tests/run.sh check_cross_cgo_off`
Expected: `ok: cgo-aarch64-linux: cgo is off, and the error says how to turn it on`.

Run Step 1's command again.
Expected: the same three `.drv` paths.

- [ ] **Step 9: Run the cross builds**

Run: `tests/run.sh cross_checks`
Expected: eleven `ok:` lines. The first run builds the standard library for linux/arm64 and linux/arm (GOARM=6), a minute or two each, and no C toolchain.

- [ ] **Step 10: Run everything**

Run: `go test ./... && tests/run.sh`
Expected: PASS, then `all integration checks passed`.

- [ ] **Step 11: Commit**

```bash
git add nix/ flake.nix tests/
git commit -q -F - <<'EOF'
feat(nix): cross-compile with the native Go and the target from pkgs

go defaults to pkgs.pkgsBuildBuild.go and must run on the build
platform. GOOS, GOARCH and GOARM come from pkgs.stdenv.hostPlatform,
cgo is off in a cross build unless CGO_ENABLED = 1, and the cgo
standard library builds with pkgs.runCommandCC. tests/run.sh runs a
single check or section when given one.

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01AdA5UEs8Aiwy6pwwGeaNzo
EOF
```

---

### Task 6: cgo in a cross build, on x86_64 macOS

**Files:**
- Modify: `tests/fixtures.nix` (`cgo-x86_64-darwin`)
- Modify: `flake.nix` (`cgoShellX86_64Darwin`)
- Modify: `tests/run.sh` (`check_run_x86_64`, `x86_64_darwin_checks`)

**Interfaces:**
- Consumes: `cgoArgs`, `mkGoEnv` in `tests/fixtures.nix`; `check_modinfo_in`, `check_file_type` (Task 5).
- Produces: `fixtures.cgo-x86_64-darwin` and `legacyPackages.cgoShellX86_64Darwin`, on aarch64-darwin only.

- [ ] **Step 1: The checks**

In `tests/run.sh`, add next to the other helpers, above the single-check
guard that Task 5 added:

```bash
# check_run_x86_64 <fixture> <binary> <expected stdout>
# check_run for an x86_64 macOS binary, which runs under Rosetta.
check_run_x86_64() {
  if ! arch -x86_64 /usr/bin/true 2>/dev/null; then
    echo "skip: $1: Rosetta is not installed, so $2 cannot run"
    return
  fi
  check_run "$@"
}

# cgo in a cross build: the cgo fixture for x86_64 macOS, with the cross C
# toolchain and that platform's libraries. It runs here under Rosetta.
x86_64_darwin_checks() {
  check_run_x86_64 cgo-x86_64-darwin cgofix "3 8 7 10 4 4 zstd true saved pure /_/example.com/cgofix/internal/cadd/where.go"
  check_file_type cgo-x86_64-darwin cgofix "x86_64"
  check_modinfo_in cgo-x86_64-darwin cgo cgofix . cgoShellX86_64Darwin GOARCH=amd64 CGO_ENABLED=1
  check_stdenv 'fixtures.cgo-x86_64-darwin.packages."example.com/cgofix/internal/cadd"' true
  check_stdenv 'fixtures.cgo-x86_64-darwin.bins.cgofix' true
}
```

and after `cross_checks` in the top-level sequence:

```bash
if [ "$(uname -s)-$(uname -m)" = Darwin-arm64 ]; then
  x86_64_darwin_checks
fi
```

- [ ] **Step 2: Run them to verify they fail**

Run: `tests/run.sh x86_64_darwin_checks`
Expected: FAIL; `nix build` reports that `fixtures` has no attribute `cgo-x86_64-darwin`.

- [ ] **Step 3: The fixture and the reference shell**

In `tests/fixtures.nix`, bring `lib` into scope (`inherit (pkgs) lib;` at the top of the `let`) and end the file's attribute set with:

```nix
} // lib.optionalAttrs (pkgs.stdenv.buildPlatform.system == "aarch64-darwin") {
  # cgo in a cross build. The binary runs here under Rosetta.
  cgo-x86_64-darwin = (mkGoEnv { pkgs = pkgs.pkgsCross.x86_64-darwin; }).buildGoApplication
    (cgoArgs pkgs.pkgsCross.x86_64-darwin // { CGO_ENABLED = 1; });
}
```

In `flake.nix`, make `legacyPackages`' set end with:

```nix
        } // nixpkgs.lib.optionalAttrs (system == "aarch64-darwin") {
          # What a plain `go build` of the cgo fixture for x86_64 macOS
          # needs: the cross C toolchain, pkg-config and the libraries for
          # that platform.
          cgoShellX86_64Darwin =
            let cross = pkgs.pkgsCross.x86_64-darwin;
            in cross.mkShell {
              packages = [ goEnv.go cross.pkg-config ];
              buildInputs = [ cross.zstd cross.lz4 ];
            };
        });
```

- [ ] **Step 4: Check the shell's toolchain**

Run: `nix develop "path:$PWD#cgoShellX86_64Darwin" --command sh -c 'echo "$CC $PKG_CONFIG"'`
Expected: `x86_64-apple-darwin-clang x86_64-apple-darwin-pkg-config`. The first run builds the x86_64-darwin C toolchain (17 derivations), zstd and lz4; run it in the background with a long timeout. If `$CC` is empty, ledger a ruling and pass `CC=x86_64-apple-darwin-clang` to `check_modinfo_in` with the other settings.

- [ ] **Step 5: Run the checks**

Run: `tests/run.sh x86_64_darwin_checks`
Expected: five `ok:` lines; the first prints what the native cgo fixture prints.

- [ ] **Step 6: Run everything**

Run: `tests/run.sh`
Expected: `all integration checks passed`.

- [ ] **Step 7: Commit**

```bash
git add tests/ flake.nix
git commit -q -F - <<'EOF'
test: build the cgo fixture for x86_64 macOS

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01AdA5UEs8Aiwy6pwwGeaNzo
EOF
```

---

### Task 7: The monorepo fixture

**Files:**
- Create: `tests/fixtures/monorepo/app/go.mod`, `app/go.sum`, `app/main.go`, `app/main_test.go`, `tests/fixtures/monorepo/lib/go.mod`, `lib/lib.go`, `lib/lib_test.go`
- Modify: `tests/fixtures.nix` (`monorepo`, `monorepoWith`)
- Modify: `tests/run.sh` (`monorepo_with`, `check_monorepo_errors`, `monorepo_checks`)

**Interfaces:**
- Consumes: Tasks 1, 2 and 4 (the replace support and the `modRoot` checks); `check_modinfo_in` (Task 5).
- Produces: `fixtures.monorepo`, `fixtures.monorepoWith`.

This fixture checks Tasks 1, 2 and 4 end to end. Its first run should pass; its RED is the missing section.

- [ ] **Step 1: Verify the section is missing**

Run: `tests/run.sh monorepo_checks`
Expected: FAIL with `monorepo_checks: command not found`.

- [ ] **Step 2: The fixture's sources**

`tests/fixtures/monorepo/lib/go.mod`:

```
module example.com/monorepo/lib

go 1.21
```

`tests/fixtures/monorepo/lib/lib.go`:

```go
// Package lib is a sibling module that the program reaches through a
// directory replace.
package lib

import "runtime"

// Where returns the path the compiler recorded for this file.
func Where() string {
	_, file, _, _ := runtime.Caller(0)
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

`tests/fixtures/monorepo/lib/lib_test.go`:

```go
package lib

import "testing"

// The tests of a module behind a directory replace are not the program's,
// and gonixgo does not run them.
func TestNotRun(t *testing.T) {
	t.Fatal("the tests of a replaced module ran")
}
```

`tests/fixtures/monorepo/app/go.mod`:

```
module example.com/monorepo/app

go 1.22

require (
	example.com/monorepo/lib v0.0.0-00010101000000-000000000000
	github.com/google/go-cmp v0.6.0
)

replace example.com/monorepo/lib => ../lib

replace github.com/google/go-cmp => github.com/google/go-cmp v0.7.0
```

`tests/fixtures/monorepo/app/main.go`:

```go
// Command app prints the source paths the compiler recorded for its own
// file, for a sibling module's and for a replaced dependency's, then what
// the sibling module's closures saw.
package main

import (
	"fmt"
	"reflect"
	"runtime"

	"example.com/monorepo/lib"
	"github.com/google/go-cmp/cmp"
)

// fileOf returns the source file the compiler recorded for function f.
func fileOf(f any) string {
	fn := runtime.FuncForPC(reflect.ValueOf(f).Pointer())
	file, _ := fn.FileLine(fn.Entry())
	return file
}

func main() {
	_, file, _, _ := runtime.Caller(0)
	fmt.Println(file, lib.Where(), fileOf(cmp.Equal), lib.Captured())
}
```

`tests/fixtures/monorepo/app/main_test.go`:

```go
package main

import (
	"fmt"
	"testing"

	"example.com/monorepo/lib"
)

// The main module's tests run.
func TestCaptured(t *testing.T) {
	if got := fmt.Sprint(lib.Captured()); got != "[3 3 3]" {
		t.Fatalf("lib.Captured() = %s, want [3 3 3]: lib compiles with its own go directive", got)
	}
}
```

- [ ] **Step 3: `go.sum`, and the reference output**

Run:

```bash
(cd tests/fixtures/monorepo/app && GOFLAGS= GOWORK=off go mod tidy && git diff --exit-code go.mod && cat go.sum)
```

Expected: `go.mod` unchanged; `go.sum` holds exactly the two `github.com/google/go-cmp v0.7.0` lines (`h1:wk8382ETsv4JYUZwIsn6YpYiWiBsYLSJiTsyBybVuN8=` and `/go.mod h1:pXiqmnSA92OHEEa9HXL2W4E7lf9JzCmGVUdgjX3N/iU=`).

Run:

```bash
(cd tests/fixtures/monorepo/app && tmp="$(mktemp -d)" && GOFLAGS=-mod=readonly GOWORK=off go build -trimpath -o "$tmp/app" . && "$tmp/app"; rm -rf "$tmp")
```

Expected: `example.com/monorepo/app/main.go example.com/monorepo/lib@v0.0.0-00010101000000-000000000000/lib.go github.com/google/go-cmp@v0.6.0/cmp/compare.go [3 3 3]`. The binary is deleted.

- [ ] **Step 4: The fixture in Nix**

In `tests/fixtures.nix`, add to the `let`:

```nix
  # The monorepo fixture's arguments, which run.sh varies through
  # monorepoWith.
  monorepoArgs = {
    pname = "monorepo";
    src = ./fixtures/monorepo;
    modRoot = "app";
  };
```

and to the attribute set, after `testsWith`:

```nix
  monorepo = goEnv.buildGoApplication monorepoArgs;
  # The monorepo fixture with the attributes f returns, given the default
  # arguments, laid over them.
  monorepoWith = f: goEnv.buildGoApplication (monorepoArgs // f monorepoArgs);
```

- [ ] **Step 5: The checks**

In `tests/run.sh`, add next to the other helpers, above the single-check
guard:

```bash
# monorepo_with <function from the default arguments to the changes> <expression over app>
# Evaluates the expression with app bound to the monorepo fixture built
# with the changes.
monorepo_with() {
  nix eval --impure --raw "${exec_opt[@]}" --expr "
    let
      flake = builtins.getFlake \"$flake\";
      app = flake.legacyPackages.\${builtins.currentSystem}.fixtures.monorepoWith ($1);
    in $2"
}

# A directory replace must point inside src, and modRoot must hold go.mod;
# the errors say what to change.
check_monorepo_errors() {
  local msg hint='a directory replace must point inside src; set src to a directory that holds both modules and modRoot to the one with go.mod'
  if msg="$(monorepo_with "_: { src = $root/tests/fixtures/monorepo/app; modRoot = \".\"; }" 'app.drvPath' 2>&1)"; then
    fail "monorepo: evaluated with the replaced module missing from src"
  fi
  case "$msg" in
    *'replacement directory ../lib does not exist'*"$hint"*) ;;
    *) fail "monorepo: unhelpful error for a replaced module missing from src: $msg" ;;
  esac
  if msg="$(monorepo_with "_: { src = \"\${$root/tests/fixtures/monorepo}/app\"; modRoot = \".\"; }" 'app.drvPath' 2>&1)"; then
    fail "monorepo: evaluated with the replaced module outside src"
  fi
  case "$msg" in
    *'replace example.com/monorepo/lib => ../lib: '*' is outside src '*"$hint"*) ;;
    *) fail "monorepo: unhelpful error for a replaced module outside src: $msg" ;;
  esac
  if msg="$(monorepo_with '_: { modRoot = "nope"; }' 'app.drvPath' 2>&1)"; then
    fail "monorepo: evaluated with a modRoot that holds no go.mod"
  fi
  case "$msg" in
    *'modRoot "nope": no go.mod in '*) ;;
    *) fail "monorepo: unhelpful error for a modRoot without go.mod: $msg" ;;
  esac
  echo "ok: monorepo: a replace outside src and a modRoot without go.mod are errors that say what to change"
}

# A monorepo: modRoot, a sibling module behind a directory replace, and a
# dependency replaced by another version. The output is the source paths
# the compiler recorded, as go build -trimpath records them, and [3 3 3]
# from lib, which compiles with its own go 1.21.
monorepo_checks() {
  check_run monorepo app "example.com/monorepo/app/main.go example.com/monorepo/lib@v0.0.0-00010101000000-000000000000/lib.go github.com/google/go-cmp@v0.6.0/cmp/compare.go [3 3 3]"
  check_modinfo_in monorepo monorepo/app app . -
  check_tested_set monorepo "example.com/monorepo/app"
  check_fetch_fallback monorepo "github.com/google/go-cmp@v0.7.0"
  check_no_source_refs monorepo
  check_monorepo_errors
}
```

and after the `x86_64_darwin_checks` block in the top-level sequence:

```bash
monorepo_checks
```

- [ ] **Step 6: Run the section**

Run: `tests/run.sh monorepo_checks`
Expected: six `ok:` lines. The build passing shows `lib`'s always-failing test did not run.

- [ ] **Step 7: Run everything**

Run: `go test ./... && tests/run.sh`
Expected: PASS, then `all integration checks passed`.

- [ ] **Step 8: Commit**

```bash
git add tests/
git commit -q -F - <<'EOF'
test: add the monorepo fixture

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01AdA5UEs8Aiwy6pwwGeaNzo
EOF
```

---

### Task 8: Documentation

**Files:**
- Modify: `README.md` (the `mkGoEnv` and `buildGoApplication` tables, new sections, "Not yet supported")
- Modify: `docs/superpowers/specs/2026-10-03-gonixgo-design.md` (lines 80-81, 103, 352, 493-500, 567-568)

**Interfaces:**
- Consumes: the behaviour of Tasks 1-7.

- [ ] **Step 1: README tables**

In the `mkGoEnv` table, replace the `go` and `evalPkgs` rows with:

```markdown
| `go` | `pkgs.pkgsBuildBuild.go` | The Go that builds. It runs on the build platform; the target comes from `pkgs`. |
| `evalPkgs` | `null` | Package set for the evaluating machine, when it differs from the build platform; `null` means `pkgs.pkgsBuildBuild`. |
```

After "To choose a Go version: …", add: "In a cross build, take it from `pkgs.pkgsBuildBuild`, as in `go = pkgs.pkgsBuildBuild.go_1_25`."

In the `buildGoApplication` table, replace the `CGO_ENABLED` row with:

```markdown
| `CGO_ENABLED` | `null` | `null` uses Go's default for the target, which with nixpkgs' Go is on, except in a cross build, where it is off; see [Cross-compilation](#cross-compilation). |
```

- [ ] **Step 2: README sections**

After the `### Tests` section, add:

````markdown
### Cross-compilation

The target is the platform of the `pkgs` you pass to `mkGoEnv`, so a
`pkgsCross` set builds for its platform:

```nix
goEnv = gonixgo.lib.mkGoEnv { pkgs = pkgs.pkgsCross.aarch64-multiplatform; };
```

The Go that builds runs on your machine and is the one the binary cache
holds. `GOOS`, `GOARCH` and, for 32-bit ARM, `GOARM` come from the target. A
Go from the cross set itself, such as `pkgs.pkgsCross.aarch64-multiplatform.go_1_25`,
runs on the target, and `mkGoEnv` refuses it.

cgo is off in a cross build unless you set `CGO_ENABLED = 1`, so a pure-Go
program needs no C toolchain for the target. With it, cgo packages compile
with the cross C compiler, and `packageOverrides` entries take the target's
libraries from the cross set:

```nix
let cross = pkgs.pkgsCross.aarch64-multiplatform; in
(gonixgo.lib.mkGoEnv { pkgs = cross; }).buildGoApplication {
  pname = "app";
  src = ./.;
  CGO_ENABLED = 1;
  packageOverrides."example.com/app/internal/zstd" = {
    buildInputs = [ cross.zstd ];
    nativeBuildInputs = [ cross.pkg-config ];
  };
}
```

Nix builds the cross C toolchain when the binary cache lacks it, which it
does for a Mac building for Linux. When a package needs cgo and cgo is off,
evaluation fails and says to set `CGO_ENABLED = 1`. A package with a pure-Go
fallback for builds without cgo, such as `github.com/mattn/go-sqlite3`, builds
without cgo and fails only when it runs; set `CGO_ENABLED = 1` for those too.

Tests run only when your machine can run the target's binaries.

`evalPkgs` is for something else: building on another machine, such as a
Linux remote builder from a Mac. There `pkgs` is the builder's nixpkgs and
`evalPkgs` your machine's, whose tool and Go resolve the graph:

```nix
gonixgo.lib.mkGoEnv {
  pkgs = nixpkgs.legacyPackages.x86_64-linux;
  evalPkgs = nixpkgs.legacyPackages.aarch64-darwin;
}
```

### Monorepos and `replace`

`modRoot` names the directory of `go.mod` inside `src`, and `subPackages` are
relative to it. A sibling module behind a directory `replace` must be inside
`src`:

```
repo/
  services/api/go.mod   replace example.com/repo/lib => ../../lib
  lib/go.mod
```

```nix
goEnv.buildGoApplication {
  pname = "api";
  src = ./.;              # repo/
  modRoot = "services/api";
}
```

The sibling module's packages build from `src` like the program's own, and
compile with that module's `go` directive. Their tests do not run; only the
main module's packages are tested. A `replace` with another version, or with
a fork under another path, changes what is fetched. Either way the packages
keep their import paths: a `packageOverrides` key is the import path or the
module path the code imports, not the fork's. The binary's module info lists
the replacements as `go build` does.

`go.work` is ignored, so each module's `go.mod` needs its own `replace`
lines. A directory `replace` that leaves `src` fails during evaluation: make
`src` the directory that holds both modules, and `modRoot` the one with the
program.
````

- [ ] **Step 3: README "Not yet supported"**

Replace its first paragraph with:

```markdown
`go.work`, `vendor/` directories, and packages with SWIG, Fortran or `.syso`
files; `.syso` support may come later. Such packages are rejected during
evaluation with a message naming them.
```

- [ ] **Step 4: The 2026-10-03 design**

In `docs/superpowers/specs/2026-10-03-gonixgo-design.md`:
- Line 80: `| \`go\` | \`pkgs.pkgsBuildBuild.go\` | Toolchain used inside derivations; it runs on the build platform. |`
- Line 81: in the `evalPkgs` row, `` `null` means `pkgs.pkgsBuildBuild` ``.
- Line 103: `| \`CGO_ENABLED\` | \`null\` | \`null\` means Go's own default for the target; in a cross build, off. |`
- Line 352, the derivations table: `` `go-stdlib-<version>-<goos>-<goarch>[v<goarm>][-cgo]` ``, one per Go version, target, ARM version and cgo setting.
- Lines 495-500, the Cross-compilation section's first two paragraphs:

```markdown
`GOOS`, `GOARCH` and `GOARM` come from `pkgs.stdenv.hostPlatform.go`. They go
to the resolver, so file lists and build constraints match the target, and to
every derivation.

Derivations run on the build platform with `pkgs.pkgsBuildBuild.go`, the
native Go. cgo is off in a cross build unless `CGO_ENABLED = 1`; cgo packages
then use the cross C compiler from `pkgs.stdenv.cc`. The
[cross-compilation and monorepo design](2026-10-10-cross-monorepo-design.md)
has the details.
```

- Lines 567-568, the fixture table:

```markdown
| `monorepo` | `modRoot`, a sibling module behind a directory `replace`, and a version `replace`. |
| `cross` | Linux arm64 and ARMv6 builds, cgo for x86_64 macOS, and a build for x86_64-linux resolved through `evalPkgs`. |
```

- [ ] **Step 5: Check and commit**

Run: `git diff --check`
Expected: no output.

```bash
git add README.md docs/superpowers/specs/2026-10-03-gonixgo-design.md
git commit -q -F - <<'EOF'
docs: document cross-compilation and monorepos

Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>
Claude-Session: https://claude.ai/code/session_01AdA5UEs8Aiwy6pwwGeaNzo
EOF
```
