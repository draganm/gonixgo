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

func TestSplitFlags(t *testing.T) {
	tests := []struct {
		in   []string
		want []string
	}{
		{nil, nil},
		{[]string{"-s", "-w"}, []string{"-s", "-w"}},
		{[]string{"-s -w"}, []string{"-s", "-w"}},
		{[]string{"-X main.version=1.2.3"}, []string{"-X", "main.version=1.2.3"}},
		{[]string{"-X 'main.msg=hello world'"}, []string{"-X", "main.msg=hello world"}},
		{[]string{`-X "main.msg=it's"`}, []string{"-X", "main.msg=it's"}},
		{[]string{"  -s  \t -w  "}, []string{"-s", "-w"}},
		{[]string{"-X", "main.v=1"}, []string{"-X", "main.v=1"}},
	}
	for _, tt := range tests {
		got, err := SplitFlags(tt.in)
		if err != nil {
			t.Errorf("SplitFlags(%q): %v", tt.in, err)
			continue
		}
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("SplitFlags(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
	if _, err := SplitFlags([]string{"-X 'main.msg=unterminated"}); err == nil {
		t.Error("SplitFlags accepted an unterminated quote")
	}
}

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
