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

func TestRunIgnoresCallerBuildConfiguration(t *testing.T) {
	a := Args{Src: testutil.WriteTree(t, appFiles), ModRoot: ".", SubPackages: []string{"."}}
	clean, err := run(t, a)
	if err != nil {
		t.Fatal(err)
	}
	testutil.HostileGoEnv(t)
	hostile, err := run(t, a)
	if err != nil {
		t.Fatal(err)
	}
	if hostile != clean {
		t.Fatalf("output in a hostile environment:\n%s\nin a clean one:\n%s", hostile, clean)
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

var cgoAppFiles = map[string]string{
	"go.mod": "module example.com/app\n\ngo 1.21\n",
	"main.go": `package main

import (
	"fmt"

	"example.com/app/internal/cadd"
)

func main() { fmt.Println(cadd.Add(1, 2)) }
`,
	"internal/cadd/cadd.go": `package cadd

/*
#cgo CFLAGS: -I${SRCDIR}/include
#include "add.h"
*/
import "C"

func Add(a, b int) int { return int(C.add(C.int(a), C.int(b))) }
`,
	"internal/cadd/add.c":         "#include \"add.h\"\n\nint add(int a, int b) { return a + b; }\n",
	"internal/cadd/include/add.h": "int add(int a, int b);\n",
}

func TestRunCgoPackage(t *testing.T) {
	on := true
	src := testutil.WriteTree(t, cgoAppFiles)
	out, err := run(t, Args{Src: src, ModRoot: ".", SubPackages: []string{"."}, CgoEnabled: &on})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`      src = b.localDir { name = "gosrc-example.com-app-internal-cadd"; files = [ "internal/cadd/add.c" "internal/cadd/cadd.go" ]; trees = [ "internal/cadd/include" ]; };`,
		"      cgo = {\n",
		`        pkgName = "cadd";`,
		`        cgoFiles = [ "cadd.go" ];`,
		`        cFiles = [ "add.c" ];`,
		`        cflags = [ "-I\${SRCDIR}/include" ];`,
		"      cgo = true;\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	// go list expanded ${SRCDIR} to the evaluation-time directory; none of
	// it may reach the build.
	if strings.Contains(out, src) {
		t.Errorf("output names the evaluation-time source directory %s:\n%s", src, out)
	}
}

// With cgo off go list drops the cgo files, and the graph has no C in it.
func TestRunCgoPackageWithCgoDisabled(t *testing.T) {
	off := false
	files := map[string]string{}
	for name, content := range cgoAppFiles {
		files[name] = content
	}
	files["internal/cadd/pure.go"] = "//go:build !cgo\n\npackage cadd\n\nfunc Add(a, b int) int { return a + b }\n"
	out, err := run(t, Args{Src: testutil.WriteTree(t, files), ModRoot: ".", SubPackages: []string{"."}, CgoEnabled: &off})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "cgo = ") || strings.Contains(out, "add.c") {
		t.Errorf("a graph resolved with cgo off has C in it:\n%s", out)
	}
}

// testedFiles is a program whose package p has internal and external
// tests and testdata; the program imports q, which imports p, and p's
// external test imports q.
var testedFiles = map[string]string{
	"go.mod":            "module example.com/app\n\ngo 1.21\n",
	"main.go":           "package main\n\nimport \"example.com/app/q\"\n\nfunc main() { println(q.Two()) }\n",
	"p/p.go":            "package p\n\nfunc One() int { return 1 }\n",
	"p/p_test.go":       "package p\n\nimport \"testing\"\n\nfunc TestOne(t *testing.T) {\n\tif One() != 1 {\n\t\tt.Fatal(One())\n\t}\n}\n",
	"p/x_test.go":       "package p_test\n\nimport (\n\t\"testing\"\n\n\t\"example.com/app/q\"\n)\n\nfunc TestTwo(t *testing.T) { _ = q.Two() }\n",
	"p/testdata/in.txt": "in",
	"q/q.go":            "package q\n\nimport \"example.com/app/p\"\n\nfunc Two() int { return 2 * p.One() }\n",
}

func TestRunTests(t *testing.T) {
	out, err := run(t, Args{Src: testutil.WriteTree(t, testedFiles), ModRoot: ".", SubPackages: []string{"."}, DoCheck: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`    "example.com/app/p" = b.testDir {`,
		`      files = [ "p/p.go" "p/p_test.go" "p/x_test.go" ];`,
		`      trees = [ "p/testdata" ];`,
		`    "example.com/app/q [example.com/app/p.test]" = b.compile {`,
		`      deps = [ testPackages."example.com/app/p [example.com/app/p.test]" ];`,
		`    "example.com/app/p.test" = b.compile {`,
		`testing.MainStart`,
		`      binName = "p.test";`,
		`      test = true;`,
		`    "example.com/app/p" = b.runTest {`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	// The program itself has no tests.
	if strings.Contains(out, `"example.com/app.test"`) {
		t.Errorf("output tests the program, which has no test files:\n%s", out)
	}
}

func TestRunWithoutDoCheck(t *testing.T) {
	out, err := run(t, Args{Src: testutil.WriteTree(t, testedFiles), ModRoot: ".", SubPackages: []string{"."}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "  tests = {\n  };\n") || strings.Contains(out, "testMain") {
		t.Errorf("tests resolved without doCheck:\n%s", out)
	}
}

// go list -test needs a build cache; with the caller's off, resolve gives
// it a temporary one.
func TestRunTestsWithoutBuildCache(t *testing.T) {
	t.Setenv("GOCACHE", "off")
	out, err := run(t, Args{Src: testutil.WriteTree(t, testedFiles), ModRoot: ".", SubPackages: []string{"."}, DoCheck: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "testing.MainStart") {
		t.Errorf("no test main with GOCACHE=off:\n%s", out)
	}
}
