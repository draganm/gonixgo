package testrun

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/draganm/gonixgo/internal/testutil"
)

// script writes a shell script that stands in for a test binary.
func script(t *testing.T, body string) string {
	t.Helper()
	file := filepath.Join(t.TempDir(), "p.test")
	if err := os.WriteFile(file, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return file
}

// readOnly makes the tree at dir read-only, as the store is, keeping
// execute bits, until the test ends.
func readOnly(t *testing.T, dir string) {
	t.Helper()
	var dirs []string
	filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err == nil && d.Type()&fs.ModeSymlink == 0 {
			if d.IsDir() {
				dirs = append(dirs, path)
			} else if info, err := d.Info(); err == nil {
				os.Chmod(path, info.Mode().Perm()&^0o222)
			}
		}
		return nil
	})
	for i := len(dirs) - 1; i >= 0; i-- {
		os.Chmod(dirs[i], 0o555)
	}
	t.Cleanup(func() {
		for _, d := range dirs {
			os.Chmod(d, 0o755)
		}
	})
}

// run runs m with the work directory and the log in temporary directories
// and returns the error, the log, what went to stderr and the work
// directory.
func run(t *testing.T, m Manifest) (error, string, string, string) {
	t.Helper()
	work, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(t.TempDir(), "log")
	var stderr bytes.Buffer
	runErr := Run(m, logPath, work, &stderr)
	data, _ := os.ReadFile(logPath)
	return runErr, string(data), stderr.String(), work
}

func TestTranslateFlags(t *testing.T) {
	for _, tt := range []struct{ in, want []string }{
		{nil, []string{}},
		{[]string{"-v", "-short"}, []string{"-test.v", "-test.short"}},
		{[]string{"-run", "TestA", "-count=2"}, []string{"-test.run", "TestA", "-test.count=2"}},
		{[]string{"--skip=TestB", "--failfast"}, []string{"-test.skip=TestB", "-test.failfast"}},
		// The value of -run is not a flag.
		{[]string{"-run", "-v"}, []string{"-test.run", "-v"}},
		// Already prefixed, the test's own, and a build flag: unchanged.
		{[]string{"-test.run=X", "-update", "-race"}, []string{"-test.run=X", "-update", "-race"}},
		{[]string{"-v", "-args", "-run", "x"}, []string{"-test.v", "-run", "x"}},
		{[]string{"-v", "--", "-run", "x"}, []string{"-test.v", "--", "-run", "x"}},
	} {
		if got := translateFlags(tt.in); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("translateFlags(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestEffectiveTimeout(t *testing.T) {
	for _, tt := range []struct {
		args []string
		want time.Duration
	}{
		{[]string{"-test.timeout=10m0s"}, 10 * time.Minute},
		{[]string{"-test.timeout=10m0s", "-test.timeout", "5s"}, 5 * time.Second},
		{[]string{"-test.timeout=10m0s", "-test.timeout=0"}, 0},
		{[]string{"-test.timeout=10m0s", "--", "-test.timeout=1s"}, 10 * time.Minute},
	} {
		if got := effectiveTimeout(tt.args); got != tt.want {
			t.Errorf("effectiveTimeout(%q) = %v, want %v", tt.args, got, tt.want)
		}
	}
}

func TestRunEnvironmentAndFlags(t *testing.T) {
	src := testutil.WriteTree(t, map[string]string{"p/testdata/in.txt": "in\n", "shared/x.txt": "x\n"})
	readOnly(t, src)
	bin := script(t, `echo "args: $*"
echo "pwd: $(pwd)"
echo "home: $HOME"
echo "path: $PATH"
echo "scope: $SCOPE"
cat testdata/in.txt ../shared/x.txt
echo new > testdata/new.txt && echo writable
echo to-stderr >&2
`)
	err, log, stderr, work := run(t, Manifest{
		Go: "/nix/store/x-go/bin/go", ImportPath: "example.com/m/p", Bin: bin, SrcDir: src, Subdir: "p",
		Flags: []string{"-v", "-run", "TestA"}, Env: map[string]string{"SCOPE": "package"},
	})
	if err != nil {
		t.Fatalf("Run: %v\n%s", err, log)
	}
	for _, want := range []string{
		"args: -test.paniconexit0 -test.timeout=10m0s -test.v -test.run TestA\n",
		"pwd: " + filepath.Join(work, "src", "p") + "\n",
		"home: " + filepath.Join(work, "home") + "\n",
		"path: /nix/store/x-go/bin" + string(filepath.ListSeparator),
		"scope: package\n",
		"in\nx\n",
		"writable\n",
		"to-stderr\n",
		"ok  \texample.com/m/p\t",
	} {
		if !strings.Contains(log, want) {
			t.Errorf("log lacks %q:\n%s", want, log)
		}
	}
	if stderr != log {
		t.Errorf("stderr differs from the log:\n%s", stderr)
	}
}

// The package at the root of the source runs in the copy's root, and the
// manifest's env wins over what the runner sets.
func TestRunAtTheRootWithOverrides(t *testing.T) {
	src := testutil.WriteTree(t, map[string]string{"testdata/in.txt": "in\n"})
	bin := script(t, "cat testdata/in.txt\necho \"home: $HOME\"\n")
	err, log, _, _ := run(t, Manifest{
		Go: "/go/bin/go", ImportPath: "example.com/m", Bin: bin, SrcDir: src,
		Env: map[string]string{"HOME": "/custom"},
	})
	if err != nil || !strings.Contains(log, "in\nhome: /custom\n") {
		t.Fatalf("Run: %v\n%s", err, log)
	}
}

func TestRunFailingBinary(t *testing.T) {
	src := testutil.WriteTree(t, map[string]string{"p/p.go": ""})
	err, log, _, _ := run(t, Manifest{Go: "/go/bin/go", ImportPath: "example.com/m/p", Bin: script(t, "echo boom\nexit 3\n"), SrcDir: src, Subdir: "p"})
	if !errors.Is(err, ErrFailed) {
		t.Fatalf("err = %v, want ErrFailed", err)
	}
	// As under go test, the process's end comes before the summary: the only
	// clue when a binary dies by a signal before it prints anything.
	if !strings.Contains(log, "boom\nexit status 3\nFAIL\texample.com/m/p\t") {
		t.Errorf("log:\n%s", log)
	}
}

func TestRunMissingBinary(t *testing.T) {
	src := testutil.WriteTree(t, map[string]string{"p/p.go": ""})
	err, log, _, _ := run(t, Manifest{Go: "/go/bin/go", ImportPath: "example.com/m/p", Bin: "/nonexistent/p.test", SrcDir: src, Subdir: "p"})
	if !errors.Is(err, ErrFailed) || !strings.Contains(log, "FAIL\texample.com/m/p\t") {
		t.Fatalf("err = %v, log:\n%s", err, log)
	}
}

// A binary still running a grace period after its -test.timeout is killed.
func TestRunKillsAHungBinary(t *testing.T) {
	defer func(grace time.Duration) { killGrace = grace }(killGrace)
	killGrace = 100 * time.Millisecond
	src := testutil.WriteTree(t, map[string]string{"p/p.go": ""})
	start := time.Now()
	err, log, _, _ := run(t, Manifest{
		Go: "/go/bin/go", ImportPath: "example.com/m/p", Bin: script(t, "exec sleep 30\n"), SrcDir: src, Subdir: "p",
		Flags: []string{"-timeout=200ms"},
	})
	if !errors.Is(err, ErrFailed) || !strings.Contains(log, "FAIL\texample.com/m/p\t") {
		t.Fatalf("err = %v, log:\n%s", err, log)
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Errorf("Run took %v; the binary was not killed", elapsed)
	}
}

func TestCopyTree(t *testing.T) {
	src := testutil.WriteTree(t, map[string]string{"a/file.txt": "x", "a/tool.sh": "#!/bin/sh\n"})
	if err := os.Chmod(filepath.Join(src, "a", "tool.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("file.txt", filepath.Join(src, "a", "link")); err != nil {
		t.Fatal(err)
	}
	readOnly(t, src)
	dst := filepath.Join(t.TempDir(), "copy")
	if err := copyTree(src, dst); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dst, "a", "file.txt"), []byte("y"), 0o644); err != nil {
		t.Errorf("the copy is not writable: %v", err)
	}
	if info, err := os.Stat(filepath.Join(dst, "a", "tool.sh")); err != nil || info.Mode()&0o100 == 0 {
		t.Errorf("tool.sh lost its execute bit: %v %v", info, err)
	}
	if target, err := os.Readlink(filepath.Join(dst, "a", "link")); err != nil || target != "file.txt" {
		t.Errorf("link = %q, %v", target, err)
	}
}
