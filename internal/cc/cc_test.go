package cc

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/draganm/gonixgo/internal/gotool"
	"github.com/draganm/gonixgo/internal/testutil"
)

// fakeCompiler stands in for a C compiler: it logs each run's arguments,
// one per line and closed by a "--" line, and rejects, the way clang words
// it, any argument listed in $FAKE_REJECT.
const fakeCompiler = `#!/bin/sh
for a in "$@"; do printf '%s\n' "$a" >>"$FAKE_LOG"; done
echo -- >>"$FAKE_LOG"
for a in "$@"; do
  case " $FAKE_REJECT " in *" $a "*) echo "error: unknown argument: '$a'" >&2; exit 1;; esac
done
`

// fake returns a Compiler over the fake compiler and a function that
// returns the argument lists of its runs so far.
func fake(t *testing.T, target Target, reject string) (*Compiler, func() [][]string) {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "fake")
	if err := os.WriteFile(bin, []byte(fakeCompiler), 0o755); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(dir, "log")
	env := append(os.Environ(), "FAKE_LOG="+log, "FAKE_REJECT="+reject)
	runs := func() [][]string {
		data, err := os.ReadFile(log)
		if err != nil {
			return nil
		}
		var all [][]string
		var run []string
		for _, line := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
			if line == "--" {
				all = append(all, run)
				run = nil
				continue
			}
			run = append(run, line)
		}
		return all
	}
	return New([]string{bin}, target, env, []string{"-O2", "-g"}, []string{"-O1"}, dir), runs
}

func TestSupports(t *testing.T) {
	c, runs := fake(t, Target{GOOS: "linux", GOARCH: "amd64"}, "-Qunused-arguments -Wl,--no-gc-sections")
	if c.Supports("-Qunused-arguments") {
		t.Error("a flag the compiler calls unknown is reported as supported")
	}
	if !c.Supports("-fno-caret-diagnostics") {
		t.Error("a flag the compiler accepts is reported as unsupported")
	}
	if c.Supports("-Wl,--no-gc-sections") {
		t.Error("a linker flag the compiler calls unknown is reported as supported")
	}
	want := [][]string{
		// A compiler flag is probed without linking, with $CGO_CFLAGS.
		{"-Qunused-arguments", "-O2", "-g", "-c", "-x", "c", "-", "-o", os.DevNull},
		{"-fno-caret-diagnostics", "-O2", "-g", "-c", "-x", "c", "-", "-o", os.DevNull},
		// A linker flag is probed by linking, with $CGO_LDFLAGS.
		{"-Wl,--no-gc-sections", "-O1", "-x", "c", "-", "-o", os.DevNull},
	}
	if got := runs(); !reflect.DeepEqual(got, want) {
		t.Errorf("probes ran as\n%q\nwant\n%q", got, want)
	}

	c.Supports("-Qunused-arguments")
	c.Supports("-fno-caret-diagnostics")
	if got := len(runs()); got != 3 {
		t.Errorf("%d compiler runs after repeating two probes, want the 3 cached ones", got)
	}
}

func TestPrefix(t *testing.T) {
	c, _ := fake(t, Target{GOOS: "darwin", GOARCH: "arm64"}, "")
	want := []string{
		"-I", "inc", "-fPIC", "-arch", "arm64", "-pthread",
		"-fno-caret-diagnostics", "-Qunused-arguments", "-Wl,--no-gc-sections", "-fmessage-length=0",
		"-ffile-prefix-map=/work=/tmp/go-build", "-gno-record-gcc-switches", "-fno-common",
	}
	if got := c.Prefix("inc", "/work"); !reflect.DeepEqual(got[1:], want) {
		t.Errorf("darwin/arm64 prefix =\n%q\nwant\n%q", got[1:], want)
	}

	// gcc with an old GNU toolchain: no clang flags, and only the older
	// way to rewrite paths.
	c, _ = fake(t, Target{GOOS: "linux", GOARCH: "amd64"}, "-Qunused-arguments -Wl,--no-gc-sections -ffile-prefix-map=a=b")
	want = []string{
		"-I", "inc", "-fPIC", "-m64", "-pthread",
		"-fno-caret-diagnostics", "-fmessage-length=0",
		"-fdebug-prefix-map=/work=/tmp/go-build", "-gno-record-gcc-switches",
	}
	if got := c.Prefix("inc", "/work/"); !reflect.DeepEqual(got[1:], want) {
		t.Errorf("linux/amd64 prefix =\n%q\nwant\n%q", got[1:], want)
	}

	// A compiler that can rewrite no paths gets no rewriting flag.
	c, _ = fake(t, Target{GOOS: "windows", GOARCH: "amd64"}, "-fdebug-prefix-map=a=b -ffile-prefix-map=a=b -Wl,--no-gc-sections")
	want = []string{
		"-I", "inc", "-m64", "-mthreads",
		"-fno-caret-diagnostics", "-Qunused-arguments", "-fmessage-length=0", "-gno-record-gcc-switches",
	}
	if got := c.Prefix("inc", "/work"); !reflect.DeepEqual(got[1:], want) {
		t.Errorf("windows/amd64 prefix =\n%q\nwant\n%q", got[1:], want)
	}
}

func TestCompileCommand(t *testing.T) {
	c, runs := fake(t, Target{GOOS: "linux", GOARCH: "arm64"}, "-Qunused-arguments -Wl,--no-gc-sections")
	prefix := c.Prefix(".", "/work")
	probes := len(runs())
	job := Job{
		Dir: t.TempDir(), IncDir: ".", Flags: []string{"-I", "/work/", "-O2", `-DGREETING="hello world"`},
		File: "add.c", Obj: "/work/_x003.o", Seed: "abc",
	}
	if err := c.Compile(job, "/work", PathMap{From: "/store/src", To: "/_/example.com/app"}); err != nil {
		t.Fatal(err)
	}
	want := append(prefix[1:],
		"-I", "/work/", "-O2", `-DGREETING="hello world"`,
		"-ffile-prefix-map=/store/src=/_/example.com/app", "-frandom-seed=abc",
		"-o", "/work/_x003.o", "-c", "add.c")
	got := runs()[probes:]
	// The seed's probe runs once, before the compile.
	if len(got) != 2 || !reflect.DeepEqual(got[1], want) {
		t.Errorf("compile ran as\n%q\nwant\n%q", got, want)
	}

	// Without a source map no second rewriting flag is passed.
	if err := c.Compile(job, "/work", PathMap{}); err != nil {
		t.Fatal(err)
	}
	last := runs()
	if args := strings.Join(last[len(last)-1], " "); strings.Count(args, "prefix-map") != 1 {
		t.Errorf("compile without a source map ran as %q", args)
	}
}

func TestCompileReportsFailure(t *testing.T) {
	c, _ := fake(t, Target{GOOS: "linux", GOARCH: "arm64"}, "broken.c")
	err := c.Compile(Job{Dir: t.TempDir(), IncDir: ".", File: "broken.c", Obj: "/work/x.o"}, "/work", PathMap{})
	if err == nil || !strings.Contains(err.Error(), "broken.c") {
		t.Fatalf("err = %v, want it to name the file", err)
	}
}

func TestLinkCommand(t *testing.T) {
	c, runs := fake(t, Target{GOOS: "linux", GOARCH: "arm64"}, "-Qunused-arguments -Wl,--no-gc-sections -lmissing")
	prefix := c.Prefix("/src/pkg", "/work")
	before := len(runs())
	if _, err := c.Link(t.TempDir(), "/src/pkg", "/work", "/work/_cgo_.o", []string{"/work/a.o", "/work/b.o"}, []string{"-O2", "-lm"}); err != nil {
		t.Fatal(err)
	}
	want := append(prefix[1:], "-o", "/work/_cgo_.o", "/work/a.o", "/work/b.o", "-O2", "-lm")
	if got := runs()[before:]; len(got) != 1 || !reflect.DeepEqual(got[0], want) {
		t.Errorf("link ran as\n%q\nwant\n%q", got, want)
	}

	out, err := c.Link(t.TempDir(), "/src/pkg", "/work", "/work/_cgo_.o", nil, []string{"-lmissing"})
	if err == nil || !strings.Contains(string(out), "-lmissing") {
		t.Errorf("a failing link returned err = %v and output %q", err, out)
	}
}

func TestArchArgs(t *testing.T) {
	tests := []struct {
		t    Target
		want []string
	}{
		{Target{GOOS: "darwin", GOARCH: "arm64"}, []string{"-arch", "arm64"}},
		{Target{GOOS: "darwin", GOARCH: "amd64"}, []string{"-arch", "x86_64", "-m64"}},
		{Target{GOOS: "linux", GOARCH: "amd64"}, []string{"-m64"}},
		{Target{GOOS: "linux", GOARCH: "386"}, []string{"-m32"}},
		{Target{GOOS: "linux", GOARCH: "arm64"}, nil},
		{Target{GOOS: "linux", GOARCH: "arm"}, []string{"-marm"}},
		{Target{GOOS: "linux", GOARCH: "s390x"}, []string{"-m64", "-march=z13"}},
		{Target{GOOS: "linux", GOARCH: "mips64le", GOMIPS64: "hardfloat"}, []string{"-mabi=64", "-mhard-float"}},
		{Target{GOOS: "linux", GOARCH: "mips", GOMIPS: "softfloat"}, []string{"-mabi=32", "-march=mips32", "-msoft-float"}},
		{Target{GOOS: "linux", GOARCH: "loong64"}, []string{"-mabi=lp64d"}},
		{Target{GOOS: "aix", GOARCH: "ppc64"}, []string{"-maix64"}},
		{Target{GOOS: "linux", GOARCH: "ppc64le"}, nil},
	}
	for _, tt := range tests {
		if got := ArchArgs(tt.t); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("ArchArgs(%+v) = %q, want %q", tt.t, got, tt.want)
		}
	}
}

// realCompiler returns a Compiler over the host's C compiler, skipping the
// test when there is none.
func realCompiler(t *testing.T, probeDir string) *Compiler {
	t.Helper()
	tc, err := gotool.New(testutil.Go(t), "", "", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cmd, err := gotool.SplitFlags([]string{tc.CC()})
	if err != nil || len(cmd) == 0 {
		t.Skipf("no C compiler command: %q, %v", tc.CC(), err)
	}
	if _, err := exec.LookPath(cmd[0]); err != nil {
		t.Skipf("C compiler %s not on PATH", cmd[0])
	}
	return New(cmd, Target{GOOS: tc.GOOS, GOARCH: tc.GOARCH}, os.Environ(), []string{"-O2", "-g"}, []string{"-O2", "-g"}, probeDir)
}

func TestCompileAndLinkWithTheRealCompiler(t *testing.T) {
	src := testutil.WriteTree(t, map[string]string{
		"where.c": "const char *where(void) { return __FILE__; }\n",
		"main.c":  "const char *where(void);\nint main(void) { return where() == 0; }\n",
	})
	work := t.TempDir()
	c := realCompiler(t, work)
	compile := func(file string, srcMap PathMap) []byte {
		t.Helper()
		obj := filepath.Join(work, file+".o")
		// A file named by its full path puts that path into __FILE__.
		job := Job{Dir: src, IncDir: ".", Flags: []string{"-O2", "-g"}, File: filepath.Join(src, file), Obj: obj, Seed: "seed"}
		if err := c.Compile(job, work, srcMap); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(obj)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}

	if obj := compile("where.c", PathMap{}); !bytes.Contains(obj, []byte(src)) {
		t.Fatal("an object compiled without a source map does not name its source directory; the next check would prove nothing")
	}
	obj := compile("where.c", PathMap{From: src, To: "/_/example.com/app"})
	if bytes.Contains(obj, []byte(src)) {
		t.Errorf("the object names the source directory %s despite the source map", src)
	}
	if !bytes.Contains(obj, []byte("/_/example.com/app/where.c")) {
		t.Error("the object does not name the rewritten source path")
	}

	compile("main.c", PathMap{From: src, To: "/_/example.com/app"})
	exe := filepath.Join(work, "prog")
	objs := []string{filepath.Join(work, "main.c.o"), filepath.Join(work, "where.c.o")}
	if out, err := c.Link(src, src, work, exe, objs, []string{"-O2", "-g"}); err != nil {
		t.Fatalf("link: %v\n%s", err, out)
	}
	if err := exec.Command(exe).Run(); err != nil {
		t.Errorf("the linked program failed: %v", err)
	}
	if out, err := c.Link(src, src, work, exe, objs[:1], nil); err == nil {
		t.Errorf("a link with an undefined symbol succeeded:\n%s", out)
	}
}
