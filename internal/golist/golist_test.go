package golist

import (
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
