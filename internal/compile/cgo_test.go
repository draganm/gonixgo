package compile

import (
	"bytes"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/draganm/gonixgo/internal/testutil"
)

var cgoFiles = map[string]string{
	"cadd/cadd.go": `package cadd

/*
#cgo CFLAGS: -DBONUS=0 -I${SRCDIR}/include -DGREETING="hello world"
#include "add.h"
*/
import "C"

func Add(a, b int) int { return int(C.add(C.int(a), C.int(b))) }

//export goTwice
func goTwice(x C.int) C.int { return 2 * x }

func Twice(x int) int { return int(C.call_twice(C.int(x))) }

func Greeting() string { return C.GoString(C.greeting()) }
`,
	"cadd/plain.go":      "package cadd\n\nconst Plain = 1\n",
	"cadd/include/add.h": "int add(int a, int b);\nint call_twice(int x);\nconst char *greeting(void);\nconst char *where(void);\n",
	"cadd/add.c": `#include "add.h"
#include "_cgo_export.h"

int add(int a, int b) { return a + b + BONUS; }
int call_twice(int x) { return goTwice(x); }
const char *greeting(void) { return GREETING; }
const char *where(void) { return __FILE__; }
`,
}

// cgoManifest is the manifest of the cadd package in src. Its flags are
// as resolve prints them: ${SRCDIR} not expanded.
func cgoManifest(goBin, src, std string) Manifest {
	return Manifest{
		Go: goBin, ImportPath: "example.com/app/cadd", SrcDir: filepath.Join(src, "cadd"),
		TrimTo: "example.com/app/cadd", Lang: "go1.21",
		GoFiles: []string{"plain.go"},
		Cgo: &Cgo{
			PkgName: "cadd", CgoFiles: []string{"cadd.go"}, CFiles: []string{"add.c"},
			CFLAGS: []string{"-DBONUS=0", "-I${SRCDIR}/include", `-DGREETING="hello world"`},
		},
		Importcfgs: []string{std},
	}
}

// members lists the names of an archive's members.
func members(t *testing.T, archive []byte) []string {
	t.Helper()
	var names []string
	for off := len("!<arch>\n"); off+60 <= len(archive); {
		header := archive[off : off+60]
		size, err := strconv.Atoi(strings.TrimSpace(string(header[48:58])))
		if err != nil {
			t.Fatalf("archive header at %d: %v", off, err)
		}
		names = append(names, strings.TrimRight(string(header[:16]), " "))
		off += 60 + size + size%2
	}
	return names
}

func readArchive(t *testing.T, outDir string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(outDir, "pkg.a"))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestRunCgo(t *testing.T) {
	goBin := testutil.Go(t)
	std := testutil.CgoStdImportcfg(t, goBin)
	src := testutil.WriteTree(t, cgoFiles)
	out, work := t.TempDir(), t.TempDir()
	if err := Run(cgoManifest(goBin, src, std), out, work); err != nil {
		t.Fatal(err)
	}
	archive := readArchive(t, out)
	// The Go object, then _cgo_export.c, cadd.cgo2.c and add.c. The trial
	// link's _cgo_main.o and _cgo_.o stay out.
	want := []string{"__.PKGDEF", "_go_.o", "_x001.o", "_x002.o", "_x003.o"}
	if got := members(t, archive); !reflect.DeepEqual(got, want) {
		t.Errorf("archive members = %q, want %q", got, want)
	}
	for name, dir := range map[string]string{"source": src, "scratch": work} {
		if bytes.Contains(archive, []byte(dir)) {
			t.Errorf("pkg.a names the %s directory %s", name, dir)
		}
	}
	// The C compiler recorded the package directory under the module's name.
	if !bytes.Contains(archive, []byte("/_/example.com/app/cadd")) {
		t.Error("pkg.a does not name the rewritten source directory /_/example.com/app/cadd")
	}
}

func TestRunCgoIsReproducible(t *testing.T) {
	goBin := testutil.Go(t)
	std := testutil.CgoStdImportcfg(t, goBin)
	var archives [2][]byte
	for i := range archives {
		out := t.TempDir()
		if err := Run(cgoManifest(goBin, testutil.WriteTree(t, cgoFiles), std), out, t.TempDir()); err != nil {
			t.Fatal(err)
		}
		archives[i] = readArchive(t, out)
	}
	if !bytes.Equal(archives[0], archives[1]) {
		t.Fatal("two compiles of the same cgo package in different directories differ")
	}
}

// A symbol that only the final link resolves makes cgo's trial link fail.
// The package must still build, marked for external linking.
func TestRunCgoTrialLinkFailure(t *testing.T) {
	goBin := testutil.Go(t)
	std := testutil.CgoStdImportcfg(t, goBin)
	files := maps.Clone(cgoFiles)
	files["cadd/ext.c"] = "extern int not_defined_anywhere(void);\nint use_it(void) { return not_defined_anywhere(); }\n"
	m := cgoManifest(goBin, testutil.WriteTree(t, files), std)
	m.Cgo.CFiles = []string{"add.c", "ext.c"}
	out := t.TempDir()
	var runErr error
	log := captureStderr(t, func() { runErr = Run(m, out, t.TempDir()) })
	if runErr != nil {
		t.Fatal(runErr)
	}
	want := []string{"__.PKGDEF", "_go_.o", "_x001.o", "_x002.o", "_x003.o", "_x004.o", "dynimportfail"}
	if got := members(t, readArchive(t, out)); !reflect.DeepEqual(got, want) {
		t.Errorf("archive members = %q, want %q", got, want)
	}
	// The log of a build that succeeds now holds a linker's error. It must
	// say which package it is about and that the build did not fail.
	for _, want := range []string{"example.com/app/cadd", "not an error", "not_defined_anywhere"} {
		if !strings.Contains(log, want) {
			t.Errorf("the build log lacks %q:\n%s", want, log)
		}
	}
}

// captureStderr runs f with the process's standard error, which the build
// steps write their diagnostics to, going to a file, and returns what was
// written.
func captureStderr(t *testing.T, f func()) string {
	t.Helper()
	file, err := os.CreateTemp(t.TempDir(), "stderr")
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stderr
	os.Stderr = file
	defer func() { os.Stderr = saved }()
	f()
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(file.Name())
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestRunCgoPkgConfigMissing(t *testing.T) {
	goBin := testutil.Go(t)
	std := testutil.CgoStdImportcfg(t, goBin)
	t.Setenv("PKG_CONFIG", "/nonexistent/pkg-config")
	m := cgoManifest(goBin, testutil.WriteTree(t, cgoFiles), std)
	m.Cgo.PkgConfig = []string{"libnope"}
	err := Run(m, t.TempDir(), t.TempDir())
	if err == nil {
		t.Fatal("compiling a package that needs pkg-config succeeded without one")
	}
	for _, want := range []string{
		"/nonexistent/pkg-config", "uses pkg-config (libnope)",
		`packageOverrides."example.com/app/cadd".nativeBuildInputs`, "buildInputs",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q:\n%v", want, err)
		}
	}
}

func TestRunCgoCompileErrorHint(t *testing.T) {
	goBin := testutil.Go(t)
	std := testutil.CgoStdImportcfg(t, goBin)
	files := maps.Clone(cgoFiles)
	files["cadd/add.c"] = "#include <no_such_header.h>\n"
	err := Run(cgoManifest(goBin, testutil.WriteTree(t, files), std), t.TempDir(), t.TempDir())
	if want := `packageOverrides."example.com/app/cadd".buildInputs`; err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("err = %v, want it to say where a missing header comes from: %s", err, want)
	}
}

// A header missing from the cgo file's own preamble fails in cgo, before
// any C file is compiled; the hint must be there too.
func TestRunCgoToolErrorHint(t *testing.T) {
	goBin := testutil.Go(t)
	std := testutil.CgoStdImportcfg(t, goBin)
	files := maps.Clone(cgoFiles)
	files["cadd/cadd.go"] = "package cadd\n\n// #include <no_such_header.h>\nimport \"C\"\n\nfunc Add(a, b int) int { return int(C.add(C.int(a), C.int(b))) }\n"
	err := Run(cgoManifest(goBin, testutil.WriteTree(t, files), std), t.TempDir(), t.TempDir())
	if want := `packageOverrides."example.com/app/cadd".buildInputs`; err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("err = %v, want it to say where a missing header comes from: %s", err, want)
	}
}

func TestRunCgoRejectsGoAssembly(t *testing.T) {
	goBin := testutil.Go(t)
	std := testutil.CgoStdImportcfg(t, goBin)
	files := maps.Clone(cgoFiles)
	files["cadd/nop.s"] = "#include \"textflag.h\"\n\nTEXT ·Nop(SB),NOSPLIT,$0-0\n\tRET\n"
	m := cgoManifest(goBin, testutil.WriteTree(t, files), std)
	m.SFiles = []string{"nop.s"}
	err := Run(m, t.TempDir(), t.TempDir())
	if want := "package using cgo has Go assembly file nop.s"; err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("err = %v, want %q", err, want)
	}
}

func TestTrimRoot(t *testing.T) {
	tests := []struct{ srcDir, trimTo, dir, name string }{
		{"/s/internal/cadd", "example.com/app/internal/cadd", "/s", "example.com/app"},
		{"/s", "example.com/app", "/s", "example.com/app"},
		{"/m/sub/pkg", "mod@v1.0.0/sub/pkg", "/m", "mod@v1.0.0"},
		// A package of a module replaced by a directory: cmd/go maps the
		// module's directory to its path and required version.
		{"/s/lib/sub", "example.com/lib@v1.2.3/sub", "/s/lib", "example.com/lib@v1.2.3"},
		// The module is in a subdirectory of the source whose name is not
		// part of the module path.
		{"/s/go/internal/x", "example.com/app/internal/x", "/s/go", "example.com/app"},
		// The name never becomes empty.
		{"/a/b", "b", "/a/b", "b"},
	}
	for _, tt := range tests {
		if dir, name := trimRoot(tt.srcDir, tt.trimTo); dir != tt.dir || name != tt.name {
			t.Errorf("trimRoot(%q, %q) = %q, %q; want %q, %q", tt.srcDir, tt.trimTo, dir, name, tt.dir, tt.name)
		}
	}
}

func TestTrialLinkFlags(t *testing.T) {
	tests := []struct {
		goos, goarch string
		in, want     []string
	}{
		{"darwin", "arm64", []string{"-O2", "-lm"}, []string{"-O2", "-lm"}},
		{"linux", "amd64", []string{"-static"}, []string{"-static"}},
		{"linux", "arm", []string{"-lm"}, []string{"-lm", "-pie"}},
		{"linux", "arm", []string{"-no-pie"}, []string{"-no-pie"}},
		{"android", "arm64", []string{"-static", "-lm"}, []string{"-lm", "-pie"}},
	}
	for _, tt := range tests {
		if got := trialLinkFlags(tt.goos, tt.goarch, tt.in); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("trialLinkFlags(%s, %s, %q) = %q, want %q", tt.goos, tt.goarch, tt.in, got, tt.want)
		}
	}
}

// Without trimming, as under go test without -trimpath, the C compiler
// keeps the source paths too.
func TestRunCgoUntrimmed(t *testing.T) {
	goBin := testutil.Go(t)
	std := testutil.CgoStdImportcfg(t, goBin)
	src := testutil.WriteTree(t, cgoFiles)
	m := cgoManifest(goBin, src, std)
	m.TrimTo = ""
	out, work := t.TempDir(), t.TempDir()
	if err := Run(m, out, work); err != nil {
		t.Fatal(err)
	}
	archive := readArchive(t, out)
	if !bytes.Contains(archive, []byte(filepath.Join(src, "cadd"))) {
		t.Errorf("pkg.a does not name the source directory %s", filepath.Join(src, "cadd"))
	}
	if bytes.Contains(archive, []byte("/_/")) || bytes.Contains(archive, []byte(work)) {
		t.Error("pkg.a has a rewritten source path or names the scratch directory")
	}
}
