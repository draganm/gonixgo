package compile

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/draganm/gonixgo/internal/testutil"
)

var files = map[string]string{
	"plain/plain.go": "package plain\n\nimport \"fmt\"\n\nfunc Hello() string { return fmt.Sprint(\"hello\") }\n",
	"lib/lib.go": `package lib

import _ "embed"

//go:embed data/msg.txt
var Msg string

// Nop is implemented in assembly.
func Nop()
`,
	// RET assembles on every architecture Go supports.
	"lib/nop.s":        "#include \"textflag.h\"\n\nTEXT ·Nop(SB),NOSPLIT,$0-0\n\tRET\n",
	"lib/data/msg.txt": "embedded",
	"broken/broken.go": "package broken\n\nfunc Oops() { return 1 }\n",
}

func libManifest(goBin, src, std string) Manifest {
	return Manifest{
		Go: goBin, ImportPath: "example.com/app/lib", SrcDir: filepath.Join(src, "lib"),
		TrimTo: "example.com/app/lib", Lang: "go1.21",
		GoFiles: []string{"lib.go"}, SFiles: []string{"nop.s"},
		Embed:      map[string][]string{"data/msg.txt": {"data/msg.txt"}},
		Importcfgs: []string{std},
	}
}

func TestRunWritesArchiveAndImportcfg(t *testing.T) {
	goBin := testutil.Go(t)
	src := testutil.WriteTree(t, files)
	out := filepath.Join(t.TempDir(), "out")
	m := Manifest{
		Go: goBin, ImportPath: "example.com/app/plain", SrcDir: filepath.Join(src, "plain"),
		TrimTo: "example.com/app/plain", Lang: "go1.21", GoFiles: []string{"plain.go"},
		Importcfgs: []string{testutil.StdImportcfg(t, goBin)},
	}
	if err := Run(m, out, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	archive, err := os.ReadFile(filepath.Join(out, "pkg.a"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(archive, []byte("!<arch>\n")) {
		t.Fatalf("pkg.a does not start with the archive magic")
	}
	if bytes.Contains(archive, []byte(src)) {
		t.Fatalf("pkg.a contains the source directory %s; -trimpath did not apply", src)
	}
	cfg, err := os.ReadFile(filepath.Join(out, "importcfg"))
	if err != nil {
		t.Fatal(err)
	}
	if want := "packagefile example.com/app/plain=" + filepath.Join(out, "pkg.a") + "\n"; string(cfg) != want {
		t.Fatalf("importcfg = %q, want %q", cfg, want)
	}
}

func TestRunWithAssemblyAndEmbed(t *testing.T) {
	goBin := testutil.Go(t)
	src := testutil.WriteTree(t, files)
	out := t.TempDir()
	if err := Run(libManifest(goBin, src, testutil.StdImportcfg(t, goBin)), out, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	archive, err := os.ReadFile(filepath.Join(out, "pkg.a"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(archive, []byte("nop.o")) {
		t.Fatal("pkg.a has no nop.o member")
	}
	if !bytes.Contains(archive, []byte("embedded")) {
		t.Fatal("pkg.a does not contain the embedded file's content")
	}
}

func TestRunIsReproducible(t *testing.T) {
	goBin := testutil.Go(t)
	src := testutil.WriteTree(t, files)
	std := testutil.StdImportcfg(t, goBin)
	var archives [2][]byte
	for i := range archives {
		out := t.TempDir()
		if err := Run(libManifest(goBin, src, std), out, t.TempDir()); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(filepath.Join(out, "pkg.a"))
		if err != nil {
			t.Fatal(err)
		}
		archives[i] = data
	}
	if !bytes.Equal(archives[0], archives[1]) {
		t.Fatal("two compiles of the same package in different directories differ")
	}
}

func TestRunReportsCompileErrors(t *testing.T) {
	goBin := testutil.Go(t)
	src := testutil.WriteTree(t, files)
	m := Manifest{
		Go: goBin, ImportPath: "example.com/app/broken", SrcDir: filepath.Join(src, "broken"),
		TrimTo: "example.com/app/broken", Lang: "go1.21", GoFiles: []string{"broken.go"},
		Importcfgs: []string{testutil.StdImportcfg(t, goBin)},
	}
	if err := Run(m, t.TempDir(), t.TempDir()); err == nil {
		t.Fatal("compiling a package with a type error succeeded")
	}
}

func TestWriteEmbedcfg(t *testing.T) {
	file := filepath.Join(t.TempDir(), "embedcfg")
	embed := map[string][]string{"static": {"static/a.txt", "static/b c.txt"}, "one.txt": {"one.txt"}}
	if err := writeEmbedcfg(file, "/src/pkg", embed); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Patterns map[string][]string
		Files    map[string]string
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	wantFiles := map[string]string{
		"static/a.txt":   "/src/pkg/static/a.txt",
		"static/b c.txt": "/src/pkg/static/b c.txt",
		"one.txt":        "/src/pkg/one.txt",
	}
	if !reflect.DeepEqual(got.Patterns, embed) || !reflect.DeepEqual(got.Files, wantFiles) {
		t.Fatalf("embedcfg = %+v", got)
	}
}

func TestAppendObjects(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "pkg.a")
	if err := os.WriteFile(archive, []byte("!<arch>\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	odd := filepath.Join(dir, "odd.o")
	even := filepath.Join(dir, "a-rather-long-object-name.o")
	if err := os.WriteFile(odd, []byte("abc"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(even, []byte("abcd"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := appendObjects(archive, []string{odd, even}); err != nil {
		t.Fatal(err)
	}
	header := func(name string, size int) string {
		return fmt.Sprintf("%-16s%-12d%-6d%-6d%-8o%-10d`\n", name, 0, 0, 0, 0o644, size)
	}
	// Members are padded to an even length; names are cut to 16 bytes.
	want := "!<arch>\n" + header("odd.o", 3) + "abc\x00" + header("a-rather-long-ob", 4) + "abcd"
	got, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("archive = %q\nwant      %q", got, want)
	}
}
