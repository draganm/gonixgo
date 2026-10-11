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
		Replace: &golist.Module{Path: "github.com/google/go-cmp", Version: "v0.7.0", Dir: "/mod/github.com/google/go-cmp@v0.7.0", GoVersion: "1.21"}}
	// forkMod is required at v1.0.0 and replaced by a fork under another
	// path.
	forkMod = &golist.Module{Path: "github.com/a/b", Version: "v1.0.0", Dir: "/mod/github.com/fork/b@v1.0.1", GoVersion: "1.20",
		Replace: &golist.Module{Path: "github.com/fork/b", Version: "v1.0.1", Dir: "/mod/github.com/fork/b@v1.0.1", GoVersion: "1.20"}}

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
