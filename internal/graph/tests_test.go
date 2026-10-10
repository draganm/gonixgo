package graph

import (
	"errors"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/draganm/gonixgo/internal/golist"
	"github.com/draganm/gonixgo/internal/modinfo"
)

var cmpMod = &golist.Module{Path: "github.com/google/go-cmp", Version: "v0.7.0", Dir: "/mod/github.com/google/go-cmp@v0.7.0", GoVersion: "1.21"}

const (
	pTest  = "example.com/app/p [example.com/app/p.test]"
	qTest  = "example.com/app/q [example.com/app/p.test]"
	pxTest = "example.com/app/p_test [example.com/app/p.test]"
)

// testedFirstPass is go list -deps output for example.com/app, which
// imports q and x; q imports p. app, p and x have tests, and so does the
// third-party color, which is never tested.
func testedFirstPass() []golist.Package {
	return []golist.Package{
		std("fmt"),
		{ImportPath: "example.com/app/p", Name: "p", Dir: "/src/p", Module: mainMod, DepOnly: true,
			GoFiles: []string{"p.go"}, TestGoFiles: []string{"p_test.go"}, XTestGoFiles: []string{"x_test.go"},
			TestEmbedPatterns: []string{"testdata/in.txt"}, XTestEmbedPatterns: []string{"testdata/x.txt"}},
		{ImportPath: "example.com/app/q", Name: "q", Dir: "/src/q", Module: mainMod, DepOnly: true,
			GoFiles: []string{"q.go"}, Imports: []string{"example.com/app/p"}},
		{ImportPath: "example.com/app/x", Name: "x", Dir: "/src/x", Module: mainMod, DepOnly: true,
			GoFiles: []string{"x.go"}, XTestGoFiles: []string{"x_test.go"}},
		{ImportPath: "github.com/fatih/color", Name: "color", Dir: colorMod.Dir, Module: colorMod, DepOnly: true,
			GoFiles: []string{"color.go"}, TestGoFiles: []string{"color_test.go"}},
		{ImportPath: "example.com/app", Name: "main", Dir: "/src", Module: mainMod, DefaultGODEBUG: "a=1",
			GoFiles: []string{"main.go"}, TestGoFiles: []string{"main_test.go"},
			Imports: []string{"example.com/app/q", "example.com/app/x", "fmt", "github.com/fatih/color"}},
	}
}

// testedSecondPass is go list -deps -test output for the tested packages
// of testedFirstPass: p's copies and the chain p ← q ← p_test; x with
// external tests only; app, a main package; go-cmp and a local helper that
// only tests import; and a third-party package recompiled against p.
func testedSecondPass() []golist.Package {
	return append(testedFirstPass(),
		std("os"), std("testing"),
		golist.Package{ImportPath: "github.com/google/go-cmp/cmp", Name: "cmp", Dir: cmpMod.Dir + "/cmp", Module: cmpMod, DepOnly: true,
			GoFiles: []string{"compare.go"}},
		golist.Package{ImportPath: "example.com/app/internal/helper", Name: "helper", Dir: "/src/internal/helper", Module: mainMod, DepOnly: true,
			GoFiles: []string{"helper.go"}, TestGoFiles: []string{"helper_test.go"}},
		golist.Package{ImportPath: pTest, Name: "p", Dir: "/src/p", Module: mainMod, ForTest: "example.com/app/p",
			GoFiles: []string{"p.go", "p_test.go"}, TestEmbedPatterns: []string{"testdata/in.txt"},
			EmbedFiles: []string{"testdata/in.txt"}, Imports: []string{"testing"}},
		golist.Package{ImportPath: qTest, Name: "q", Dir: "/src/q", Module: mainMod, ForTest: "example.com/app/p", DepOnly: true,
			GoFiles: []string{"q.go"}, Imports: []string{pTest}, ImportMap: map[string]string{"example.com/app/p": pTest}},
		golist.Package{ImportPath: "github.com/fatih/color/hook [example.com/app/p.test]", Name: "hook", Dir: colorMod.Dir + "/hook",
			Module: colorMod, ForTest: "example.com/app/p", DepOnly: true,
			GoFiles: []string{"hook.go"}, Imports: []string{pTest}, ImportMap: map[string]string{"example.com/app/p": pTest}},
		golist.Package{ImportPath: pxTest, Name: "p_test", Dir: "/src/p", Module: mainMod, ForTest: "example.com/app/p",
			GoFiles: []string{"x_test.go"}, EmbedFiles: []string{"testdata/x.txt"},
			Imports:   []string{"example.com/app/internal/helper", pTest, qTest, "github.com/google/go-cmp/cmp", "testing"},
			ImportMap: map[string]string{"example.com/app/p": pTest, "example.com/app/q": qTest}},
		golist.Package{ImportPath: "example.com/app/p.test", Name: "main", Dir: "/src/p", Module: mainMod, DefaultGODEBUG: "a=1",
			GoFiles: []string{"/cache/p-d"}, Imports: []string{pTest, pxTest, "os", "testing"}},
		golist.Package{ImportPath: "example.com/app/x_test [example.com/app/x.test]", Name: "x_test", Dir: "/src/x", Module: mainMod,
			ForTest: "example.com/app/x", GoFiles: []string{"x_test.go"}, Imports: []string{"example.com/app/x", "testing"}},
		golist.Package{ImportPath: "example.com/app/x.test", Name: "main", Dir: "/src/x", Module: mainMod, DefaultGODEBUG: "a=1",
			GoFiles: []string{"/cache/x-d"}, Imports: []string{"example.com/app/x_test [example.com/app/x.test]", "os", "testing"}},
		golist.Package{ImportPath: "example.com/app [example.com/app.test]", Name: "main", Dir: "/src", Module: mainMod,
			ForTest: "example.com/app", GoFiles: []string{"main.go", "main_test.go"},
			Imports: []string{"example.com/app/q", "example.com/app/x", "fmt", "github.com/fatih/color", "testing"}},
		golist.Package{ImportPath: "example.com/app.test", Name: "main", Dir: "/src", Module: mainMod, DefaultGODEBUG: "a=1",
			GoFiles: []string{"/cache/app-d"}, Imports: []string{"example.com/app [example.com/app.test]", "os", "testing"}},
	)
}

var testMains = map[string]string{"/cache/p-d": "p main", "/cache/x-d": "x main", "/cache/app-d": "app main"}

// testedGraph builds both passes, with the second pass edited by edit.
func testedGraph(t *testing.T, edit func([]golist.Package)) (*Graph, error) {
	t.Helper()
	g := build(t, testedFirstPass())
	second := testedSecondPass()
	if edit != nil {
		edit(second)
	}
	allSums := maps.Clone(sums)
	allSums["github.com/google/go-cmp@v0.7.0"] = "h1:cmp"
	err := g.AddTests(Input{
		Packages: second, Src: "/src", Env: testEnv, Sums: allSums,
		Stat: func(path string) (bool, bool) { return path == "/src/p/testdata", path == "/src/p/testdata" },
		ReadFile: func(path string) ([]byte, error) {
			if src, ok := testMains[path]; ok {
				return []byte(src), nil
			}
			return nil, errors.New("no such file")
		},
	})
	return g, err
}

func TestBuildTested(t *testing.T) {
	g := build(t, testedFirstPass())
	want := []string{"example.com/app", "example.com/app/p", "example.com/app/x"}
	if !reflect.DeepEqual(g.Tested, want) {
		t.Fatalf("Tested = %v, want %v", g.Tested, want)
	}
	if len(g.TestPackages) != 0 || len(g.Tests) != 0 {
		t.Fatalf("Build added tests: %v %v", g.TestPackages, g.Tests)
	}
}

func TestAddTestsPackages(t *testing.T) {
	g, err := testedGraph(t, nil)
	if err != nil {
		t.Fatal(err)
	}
	wantKeys := []string{
		"example.com/app [example.com/app.test]", "example.com/app.test",
		pTest, "example.com/app/p.test", pxTest, qTest,
		"example.com/app/x.test", "example.com/app/x_test [example.com/app/x.test]",
		"github.com/fatih/color/hook [example.com/app/p.test]",
	}
	if got := slices.Sorted(maps.Keys(g.TestPackages)); !reflect.DeepEqual(got, wantKeys) {
		t.Fatalf("TestPackages = %q\nwant %q", got, wantKeys)
	}

	wantP := &Package{
		ImportPath: "example.com/app/p", Name: "gotestpkg-example.com-app-p--example.com-app-p.test-",
		Local: true, ModulePath: "example.com/app", Subdir: "p", TestSrc: "example.com/app/p", Lang: "go1.24",
		GoFiles: []string{"p.go", "p_test.go"}, Embed: map[string][]string{"testdata/in.txt": {"testdata/in.txt"}},
	}
	if got := g.TestPackages[pTest]; !reflect.DeepEqual(got, wantP) {
		t.Errorf("p [p.test] =\n%+v\nwant\n%+v", got, wantP)
	}
	wantQ := &Package{
		ImportPath: "example.com/app/q", Name: "gotestpkg-example.com-app-q--example.com-app-p.test-",
		SrcName: "gosrc-example.com-app-q", Local: true, ModulePath: "example.com/app", Subdir: "q",
		TrimTo: "example.com/app/q", Lang: "go1.24", GoFiles: []string{"q.go"}, SrcFiles: []string{"q/q.go"},
		Deps: []string{pTest},
	}
	if got := g.TestPackages[qTest]; !reflect.DeepEqual(got, wantQ) {
		t.Errorf("q [p.test] =\n%+v\nwant\n%+v", got, wantQ)
	}
	wantPX := &Package{
		ImportPath: "example.com/app/p_test", Name: "gotestpkg-example.com-app-p_test--example.com-app-p.test-",
		Local: true, ModulePath: "example.com/app", Subdir: "p", TestSrc: "example.com/app/p", Lang: "go1.24",
		GoFiles: []string{"x_test.go"}, Embed: map[string][]string{"testdata/x.txt": {"testdata/x.txt"}},
		Deps: []string{"example.com/app/internal/helper", pTest, qTest, "github.com/google/go-cmp/cmp"},
	}
	if got := g.TestPackages[pxTest]; !reflect.DeepEqual(got, wantPX) {
		t.Errorf("p_test [p.test] =\n%+v\nwant\n%+v", got, wantPX)
	}
	wantMain := &Package{
		ImportPath: "example.com/app/p.test", Name: "gotestpkg-example.com-app-p.test",
		Local: true, IsMain: true, ModulePath: "example.com/app", Lang: "go1.24",
		TestMain: "p main", Deps: []string{pTest, pxTest},
	}
	if got := g.TestPackages["example.com/app/p.test"]; !reflect.DeepEqual(got, wantMain) {
		t.Errorf("p.test =\n%+v\nwant\n%+v", got, wantMain)
	}

	// A main package's copy is compiled as a library, -p example.com/app.
	if app := g.TestPackages["example.com/app [example.com/app.test]"]; app.IsMain || app.ImportPath != "example.com/app" || app.Subdir != "" {
		t.Errorf("app [app.test] = %+v", app)
	}
	// A third-party package recompiled against p keeps its module's trimTo.
	hook := g.TestPackages["github.com/fatih/color/hook [example.com/app/p.test]"]
	if hook.ImportPath != "github.com/fatih/color/hook" || hook.TrimTo != "github.com/fatih/color@v1.18.0/hook" ||
		hook.ModuleKey != "github.com/fatih/color@v1.18.0" || hook.TestSrc != "" {
		t.Errorf("hook [p.test] = %+v", hook)
	}

	// What only tests import is an ordinary node.
	if helper := g.Packages["example.com/app/internal/helper"]; helper == nil || helper.Name != "golocal-example.com-app-internal-helper" {
		t.Errorf("helper = %+v", helper)
	}
	if cmp := g.Modules["github.com/google/go-cmp@v0.7.0"]; cmp == nil || cmp.Sum != "h1:cmp" {
		t.Errorf("go-cmp module = %+v", cmp)
	}
}

func TestAddTestsTests(t *testing.T) {
	g, err := testedGraph(t, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := slices.Sorted(maps.Keys(g.Tests)); !reflect.DeepEqual(got, g.Tested) {
		t.Fatalf("Tests = %v, want %v", got, g.Tested)
	}

	p := g.Tests["example.com/app/p"]
	wantP := &Test{
		ImportPath: "example.com/app/p", Name: "gotest-example.com-app-p", SrcName: "gosrc-test-example.com-app-p",
		ModulePath: "example.com/app", Subdir: "p",
		SrcFiles: []string{"p/p.go", "p/p_test.go", "p/testdata/in.txt", "p/testdata/x.txt", "p/x_test.go"},
		SrcTrees: []string{"p/testdata"},
		Bin: &Binary{
			Name: "p.test", DrvName: "gotestbin-example.com-app-p", Main: "example.com/app/p.test",
			Deps: []string{"example.com/app/internal/helper", pTest, pxTest, qTest, "github.com/google/go-cmp/cmp"},
			Modinfo: modinfo.Info{
				Path:     "example.com/app/p.test",
				Main:     modinfo.Module{Path: "example.com/app", Version: "(devel)"},
				Settings: modinfo.Settings(testEnv, nil, "a=1"),
			}.String(),
			Godebug: "a=1", Test: true,
		},
	}
	if !reflect.DeepEqual(p, wantP) {
		t.Errorf("test of p =\n%+v\nwant\n%+v", p, wantP)
		t.Errorf("bin =\n%+v\nwant\n%+v", p.Bin, wantP.Bin)
	}

	// External tests only: the test imports x itself.
	if x := g.Tests["example.com/app/x"].Bin; !reflect.DeepEqual(x.Deps, []string{"example.com/app/x", "example.com/app/x_test [example.com/app/x.test]"}) {
		t.Errorf("x.test deps = %v", x.Deps)
	}

	// The module root: the program's own module info, and no testdata.
	app := g.Tests["example.com/app"]
	if app.Subdir != "" || app.SrcTrees != nil || !reflect.DeepEqual(app.SrcFiles, []string{"main.go", "main_test.go"}) {
		t.Errorf("test of app = %+v", app)
	}
	if app.Bin.Name != "app.test" || app.Bin.Modinfo != g.Bins[0].Modinfo {
		t.Errorf("app.test = %+v, want the program's module info", app.Bin)
	}
}

// A //go:debug line in a test file gives the test main its own
// DefaultGODEBUG; go test -c then describes the test binary as P.test.
func TestAddTestsMainPackageWithOwnGodebug(t *testing.T) {
	g, err := testedGraph(t, func(pkgs []golist.Package) {
		for i := range pkgs {
			if pkgs[i].ImportPath == "example.com/app.test" {
				pkgs[i].DefaultGODEBUG = "a=1,b=2"
			}
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	bin := g.Tests["example.com/app"].Bin
	want := modinfo.Info{
		Path:     "example.com/app.test",
		Main:     modinfo.Module{Path: "example.com/app", Version: "(devel)"},
		Settings: modinfo.Settings(testEnv, nil, "a=1,b=2"),
	}.String()
	if bin.Modinfo != want || bin.Godebug != "a=1,b=2" {
		t.Errorf("app.test modinfo =\n%s\nwant\n%s", bin.Modinfo, want)
	}
}

func TestAddTestsReportsProblems(t *testing.T) {
	_, err := testedGraph(t, func(pkgs []golist.Package) {
		for i := range pkgs {
			if pkgs[i].ImportPath == pxTest {
				pkgs[i].Error = &golist.PackageError{Err: "missing go.sum entry for module providing package github.com/google/go-cmp/cmp"}
			}
		}
	})
	var loadErr *LoadError
	if !errors.As(err, &loadErr) || !loadErr.Tests {
		t.Fatalf("err = %v, want a *LoadError for tests", err)
	}
	for _, want := range []string{"1 problem(s) loading tests:", "missing go.sum entry", "go mod tidy", "doCheck = false"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q:\n%v", want, err)
		}
	}
}
