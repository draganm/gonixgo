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

const testModinfo = "path\texample.com/app\nmod\texample.com/app\t(devel)\t\nbuild\t-trimpath=true\n"

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

	m := Manifest{
		Go: goBin, BinName: "app", Main: filepath.Join(mainOut, "pkg.a"),
		Importcfgs: []string{std, filepath.Join(mainOut, "importcfg"), filepath.Join(libOut, "importcfg")},
		Modinfo:    testModinfo,
		LDFlags:    []string{"-X 'main.version=1 2'"},
	}
	if err := Run(m, binOut, t.TempDir()); err != nil {
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
	if id := buildID(m); !bytes.Contains(data, []byte(id)) {
		t.Errorf("binary does not contain the build ID %s", id)
	}
}

func TestBuildID(t *testing.T) {
	base := Manifest{
		Go: "/go", BinName: "app", Main: "/main/pkg.a",
		Importcfgs: []string{"/std/importcfg"}, Modinfo: "path\texample.com/app\n",
		LDFlags: []string{"-s"},
	}
	same := base
	same.Importcfgs = append([]string(nil), base.Importcfgs...)
	if buildID(base) != buildID(same) {
		t.Error("buildID differs for equal manifests")
	}
	if got := len(buildID(base)); got != 40 {
		t.Errorf("len(buildID) = %d, want 40", got)
	}
	for name, change := range map[string]func(*Manifest){
		"BinName": func(m *Manifest) { m.BinName = "other" },
		"Main":    func(m *Manifest) { m.Main = "/other/pkg.a" },
		"LDFlags": func(m *Manifest) { m.LDFlags = []string{"-w"} },
		"Cgo":     func(m *Manifest) { m.Cgo = true },
		"CXX":     func(m *Manifest) { m.CXX = true },
		"Modinfo": func(m *Manifest) { m.Modinfo = "path\texample.com/other\n" },
	} {
		m := base
		change(&m)
		if buildID(m) == buildID(base) {
			t.Errorf("buildID does not change with %s", name)
		}
	}
}

func TestRunReportsLinkErrors(t *testing.T) {
	goBin := testutil.Go(t)
	err := Run(Manifest{Go: goBin, BinName: "app", Main: filepath.Join(t.TempDir(), "absent.a")}, t.TempDir(), t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "go tool link") {
		t.Fatalf("err = %v, want a link failure", err)
	}
}

func TestWithExtld(t *testing.T) {
	tests := []struct{ in, want []string }{
		{nil, []string{"-extld=clang"}},
		{[]string{"-s", "-w"}, []string{"-s", "-w", "-extld=clang"}},
		// The caller's choice of linker stands.
		{[]string{"-extld=gcc"}, []string{"-extld=gcc"}},
		{[]string{"-extld", "gcc", "-s"}, []string{"-extld", "gcc", "-s"}},
		// -extldflags is a different flag.
		{[]string{"-extldflags=-static"}, []string{"-extldflags=-static", "-extld=clang"}},
	}
	for _, tt := range tests {
		if got := withExtld(tt.in, "clang"); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("withExtld(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

var cgoFiles = map[string]string{
	"cadd/cadd.go": `package cadd

/*
#cgo CPPFLAGS: -I${SRCDIR}/include
#cgo CFLAGS: -DBONUS=0 -DGREETING="hello world"
#include <stdlib.h>
#include "add.h"
*/
import "C"

import "unsafe"

func Add(a, b int) int { return int(C.add(C.int(a), C.int(b))) }

//export goTwice
func goTwice(x C.int) C.int { return 2 * x }

func Twice(x int) int { return int(C.call_twice(C.int(x))) }

func Greeting() string { return C.GoString(C.greeting()) }

func Len(s string) int {
	cs := C.CString(s)
	defer C.free(unsafe.Pointer(cs))
	return int(C.cxx_len(cs))
}
`,
	"cadd/include/add.h": `#ifdef __cplusplus
extern "C" {
#endif
int add(int a, int b);
int call_twice(int x);
const char *greeting(void);
int cxx_len(const char *s);
#ifdef __cplusplus
}
#endif
`,
	"cadd/add.c": `#include "add.h"
#include "_cgo_export.h"

int add(int a, int b) { return a + b + BONUS; }
int call_twice(int x) { return goTwice(x); }
const char *greeting(void) { return GREETING; }
`,
	"cadd/len.cc": `#include <string>
#include "add.h"

int cxx_len(const char *s) { return static_cast<int>(std::string(s).size()); }
`,
	"main.go": `package main

import (
	"fmt"

	"example.com/app/cadd"
)

func main() { fmt.Println(cadd.Add(1, 2), cadd.Twice(4), cadd.Greeting(), cadd.Len("four")) }
`,
	"cmd/abs/main.go": `package main

// #include <stdlib.h>
import "C"

import "fmt"

func main() { fmt.Println(C.abs(-7)) }
`,
}

// A cgo package with C and C++ files, an exported Go function and a
// header directory, linked into a program by the C++ compiler.
func TestCgoCompileLinkRun(t *testing.T) {
	goBin := testutil.Go(t)
	std := testutil.CgoStdImportcfg(t, goBin)
	src := testutil.WriteTree(t, cgoFiles)

	caddOut, mainOut, binOut := t.TempDir(), t.TempDir(), t.TempDir()
	if err := compile.Run(compile.Manifest{
		Go: goBin, ImportPath: "example.com/app/cadd", SrcDir: filepath.Join(src, "cadd"),
		TrimTo: "example.com/app/cadd", Lang: "go1.21",
		Cgo: &compile.Cgo{
			PkgName: "cadd", CgoFiles: []string{"cadd.go"}, CFiles: []string{"add.c"}, CXXFiles: []string{"len.cc"},
			CPPFLAGS: []string{"-I${SRCDIR}/include"},
			CFLAGS:   []string{"-DBONUS=0", `-DGREETING="hello world"`},
		},
		Importcfgs: []string{std},
	}, caddOut, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if err := compile.Run(compile.Manifest{
		Go: goBin, ImportPath: "example.com/app", IsMain: true, SrcDir: src,
		TrimTo: "example.com/app", Lang: "go1.21", GoFiles: []string{"main.go"},
		Importcfgs: []string{std, filepath.Join(caddOut, "importcfg")},
	}, mainOut, t.TempDir()); err != nil {
		t.Fatal(err)
	}

	m := Manifest{
		Go: goBin, BinName: "app", Main: filepath.Join(mainOut, "pkg.a"),
		Importcfgs: []string{std, filepath.Join(mainOut, "importcfg"), filepath.Join(caddOut, "importcfg")},
		Modinfo:    testModinfo,
		Cgo:        true, CXX: true,
	}
	if err := Run(m, binOut, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(binOut, "bin", "app")
	out, err := exec.Command(bin).Output()
	if err != nil {
		t.Fatalf("running the linked binary: %v", err)
	}
	if got, want := string(out), "3 8 hello world 4\n"; got != want {
		t.Fatalf("binary printed %q, want %q", got, want)
	}
	data, err := os.ReadFile(bin)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte(src)) {
		t.Errorf("binary contains the source directory %s", src)
	}
}

// The main package may itself import "C".
func TestCgoMainPackage(t *testing.T) {
	goBin := testutil.Go(t)
	std := testutil.CgoStdImportcfg(t, goBin)
	src := testutil.WriteTree(t, cgoFiles)

	mainOut, binOut := t.TempDir(), t.TempDir()
	if err := compile.Run(compile.Manifest{
		Go: goBin, ImportPath: "example.com/app/cmd/abs", IsMain: true, SrcDir: filepath.Join(src, "cmd", "abs"),
		TrimTo: "example.com/app/cmd/abs", Lang: "go1.21",
		Cgo:        &compile.Cgo{PkgName: "main", CgoFiles: []string{"main.go"}},
		Importcfgs: []string{std},
	}, mainOut, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	m := Manifest{
		Go: goBin, BinName: "abs", Main: filepath.Join(mainOut, "pkg.a"),
		Importcfgs: []string{std, filepath.Join(mainOut, "importcfg")},
		Modinfo:    testModinfo,
		Cgo:        true,
	}
	if err := Run(m, binOut, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(filepath.Join(binOut, "bin", "abs")).Output()
	if err != nil {
		t.Fatalf("running the linked binary: %v", err)
	}
	if got := string(out); got != "7\n" {
		t.Fatalf("binary printed %q, want %q", got, "7\n")
	}
}

// Without the C toolchain's environment and linker the same link cannot
// work; this is what the cgo flag of the manifest turns on.
func TestCgoLinkNeedsTheFlag(t *testing.T) {
	goBin := testutil.Go(t)
	std := testutil.CgoStdImportcfg(t, goBin)
	src := testutil.WriteTree(t, cgoFiles)
	mainOut := t.TempDir()
	if err := compile.Run(compile.Manifest{
		Go: goBin, ImportPath: "example.com/app/cmd/abs", IsMain: true, SrcDir: filepath.Join(src, "cmd", "abs"),
		TrimTo: "example.com/app/cmd/abs", Lang: "go1.21",
		Cgo:        &compile.Cgo{PkgName: "main", CgoFiles: []string{"main.go"}},
		Importcfgs: []string{std},
	}, mainOut, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	// The minimal environment has the caller's PATH, so take the
	// compiler away through -extld instead.
	m := Manifest{
		Go: goBin, BinName: "abs", Main: filepath.Join(mainOut, "pkg.a"),
		Importcfgs: []string{std, filepath.Join(mainOut, "importcfg")},
		Modinfo:    testModinfo,
		Cgo:        true,
		LDFlags:    []string{"-extld=/nonexistent/cc"},
	}
	err := Run(m, t.TempDir(), t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "packageOverrides") {
		t.Fatalf("err = %v, want a failed link that says where libraries come from", err)
	}
}

// A test binary built as the test nodes build it: the package under test
// untrimmed, the test main that go list generated, and the link marking
// it as a test binary. Its test finds testdata through runtime.Caller.
func TestLinkTestBinary(t *testing.T) {
	goBin := testutil.Go(t)
	std := testutil.StdImportcfg(t, goBin)
	src := testutil.WriteTree(t, map[string]string{
		"go.mod": "module example.com/m\n\ngo 1.21\n",
		"p/p.go": "package p\n\nfunc One() int { return 1 }\n",
		"p/p_test.go": `package p

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestCaller(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	data, err := os.ReadFile(filepath.Join(filepath.Dir(file), "testdata", "in.txt"))
	if err != nil || string(data) != "in" {
		t.Fatalf("testdata beside %s: %q, %v", file, data, err)
	}
	if !testing.Testing() {
		t.Fatal("testing.Testing() is false")
	}
}
`,
		"p/testdata/in.txt": "in",
	})
	list := exec.Command(goBin, "list", "-test", "-f", `{{if eq .ImportPath "example.com/m/p.test"}}{{index .GoFiles 0}}{{end}}`, "./p")
	list.Dir = src
	list.Env = append(os.Environ(), "GOFLAGS=-mod=mod", "GOWORK=off")
	file, err := list.Output()
	if err != nil {
		t.Fatalf("go list -test: %v", err)
	}
	testMain, err := os.ReadFile(strings.TrimSpace(string(file)))
	if err != nil {
		t.Fatal(err)
	}

	pOut, mainOut, binOut := t.TempDir(), t.TempDir(), t.TempDir()
	if err := compile.Run(compile.Manifest{
		Go: goBin, ImportPath: "example.com/m/p", SrcDir: filepath.Join(src, "p"), Lang: "go1.21",
		GoFiles: []string{"p.go", "p_test.go"}, Importcfgs: []string{std},
	}, pOut, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if err := compile.Run(compile.Manifest{
		Go: goBin, ImportPath: "example.com/m/p.test", IsMain: true, Lang: "go1.21", TestMain: string(testMain),
		Importcfgs: []string{std, filepath.Join(pOut, "importcfg")},
	}, mainOut, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if err := Run(Manifest{
		Go: goBin, BinName: "p.test", Main: filepath.Join(mainOut, "pkg.a"),
		Importcfgs: []string{std, filepath.Join(mainOut, "importcfg"), filepath.Join(pOut, "importcfg")},
		Modinfo:    "path\texample.com/m/p.test\nmod\texample.com/m\t(devel)\t\n",
		Test:       true,
	}, binOut, t.TempDir()); err != nil {
		t.Fatal(err)
	}

	run := exec.Command(filepath.Join(binOut, "bin", "p.test"), "-test.v")
	run.Dir = t.TempDir()
	if out, err := run.CombinedOutput(); err != nil {
		t.Fatalf("the test binary failed: %v\n%s", err, out)
	}
}
