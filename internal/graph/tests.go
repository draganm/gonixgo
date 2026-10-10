package graph

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/draganm/gonixgo/internal/golist"
	"github.com/draganm/gonixgo/internal/modinfo"
	"github.com/draganm/gonixgo/internal/storepath"
)

// Test is how one package's tests are built and run: the tree its test
// copies compile from and its test runs in, its test binary, and the run.
type Test struct {
	ImportPath string   // the tested package
	Name       string   // derivation name of the run
	SrcName    string   // store name of the test source
	ModulePath string   //
	Subdir     string   // the package's directory relative to the source root, "" for the root
	SrcFiles   []string // files of the test source, relative to the source root
	SrcTrees   []string // directories of the test source, copied whole
	Bin        *Binary  // the test binary
}

// AddTests adds the tests of g.Tested. in.Packages is the output of
// go list -e -deps -test for them; the rest of in is as for Build.
// Packages the first pass printed keep their nodes. It reports every
// problem it finds, not just the first.
func (g *Graph) AddTests(in Input) error {
	var problems []string
	for i := range in.Packages {
		if p := &in.Packages[i]; p.Error != nil {
			problems = append(problems, problem(p))
		}
	}
	if len(problems) > 0 {
		return newTestLoadError(problems)
	}

	stat, readFile := in.Stat, in.ReadFile
	if stat == nil {
		stat = osStat
	}
	if readFile == nil {
		readFile = os.ReadFile
	}
	byPath := make(map[string]*golist.Package, len(in.Packages))
	for i := range in.Packages {
		byPath[in.Packages[i].ImportPath] = &in.Packages[i]
	}
	tested := map[string]bool{}
	for _, ip := range g.Tested {
		tested[ip] = true
	}

	for i := range in.Packages {
		p := &in.Packages[i]
		switch {
		case p.Standard:
		case p.ForTest != "":
			pkg, mod, err := newTestPackage(p, in.Src, byPath, stat)
			if err != nil {
				problems = append(problems, err.Error())
				continue
			}
			g.TestPackages[p.ImportPath] = pkg
			g.addModule(mod, in.Sums)
		case p.Name == "main" && strings.HasSuffix(p.ImportPath, ".test") && tested[strings.TrimSuffix(p.ImportPath, ".test")]:
			pkg, err := newTestMain(p, byPath, readFile)
			if err != nil {
				problems = append(problems, err.Error())
				continue
			}
			g.TestPackages[p.ImportPath] = pkg
		case g.Packages[p.ImportPath] == nil:
			// Only tests import it; it is built like any other package.
			pkg, mod, err := newPackage(p, in.Src, byPath, stat)
			if err != nil {
				problems = append(problems, err.Error())
				continue
			}
			g.Packages[pkg.ImportPath] = pkg
			g.addModule(mod, in.Sums)
		}
	}
	if len(problems) > 0 {
		return newTestLoadError(problems)
	}

	for _, ip := range g.Tested {
		t, err := g.newTest(ip, byPath, in, stat)
		if err != nil {
			problems = append(problems, err.Error())
			continue
		}
		g.Tests[ip] = t
	}
	if len(problems) > 0 {
		return newTestLoadError(problems)
	}
	return nil
}

// newTestPackage turns "X [P.test]", a package go list -test copied for
// P's test binary, into a node. X is P or P_test, which compile from P's
// test source and keep their source paths, or a package between them,
// recompiled against them and otherwise built as X is.
func newTestPackage(p *golist.Package, src string, byPath map[string]*golist.Package, stat func(string) (bool, bool)) (*Package, *Module, error) {
	pkg, mod, err := newPackage(p, src, byPath, stat)
	if err != nil {
		return nil, nil, err
	}
	base, _, _ := strings.Cut(p.ImportPath, " ")
	pkg.ImportPath = base
	pkg.Name = storepath.SanitizeName("gotestpkg-" + p.ImportPath)
	// Only the test main is compiled as main; go test compiles a main
	// package under test with -p and its import path.
	pkg.IsMain = false
	switch base {
	case p.ForTest, p.ForTest + "_test":
		patterns := slices.Concat(p.EmbedPatterns, p.TestEmbedPatterns)
		if base != p.ForTest {
			// go list reports the external test's patterns on P.
			patterns = nil
			if under := byPath[p.ForTest]; under != nil {
				patterns = under.XTestEmbedPatterns
			}
		}
		pkg.Embed = embedMap(patterns, p.EmbedFiles)
		pkg.TrimTo, pkg.TestSrc = "", p.ForTest
		pkg.SrcName, pkg.SrcFiles, pkg.SrcTrees = "", nil, nil
	default:
		if pkg.Local {
			pkg.SrcName = storepath.SanitizeName("gosrc-" + base)
			pkg.TrimTo = base
		} else {
			pkg.TrimTo = pkg.ModuleKey + strings.TrimPrefix(base, pkg.ModulePath)
		}
	}
	return pkg, mod, nil
}

// newTestMain turns P.test, the test main go list generated, into a node
// that carries the generated source.
func newTestMain(p *golist.Package, byPath map[string]*golist.Package, readFile func(string) ([]byte, error)) (*Package, error) {
	if len(p.GoFiles) != 1 || p.Module == nil {
		return nil, fmt.Errorf("%s: want one generated file in a module, go list reported %v", p.ImportPath, p.GoFiles)
	}
	file := p.GoFiles[0]
	if !filepath.IsAbs(file) {
		file = filepath.Join(p.Dir, file)
	}
	src, err := readFile(file)
	if err != nil {
		return nil, fmt.Errorf("%s: reading the generated test main: %w", p.ImportPath, err)
	}
	deps, err := deps(p, byPath)
	if err != nil {
		return nil, err
	}
	return &Package{
		ImportPath: p.ImportPath,
		Name:       storepath.SanitizeName("gotestpkg-" + p.ImportPath),
		Local:      true,
		IsMain:     true,
		ModulePath: p.Module.Path,
		Lang:       lang(p.Module.GoVersion),
		TestMain:   string(src),
		Deps:       deps,
	}, nil
}

// newTest describes the test source, the test binary and the run of the
// tested package ip.
func (g *Graph) newTest(ip string, byPath map[string]*golist.Package, in Input, stat func(string) (bool, bool)) (*Test, error) {
	pkg, p, main := g.Packages[ip], byPath[ip], byPath[ip+".test"]
	if pkg == nil || p == nil || main == nil || g.TestPackages[ip+".test"] == nil {
		return nil, fmt.Errorf("%s: go list -test reported no test main for it", ip)
	}

	// What the package compiles from, its test files, what they embed,
	// and testdata/.
	files := slices.Clone(pkg.SrcFiles)
	for _, f := range slices.Concat(p.TestGoFiles, p.XTestGoFiles) {
		files = append(files, path.Join(pkg.Subdir, f))
	}
	for _, copied := range []string{ip, ip + "_test"} {
		if c := byPath[copied+" ["+ip+".test]"]; c != nil {
			for _, f := range c.EmbedFiles {
				files = append(files, path.Join(pkg.Subdir, f))
			}
		}
	}
	sort.Strings(files)
	trees := slices.Clone(pkg.SrcTrees)
	testdata := path.Join(pkg.Subdir, "testdata")
	if isDir, _ := stat(filepath.Join(in.Src, filepath.FromSlash(testdata))); isDir {
		trees = append(trees, testdata)
	}
	sort.Strings(trees)

	return &Test{
		ImportPath: ip,
		Name:       storepath.SanitizeName("gotest-" + ip),
		SrcName:    storepath.SanitizeName("gosrc-test-" + ip),
		ModulePath: pkg.ModulePath,
		Subdir:     pkg.Subdir,
		SrcFiles:   slices.Compact(files),
		SrcTrees:   slices.Compact(trees),
		Bin:        g.newTestBinary(ip, main, in),
	}, nil
}

// newTestBinary describes the link of ip's test binary, main being its
// test main as go list printed it.
func (g *Graph) newTestBinary(ip string, main *golist.Package, in Input) *Binary {
	key := ip + ".test"
	deps := g.closure(key)
	cgo, cxx := g.cgoIn(append([]string{key}, deps...))
	name := execName(ip) + ".test"
	if g.GOOS == "windows" {
		name += ".exe"
	}

	// go test -c sets the module info before it attaches the test main's
	// imports, so it lists no dependencies. A program under test lends
	// the test binary its own, unless a //go:debug line in a test file
	// gives the test main a different DefaultGODEBUG.
	info := modinfo.Info{
		Path:     key,
		Main:     modinfo.Module{Path: main.Module.Path, Version: "(devel)"},
		Settings: modinfo.Settings(in.Env, in.Tags, main.DefaultGODEBUG),
	}.String()
	for _, prog := range g.Bins {
		if prog.Main == ip && prog.Godebug == main.DefaultGODEBUG {
			info = prog.Modinfo
		}
	}
	return &Binary{
		Name:    name,
		DrvName: storepath.SanitizeName("gotestbin-" + ip),
		Main:    key,
		Deps:    deps,
		Modinfo: info,
		Godebug: main.DefaultGODEBUG,
		Cgo:     cgo,
		CXX:     cxx,
		Test:    true,
	}
}
