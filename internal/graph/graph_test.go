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
		{"swig", golist.Package{ImportPath: "example.com/app/c", Name: "c", Dir: "/src/c", Module: mainMod, DepOnly: true, GoFiles: []string{"c.go"}, SwigFiles: []string{"x.swig"}}, "example.com/app/c: SWIG files are not supported yet"},
		{"swig hint", golist.Package{ImportPath: "example.com/app/c", Name: "c", Dir: "/src/c", Module: mainMod, DepOnly: true, GoFiles: []string{"c.go"}, SwigCXXFiles: []string{"x.swigcxx"}}, "if the package also builds without cgo, set CGO_ENABLED = 0 in buildGoApplication"},
		{"fortran", golist.Package{ImportPath: "example.com/app/c", Name: "c", Dir: "/src/c", Module: mainMod, DepOnly: true, GoFiles: []string{"c.go"}, CgoFiles: []string{"cgo.go"}, FFiles: []string{"x.f90"}}, "example.com/app/c: Fortran files are not supported yet"},
		{"syso", golist.Package{ImportPath: "example.com/app/c", Name: "c", Dir: "/src/c", Module: mainMod, DepOnly: true, GoFiles: []string{"c.go"}, SysoFiles: []string{"x.syso"}}, "example.com/app/c: .syso files are not supported yet"},
		{"replace outside src", golist.Package{ImportPath: "github.com/x/y", Name: "y", Dir: "/elsewhere", DepOnly: true, GoFiles: []string{"y.go"},
			Module: &golist.Module{Path: "github.com/x/y", Version: "v1.0.0", Replace: &golist.Module{Path: "../y", Dir: "/elsewhere"}}}, "replace github.com/x/y => ../y: /elsewhere is outside src /src"},
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

// caddPkg is a local cgo package with every kind of file gonixgo builds,
// as go list reports it: ${SRCDIR} already expanded to Dir.
var caddPkg = golist.Package{
	ImportPath: "example.com/app/internal/cadd", Name: "cadd", Dir: "/src/internal/cadd", Module: mainMod, DepOnly: true,
	GoFiles: []string{"plain.go"}, CgoFiles: []string{"cadd.go"}, CFiles: []string{"add.c"},
	CXXFiles: []string{"len.cc"}, MFiles: []string{"objc.m"}, HFiles: []string{"local.h"}, SFiles: []string{"seven.S"},
	CgoCFLAGS:   []string{"-DBONUS=0", "-I/src/internal/cadd/include", `-DGREETING="hello world"`},
	CgoCPPFLAGS: []string{"-I/src/internal/cadd/../shared"},
	CgoLDFLAGS: []string{
		"-L/src/internal/cadd/lib", "-Wl,-rpath,/src/internal/cadd/lib,-z,now",
		"/src/internal/cadd/libfoo.a", "-L/src/internal/cadd/absent",
	},
	CgoPkgConfig: []string{"libzstd"},
	Imports:      []string{"C", "unsafe"},
}

// caddStat is the file system around caddPkg.
func caddStat(path string) (isDir, exists bool) {
	switch path {
	case "/src/internal/cadd/include", "/src/internal/shared", "/src/internal/cadd/lib":
		return true, true
	case "/src/internal/cadd/libfoo.a":
		return false, true
	}
	return false, false
}

// cgoPackages is a program whose main package imports cadd.
func cgoPackages(cadd golist.Package) []golist.Package {
	return []golist.Package{
		std("unsafe"),
		std("fmt"),
		cadd,
		{ImportPath: "example.com/app", Name: "main", Dir: "/src", Module: mainMod,
			GoFiles: []string{"main.go"}, Imports: []string{cadd.ImportPath, "fmt"}},
	}
}

func TestBuildCgoPackage(t *testing.T) {
	g, err := Build(Input{Packages: cgoPackages(caddPkg), Src: "/src", Env: testEnv, Stat: caddStat})
	if err != nil {
		t.Fatal(err)
	}
	want := &Package{
		ImportPath: "example.com/app/internal/cadd", Name: "golocal-example.com-app-internal-cadd",
		SrcName: "gosrc-example.com-app-internal-cadd",
		Local:   true, ModulePath: "example.com/app", Subdir: "internal/cadd", TrimTo: "example.com/app/internal/cadd", Lang: "go1.24",
		GoFiles: []string{"plain.go"}, SFiles: []string{"seven.S"},
		SrcFiles: []string{
			"internal/cadd/add.c", "internal/cadd/cadd.go", "internal/cadd/len.cc", "internal/cadd/libfoo.a",
			"internal/cadd/local.h", "internal/cadd/objc.m", "internal/cadd/plain.go", "internal/cadd/seven.S",
		},
		SrcTrees: []string{"internal/cadd/include", "internal/cadd/lib", "internal/shared"},
		Cgo: &Cgo{
			PkgName:  "cadd",
			CgoFiles: []string{"cadd.go"}, CFiles: []string{"add.c"}, CXXFiles: []string{"len.cc"}, MFiles: []string{"objc.m"},
			CPPFLAGS: []string{"-I${SRCDIR}/../shared"},
			CFLAGS:   []string{"-DBONUS=0", "-I${SRCDIR}/include", `-DGREETING="hello world"`},
			LDFLAGS: []string{
				"-L${SRCDIR}/lib", "-Wl,-rpath,${SRCDIR}/lib,-z,now", "${SRCDIR}/libfoo.a", "-L${SRCDIR}/absent",
			},
			PkgConfig: []string{"libzstd"},
		},
	}
	if got := g.Packages["example.com/app/internal/cadd"]; !reflect.DeepEqual(got, want) {
		t.Errorf("cadd package =\n%+v\ncgo %+v\nwant\n%+v\ncgo %+v", got, got.Cgo, want, want.Cgo)
	}
}

func TestBuildCgoBinary(t *testing.T) {
	g, err := Build(Input{Packages: cgoPackages(caddPkg), Src: "/src", Env: testEnv, Stat: caddStat})
	if err != nil {
		t.Fatal(err)
	}
	if bin := g.Bins[0]; !bin.Cgo || !bin.CXX {
		t.Errorf("binary with a cgo and C++ package: Cgo = %v, CXX = %v, want both", bin.Cgo, bin.CXX)
	}

	noCXX := caddPkg
	noCXX.CXXFiles = nil
	g, err = Build(Input{Packages: cgoPackages(noCXX), Src: "/src", Env: testEnv, Stat: caddStat})
	if err != nil {
		t.Fatal(err)
	}
	if bin := g.Bins[0]; !bin.Cgo || bin.CXX {
		t.Errorf("binary with a cgo package without C++: Cgo = %v, CXX = %v", bin.Cgo, bin.CXX)
	}

	if bin := build(t, appPackages()).Bins[0]; bin.Cgo || bin.CXX {
		t.Errorf("pure binary: Cgo = %v, CXX = %v, want neither", bin.Cgo, bin.CXX)
	}
}

func TestBuildCgoThirdParty(t *testing.T) {
	cg := golist.Package{
		ImportPath: "golang.org/x/sys/cg", Name: "cg", Dir: sysMod.Dir + "/cg", Module: sysMod, DepOnly: true,
		CgoFiles: []string{"cg.go"}, CgoCFLAGS: []string{"-I" + sysMod.Dir + "/cg/inc"}, Imports: []string{"C"},
	}
	stat := func(path string) (bool, bool) {
		t.Errorf("stat(%s) called for a third-party package", path)
		return false, false
	}
	g, err := Build(Input{Packages: cgoPackages(cg), Src: "/src", Env: testEnv, Stat: stat})
	if err != nil {
		t.Fatal(err)
	}
	got := g.Packages["golang.org/x/sys/cg"]
	if got.Cgo == nil || !reflect.DeepEqual(got.Cgo.CFLAGS, []string{"-I${SRCDIR}/inc"}) {
		t.Errorf("cgo = %+v, want the module cache directory restored to ${SRCDIR}", got.Cgo)
	}
	if got.SrcFiles != nil || got.SrcTrees != nil {
		t.Errorf("third-party package has SrcFiles %v and SrcTrees %v, want none", got.SrcFiles, got.SrcTrees)
	}
}

func TestBuildCgoRootTree(t *testing.T) {
	stat := func(path string) (bool, bool) { return path == "/src", path == "/src" }
	g, err := Build(Input{Src: "/src", Env: testEnv, Stat: stat, Packages: []golist.Package{
		{ImportPath: "example.com/app", Name: "main", Dir: "/src", Module: mainMod,
			CgoFiles: []string{"main.go"}, CgoCFLAGS: []string{"-I/src"}, Imports: []string{"C"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	got := g.Packages["example.com/app"]
	if !reflect.DeepEqual(got.SrcTrees, []string{"."}) || got.Cgo.PkgName != "main" ||
		!reflect.DeepEqual(got.Cgo.CFLAGS, []string{"-I${SRCDIR}"}) {
		t.Errorf("package = %+v, cgo = %+v", got, got.Cgo)
	}
	if !g.Bins[0].Cgo {
		t.Error("a cgo main package does not make its binary a cgo binary")
	}
}

func TestBuildCgoPathOutsideSrc(t *testing.T) {
	outside := caddPkg
	outside.CgoCFLAGS = []string{"-I/src/internal/cadd/../../../elsewhere"}
	_, err := Build(Input{Packages: cgoPackages(outside), Src: "/src", Env: testEnv, Stat: caddStat})
	want := "example.com/app/internal/cadd: a #cgo directive names /elsewhere, which is outside src"
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("err = %v, want it to contain %q", err, want)
	}
}
