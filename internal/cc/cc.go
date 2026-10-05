// Package cc drives a C or C++ compiler the way cmd/go does for the C side
// of a cgo package.
package cc

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
)

// Target is what the compiler's flags depend on.
type Target struct {
	GOOS, GOARCH     string
	GOMIPS, GOMIPS64 string
}

// Compiler is one compiler command aimed at one target.
type Compiler struct {
	cmd      []string
	target   Target
	env      []string
	cflags   []string
	ldflags  []string
	probeDir string

	mu        sync.Mutex
	supported map[string]bool
}

// New prepares the compiler whose command line starts with cmd: $CC or
// $CXX, split into fields. env is the environment of every run. cflags and
// ldflags are $CGO_CFLAGS and $CGO_LDFLAGS, which the flag probes pass
// along as cmd/go's do. probeDir is a directory the probes may run in.
func New(cmd []string, t Target, env []string, cflags, ldflags []string, probeDir string) *Compiler {
	return &Compiler{
		cmd: cmd, target: t, env: env, cflags: cflags, ldflags: ldflags, probeDir: probeDir,
		supported: map[string]bool{},
	}
}

// unsupportedWords are how compilers say they do not know an option: GCC
// "unrecognized command line option", clang "unknown argument", and so on.
var unsupportedWords = []string{
	"unrecognized", "unknown", "unrecognised", "is not supported", "not recognized", "unsupported",
}

// Supports reports whether the compiler accepts flag. It asks the
// compiler once per flag, by compiling an empty file from standard input,
// or linking one when the flag is for the linker.
func (c *Compiler) Supports(flag string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if ok, known := c.supported[flag]; known {
		return ok
	}
	args := append(slices.Clone(c.cmd[1:]), flag)
	if strings.HasPrefix(flag, "-Wl,") {
		args = append(args, c.ldflags...)
	} else {
		args = append(args, c.cflags...)
		args = append(args, "-c")
	}
	args = append(args, "-x", "c", "-", "-o", os.DevNull)
	cmd := exec.Command(c.cmd[0], args...)
	cmd.Dir = c.probeDir
	// The words looked for are English.
	cmd.Env = append(slices.Clone(c.env), "LC_ALL=C")
	out, _ := cmd.CombinedOutput()
	ok := true
	for _, word := range unsupportedWords {
		if bytes.Contains(out, []byte(word)) {
			ok = false
		}
	}
	c.supported[flag] = ok
	return ok
}

// Prefix is how every compile and link of a package starts: the compiler,
// -I incDir, and the flags that do not depend on the file. Paths under
// workDir are recorded as under /tmp/go-build.
func (c *Compiler) Prefix(incDir, workDir string) []string {
	a := append(slices.Clone(c.cmd), "-I", incDir)
	// gcc on Windows complains that all its code is position independent.
	if c.target.GOOS != "windows" {
		a = append(a, "-fPIC")
	}
	a = append(a, ArchArgs(c.target)...)
	if c.target.GOOS == "windows" {
		a = append(a, "-mthreads")
	} else {
		a = append(a, "-pthread")
	}
	if c.target.GOOS == "aix" {
		a = append(a, "-mcmodel=large")
	}
	for _, flag := range []string{
		"-fno-caret-diagnostics", // no ASCII art in clang's errors
		"-Qunused-arguments",     // clang is strict about unused arguments
		"-Wl,--no-gc-sections",   // zig cc collects sections even without linking
	} {
		if c.Supports(flag) {
			a = append(a, flag)
		}
	}
	a = append(a, "-fmessage-length=0")
	if flag := c.prefixMap(PathMap{From: strings.TrimSuffix(workDir, string(filepath.Separator)), To: "/tmp/go-build"}); flag != "" {
		a = append(a, flag)
	}
	// Recorded flags would bring back the paths the map just removed.
	if c.Supports("-gno-record-gcc-switches") {
		a = append(a, "-gno-record-gcc-switches")
	}
	// The Go linker's Mach-O support assumes no common symbols.
	if c.target.GOOS == "darwin" || c.target.GOOS == "ios" {
		a = append(a, "-fno-common")
	}
	return a
}

// PathMap rewrites the directory From to To in the paths a compiler
// records in an object: debug information and __FILE__. The zero value
// rewrites nothing.
type PathMap struct {
	From, To string
}

// prefixMap returns the flag for m, "" when the compiler has none.
func (c *Compiler) prefixMap(m PathMap) string {
	switch {
	case m == PathMap{} || !c.Supports("-fdebug-prefix-map=a=b"):
		return ""
	case c.Supports("-ffile-prefix-map=a=b"):
		return "-ffile-prefix-map=" + m.From + "=" + m.To
	}
	return "-fdebug-prefix-map=" + m.From + "=" + m.To
}

// Job is one compile.
type Job struct {
	Dir    string   // working directory
	IncDir string   // the -I directory: "." for a file in Dir, the package directory for a generated file
	Flags  []string // preprocessor flags, then compiler flags
	File   string   // source file, relative to Dir or absolute
	Obj    string   // object file to write
	Seed   string   // what the compiler derives its "random" names from
}

// Compile runs one job. src keeps the source root out of the object. The
// compiler's output goes to stderr.
func (c *Compiler) Compile(j Job, workDir string, src PathMap) error {
	args := append(c.Prefix(j.IncDir, workDir), j.Flags...)
	if flag := c.prefixMap(src); flag != "" {
		args = append(args, flag)
	}
	// A fixed seed keeps the symbols the compiler invents, under LTO for
	// one, the same from build to build.
	if c.Supports("-frandom-seed=1") {
		args = append(args, "-frandom-seed="+j.Seed)
	}
	args = append(args, "-o", j.Obj, "-c", j.File)
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = j.Dir
	cmd.Env = c.env
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %s: %w", filepath.Base(c.cmd[0]), j.File, err)
	}
	return nil
}

// Link links objs into out with ldflags and returns the compiler's output.
// cgo's trial link is expected to fail for some packages, so the caller
// decides what a failure means and whether to show the output.
func (c *Compiler) Link(dir, incDir, workDir, out string, objs, ldflags []string) ([]byte, error) {
	args := append(c.Prefix(incDir, workDir), "-o", out)
	args = append(args, objs...)
	args = append(args, ldflags...)
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = dir
	cmd.Env = c.env
	return cmd.CombinedOutput()
}

// ArchArgs returns the flags that select the target architecture.
func ArchArgs(t Target) []string {
	switch t.GOARCH {
	case "386":
		return []string{"-m32"}
	case "amd64":
		if t.GOOS == "darwin" {
			return []string{"-arch", "x86_64", "-m64"}
		}
		return []string{"-m64"}
	case "arm64":
		if t.GOOS == "darwin" {
			return []string{"-arch", "arm64"}
		}
	case "arm":
		return []string{"-marm"} // not thumb
	case "s390x":
		return []string{"-m64", "-march=z13"}
	case "mips64", "mips64le":
		return append([]string{"-mabi=64"}, mipsFloat(t.GOMIPS64)...)
	case "mips", "mipsle":
		args := []string{"-mabi=32", "-march=mips32"}
		if t.GOMIPS == "hardfloat" {
			return append(args, "-mhard-float", "-mfp32", "-mno-odd-spreg")
		}
		return append(args, mipsFloat(t.GOMIPS)...)
	case "loong64":
		return []string{"-mabi=lp64d"}
	case "ppc64":
		if t.GOOS == "aix" {
			return []string{"-maix64"}
		}
	}
	return nil
}

func mipsFloat(mode string) []string {
	switch mode {
	case "hardfloat":
		return []string{"-mhard-float"}
	case "softfloat":
		return []string{"-msoft-float"}
	}
	return nil
}
