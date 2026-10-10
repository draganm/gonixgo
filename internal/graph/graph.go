// Package graph models the build of a Go program as modules, packages and
// binaries, and constructs that model from go list output.
package graph

import (
	"fmt"
	"maps"
	"os"
	"slices"
	"sort"
	"strings"

	"github.com/draganm/gonixgo/internal/golist"
	"github.com/draganm/gonixgo/internal/modinfo"
	"github.com/draganm/gonixgo/internal/storepath"
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
	TrimTo     string // what -trimpath rewrites the source directory to, "" to keep it
	TestSrc    string // compiles from the test source of this tested package, "" otherwise
	TestMain   string // a test main: the source go list generated, "" otherwise
	Lang       string // -lang value, e.g. go1.24
	GoFiles    []string
	SFiles     []string
	Embed      map[string][]string // //go:embed pattern to matched files
	SrcFiles   []string            // local: files to copy, relative to the source root
	SrcTrees   []string            // local: directories to copy whole, relative to the source root, "." for the root
	Deps       []string            // direct non-standard imports, sorted
	Cgo        *Cgo                // nil for a pure package
}

// Cgo is the C side of a cgo package.
type Cgo struct {
	PkgName   string   // Go package name, which cgo -dynpackage needs
	CgoFiles  []string // Go files that import "C"
	CFiles    []string
	CXXFiles  []string
	MFiles    []string
	CPPFLAGS  []string // the #cgo directives, ${SRCDIR} as written
	CFLAGS    []string
	CXXFLAGS  []string
	LDFLAGS   []string
	PkgConfig []string
}

// Binary is one link.
type Binary struct {
	Name    string   // file name in $out/bin
	DrvName string   // derivation name
	Main    string   // import path of the main package
	Deps    []string // transitive non-standard imports of Main, sorted
	Modinfo string   // module info to embed
	Godebug string   // DefaultGODEBUG, "" for none
	Cgo     bool     // Main or one of Deps is a cgo package: the link needs the C toolchain
	CXX     bool     // one of them has C++ files: the C++ compiler links
	Test    bool     // a test binary: testing.Testing reports true
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

	// Tested lists the main-module packages with test files, sorted.
	// AddTests adds their tests.
	Tested []string
	// TestPackages are the test copies "X [P.test]" and the test mains
	// "P.test", by go list import path.
	TestPackages map[string]*Package
	// Tests are by tested import path.
	Tests map[string]*Test
}

// ModuleList returns the modules sorted by key.
func (g *Graph) ModuleList() []*Module {
	mods := make([]*Module, 0, len(g.Modules))
	for _, key := range slices.Sorted(maps.Keys(g.Modules)) {
		mods = append(mods, g.Modules[key])
	}
	return mods
}

// Input is what Build needs from the evaluation-time go commands.
type Input struct {
	Packages []golist.Package  // go list -e -deps output for the main packages
	Src      string            // absolute path of the source root, symlinks resolved
	Env      map[string]string // go env: GOVERSION, GOOS, GOARCH, CGO_ENABLED and the GO<arch> keys
	Tags     []string          // build tags
	Sums     map[string]string // path@version to h1: sum, from go.sum

	// Stat reports whether path exists and whether it is a directory. nil
	// means the file system.
	Stat func(path string) (isDir, exists bool)
	// ReadFile reads a test main's generated source. nil means the file
	// system.
	ReadFile func(path string) ([]byte, error)
}

// osStat is Input.Stat over the file system.
func osStat(path string) (isDir, exists bool) {
	info, err := os.Stat(path)
	if err != nil {
		return false, false
	}
	return info.IsDir(), true
}

// LoadError lists everything that keeps a graph from being built.
type LoadError struct {
	Problems []string
	Tests    bool // the problems are in the -test pass
}

func newLoadError(problems []string) *LoadError {
	sort.Strings(problems)
	return &LoadError{Problems: slices.Compact(problems)}
}

func newTestLoadError(problems []string) *LoadError {
	e := newLoadError(problems)
	e.Tests = true
	return e
}

func (e *LoadError) Error() string {
	var b strings.Builder
	what := "packages"
	if e.Tests {
		what = "tests"
	}
	fmt.Fprintf(&b, "%d problem(s) loading %s:", len(e.Problems), what)
	hint := false
	for _, p := range e.Problems {
		b.WriteString("\n  " + p)
		hint = hint || strings.Contains(p, "go.sum")
	}
	if hint {
		b.WriteString("\nrun `go mod tidy` to update go.sum")
	}
	if e.Tests {
		b.WriteString("\nset doCheck = false to build without tests")
	}
	return b.String()
}

// problem describes p's load error.
func problem(p *golist.Package) string {
	msg := strings.TrimSpace(p.Error.Err)
	if p.Error.Pos != "" {
		msg = p.Error.Pos + ": " + msg
	}
	return p.ImportPath + ": " + msg
}

// Build constructs the graph. It reports every problem it finds, not just
// the first.
func Build(in Input) (*Graph, error) {
	var problems []string
	for i := range in.Packages {
		if p := &in.Packages[i]; p.Error != nil {
			problems = append(problems, problem(p))
		}
	}
	if len(problems) > 0 {
		return nil, newLoadError(problems)
	}

	stat := in.Stat
	if stat == nil {
		stat = osStat
	}
	byPath := make(map[string]*golist.Package, len(in.Packages))
	for i := range in.Packages {
		byPath[in.Packages[i].ImportPath] = &in.Packages[i]
	}

	g := &Graph{
		GoVersion:    strings.TrimPrefix(in.Env["GOVERSION"], "go"),
		GOOS:         in.Env["GOOS"],
		GOARCH:       in.Env["GOARCH"],
		CgoEnabled:   in.Env["CGO_ENABLED"] == "1",
		Modules:      map[string]*Module{},
		Packages:     map[string]*Package{},
		TestPackages: map[string]*Package{},
		Tests:        map[string]*Test{},
	}
	for i := range in.Packages {
		p := &in.Packages[i]
		if p.Standard {
			continue
		}
		pkg, mod, err := newPackage(p, in.Src, byPath, stat)
		if err != nil {
			problems = append(problems, err.Error())
			continue
		}
		g.Packages[pkg.ImportPath] = pkg
		g.addModule(mod, in.Sums)
		if pkg.Local && len(p.TestGoFiles)+len(p.XTestGoFiles) > 0 {
			g.Tested = append(g.Tested, pkg.ImportPath)
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
	sort.Strings(g.Tested)
	return g, nil
}

// addModule adds the module of a third-party package, once.
func (g *Graph) addModule(mod *Module, sums map[string]string) {
	if mod != nil && g.Modules[mod.Key] == nil {
		mod.Sum = sums[mod.Key]
		g.Modules[mod.Key] = mod
	}
}

// node returns the package node at key, in TestPackages or Packages.
func (g *Graph) node(key string) *Package {
	if p := g.TestPackages[key]; p != nil {
		return p
	}
	return g.Packages[key]
}

// cgoIn reports whether one of the packages at keys is a cgo package, and
// whether one has C++ files.
func (g *Graph) cgoIn(keys []string) (cgo, cxx bool) {
	for _, key := range keys {
		if c := g.node(key).Cgo; c != nil {
			cgo = true
			cxx = cxx || len(c.CXXFiles) > 0
		}
	}
	return cgo, cxx
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

	cgo, cxx := g.cgoIn(append([]string{p.ImportPath}, deps...))

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
		Cgo:     cgo,
		CXX:     cxx,
	}
}

// closure returns the non-standard packages root imports transitively,
// sorted, without root itself. It follows both package sets, so it also
// serves a test main, whose imports are test copies.
func (g *Graph) closure(root string) []string {
	seen := map[string]bool{root: true}
	queue := []string{root}
	var out []string
	for len(queue) > 0 {
		ip := queue[0]
		queue = queue[1:]
		for _, dep := range g.node(ip).Deps {
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
