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

// A vendored package that go list -test recompiles against the package
// under test builds from vendor/ like the package itself.
func TestTestCopyOfVendoredPackage(t *testing.T) {
	p := golist.Package{ImportPath: "github.com/google/go-cmp/cmp [example.com/vendored/p.test]", Name: "cmp",
		Dir: "/src/vendor/github.com/google/go-cmp/cmp", Module: cmpVendored, ForTest: "example.com/vendored/p",
		DepOnly: true, GoFiles: []string{"compare.go"}}
	pkg, _, err := newTestPackage(&p, "/src", nil, osStat)
	if err != nil {
		t.Fatal(err)
	}
	if pkg.ImportPath != "github.com/google/go-cmp/cmp" || !pkg.Local || pkg.VendorDir != "vendor" ||
		pkg.Subdir != "vendor/github.com/google/go-cmp/cmp" || pkg.TrimTo != "github.com/google/go-cmp@v0.6.0/cmp" ||
		pkg.SrcName != "gosrc-github.com-google-go-cmp-cmp" {
		t.Errorf("test copy = %+v", pkg)
	}
}
