package golist

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

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
}

func index(pkgs []Package) map[string]Package {
	m := make(map[string]Package, len(pkgs))
	for _, p := range pkgs {
		m[p.ImportPath] = p
	}
	return m
}

func TestListClassifiesPackages(t *testing.T) {
	goBin := testutil.Go(t)
	dir := testutil.WriteTree(t, appFiles)

	pkgs, err := List(Options{Go: goBin, Dir: dir}, ".")
	if err != nil {
		t.Fatal(err)
	}
	by := index(pkgs)

	main, ok := by["example.com/app"]
	if !ok {
		t.Fatalf("example.com/app missing from %d packages", len(pkgs))
	}
	if main.DepOnly || main.Name != "main" || main.Module == nil || !main.Module.Main {
		t.Errorf("main package = %+v", main)
	}
	if main.Dir != dir {
		t.Errorf("main.Dir = %s, want %s", main.Dir, dir)
	}
	if !reflect.DeepEqual(main.GoFiles, []string{"main.go"}) {
		t.Errorf("main.GoFiles = %v", main.GoFiles)
	}

	greet := by["example.com/app/internal/greet"]
	if !greet.DepOnly || greet.Module == nil || !greet.Module.Main {
		t.Errorf("greet package = %+v", greet)
	}
	if fmtPkg := by["fmt"]; !fmtPkg.Standard {
		t.Errorf("fmt package = %+v, want Standard", fmtPkg)
	}
}

func TestListReportsBrokenPackagesWithoutFailing(t *testing.T) {
	goBin := testutil.Go(t)
	files := map[string]string{
		"go.mod":  "module example.com/app\n\ngo 1.21\n",
		"main.go": "package main\n\nimport _ \"example.com/app/missing\"\n\nfunc main() {}\n",
	}
	pkgs, err := List(Options{Go: goBin, Dir: testutil.WriteTree(t, files)}, ".")
	if err != nil {
		t.Fatalf("List failed, want the error on the package: %v", err)
	}
	missing := index(pkgs)["example.com/app/missing"]
	if missing.Error == nil || missing.Error.Err == "" {
		t.Fatalf("missing package = %+v, want Error set", missing)
	}
}

func TestListAppliesTags(t *testing.T) {
	goBin := testutil.Go(t)
	files := map[string]string{
		"go.mod":   "module example.com/app\n\ngo 1.21\n",
		"main.go":  "package main\n\nfunc main() {}\n",
		"extra.go": "//go:build extra\n\npackage main\n\nvar _ = 1\n",
	}
	dir := testutil.WriteTree(t, files)
	pkgs, err := List(Options{Go: goBin, Dir: dir, Tags: []string{"extra"}}, ".")
	if err != nil {
		t.Fatal(err)
	}
	got := index(pkgs)["example.com/app"].GoFiles
	if !reflect.DeepEqual(got, []string{"extra.go", "main.go"}) {
		t.Fatalf("GoFiles = %v, want extra.go and main.go", got)
	}
}

func TestDecode(t *testing.T) {
	const stream = `{"ImportPath": "fmt", "Standard": true}
{"ImportPath": "example.com/a", "Module": {"Path": "example.com/a", "Main": true},
 "Error": {"Pos": "a.go:1:1", "Err": "boom"}}`
	pkgs, err := Decode(strings.NewReader(stream))
	if err != nil {
		t.Fatal(err)
	}
	if len(pkgs) != 2 || !pkgs[0].Standard || pkgs[1].Error.Err != "boom" || !pkgs[1].Module.Main {
		t.Fatalf("decoded %+v", pkgs)
	}
}

func TestDecodeRejectsGarbage(t *testing.T) {
	if _, err := Decode(strings.NewReader(`{"ImportPath": `)); err == nil {
		t.Fatal("Decode of truncated input succeeded")
	}
}

func TestEnv(t *testing.T) {
	goBin := testutil.Go(t)
	dir := testutil.WriteTree(t, appFiles)
	env, err := Env(Options{Go: goBin, Dir: dir, GOOS: "plan9", GOARCH: "amd64"}, "GOOS", "GOARCH", "GOVERSION")
	if err != nil {
		t.Fatal(err)
	}
	if env["GOOS"] != "plan9" || env["GOARCH"] != "amd64" || !strings.HasPrefix(env["GOVERSION"], "go") {
		t.Fatalf("env = %v", env)
	}
}

func TestEnvIgnoresCallerBuildConfiguration(t *testing.T) {
	goBin := testutil.Go(t)
	dir := testutil.WriteTree(t, appFiles)
	o := Options{Go: goBin, Dir: dir}
	keys := []string{"CGO_ENABLED", "GOFLAGS", "GOWORK", "GOTOOLCHAIN", "GOEXPERIMENT"}
	clean, err := Env(o, append(keys, "GOARM64", "GOAMD64")...)
	if err != nil {
		t.Fatal(err)
	}

	if archKey := testutil.HostileGoEnv(t); archKey != "" {
		keys = append(keys, archKey)
	}
	hostile, err := Env(o, keys...)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range keys {
		if hostile[k] != clean[k] {
			t.Errorf("%s = %q in a hostile environment, %q in a clean one", k, hostile[k], clean[k])
		}
	}
	for k, want := range map[string]string{"GOFLAGS": "-mod=readonly", "GOWORK": "off", "GOTOOLCHAIN": "local"} {
		if hostile[k] != want {
			t.Errorf("%s = %q, want %q", k, hostile[k], want)
		}
	}
	if _, err := List(o, "."); err != nil {
		t.Errorf("List in a hostile environment: %v", err)
	}
}

func TestModuleEnvCarriesModuleSources(t *testing.T) {
	goBin := testutil.Go(t)
	dir := testutil.WriteTree(t, appFiles)
	envFile := filepath.Join(t.TempDir(), "goenv")
	if err := os.WriteFile(envFile, []byte("GOPROXY=https://proxy.example.com\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOENV", envFile)
	t.Setenv("GOPRIVATE", "example.com/private")
	t.Setenv("GOPROXY", "")
	os.Unsetenv("GOPROXY")

	me, err := ModuleEnv(goBin, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"GOPRIVATE": "example.com/private", "GOPROXY": "https://proxy.example.com"}
	for k, v := range want {
		if me[k] != v {
			t.Errorf("ModuleEnv %s = %q, want %q", k, me[k], v)
		}
	}
	env, err := Env(Options{Go: goBin, Dir: dir, ModuleEnv: me}, "GOPRIVATE", "GOPROXY")
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range want {
		if env[k] != v {
			t.Errorf("Env with ModuleEnv: %s = %q, want %q", k, env[k], v)
		}
	}
}

// testedFiles is a module whose package p has internal and external
// tests, each with an embed; p's external test imports q, which imports p.
var testedFiles = map[string]string{
	"go.mod": "module example.com/m\n\ngo 1.21\n",
	"p/p.go": "package p\n\nfunc One() int { return 1 }\n",
	"p/p_test.go": `package p

import (
	_ "embed"
	"testing"
)

//go:embed testdata/in.txt
var in string

func TestOne(t *testing.T) {}
`,
	"p/x_test.go": `package p_test

import (
	_ "embed"
	"testing"

	"example.com/m/q"
)

//go:embed testdata/x.txt
var x string

func TestTwo(t *testing.T) { _ = q.Two() }
`,
	"p/testdata/in.txt": "in",
	"p/testdata/x.txt":  "x",
	"q/q.go":            "package q\n\nimport \"example.com/m/p\"\n\nfunc Two() int { return 2 * p.One() }\n",
}

func TestListTests(t *testing.T) {
	goBin := testutil.Go(t)
	dir := testutil.WriteTree(t, testedFiles)

	pkgs, err := ListTests(Options{Go: goBin, Dir: dir}, "example.com/m/p")
	if err != nil {
		t.Fatal(err)
	}
	by := index(pkgs)

	p := by["example.com/m/p"]
	if !reflect.DeepEqual(p.TestGoFiles, []string{"p_test.go"}) || !reflect.DeepEqual(p.XTestGoFiles, []string{"x_test.go"}) {
		t.Errorf("p test files = %v %v", p.TestGoFiles, p.XTestGoFiles)
	}
	if !reflect.DeepEqual(p.TestEmbedPatterns, []string{"testdata/in.txt"}) || !reflect.DeepEqual(p.XTestEmbedPatterns, []string{"testdata/x.txt"}) {
		t.Errorf("p test embeds = %v %v", p.TestEmbedPatterns, p.XTestEmbedPatterns)
	}
	for _, ip := range []string{
		"example.com/m/p [example.com/m/p.test]",
		"example.com/m/q [example.com/m/p.test]",
		"example.com/m/p_test [example.com/m/p.test]",
	} {
		if got := by[ip].ForTest; got != "example.com/m/p" {
			t.Errorf("%s: ForTest = %q, want example.com/m/p", ip, got)
		}
	}
	main, ok := by["example.com/m/p.test"]
	if !ok || main.Name != "main" || main.ForTest != "" || len(main.GoFiles) != 1 {
		t.Fatalf("test main = %+v", main)
	}
	src, err := os.ReadFile(main.GoFiles[0])
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(src, []byte("testing.MainStart")) {
		t.Errorf("test main source:\n%s", src)
	}
}

// go list -test writes each test main into the build cache; a private one
// works too, which resolve uses when the caller's is off.
func TestListTestsWithPrivateCache(t *testing.T) {
	goBin := testutil.Go(t)
	dir := testutil.WriteTree(t, testedFiles)
	cache := t.TempDir()

	pkgs, err := ListTests(Options{Go: goBin, Dir: dir, GOCACHE: cache}, "example.com/m/p")
	if err != nil {
		t.Fatal(err)
	}
	main := index(pkgs)["example.com/m/p.test"]
	if len(main.GoFiles) != 1 || !strings.HasPrefix(main.GoFiles[0], cache) {
		t.Fatalf("test main files = %v, want one under %s", main.GoFiles, cache)
	}
}
