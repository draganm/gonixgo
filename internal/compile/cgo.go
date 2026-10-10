package compile

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/draganm/gonixgo/internal/cc"
	"github.com/draganm/gonixgo/internal/gotool"
)

// Cgo is the C side of a cgo package: the cgo attribute of its compile
// node. The flags are the package's #cgo directives with ${SRCDIR} not yet
// expanded.
type Cgo struct {
	PkgName   string   `json:"pkgName"`   // Go package name
	CgoFiles  []string `json:"cgoFiles"`  // Go files that import "C"
	CFiles    []string `json:"cFiles"`    //
	CXXFiles  []string `json:"cxxFiles"`  //
	MFiles    []string `json:"mFiles"`    // Objective-C
	CPPFLAGS  []string `json:"cppflags"`  //
	CFLAGS    []string `json:"cflags"`    //
	CXXFLAGS  []string `json:"cxxflags"`  //
	LDFLAGS   []string `json:"ldflags"`   //
	PkgConfig []string `json:"pkgConfig"` // arguments of the #cgo pkg-config directives
}

// cgoDefaultFlags is what cmd/go uses for $CGO_CFLAGS, $CGO_CXXFLAGS and
// $CGO_LDFLAGS when they are not set.
const cgoDefaultFlags = "-O2 -g"

// runCgo performs the C side of a cgo package in workDir, the way cmd/go
// does: cgo translates the files that import "C", the C compiler compiles
// what cgo generated and the package's own C, C++, Objective-C and
// assembly files, and a trial link tells cgo what the package imports
// dynamically. It returns the Go files to compile with the package's own
// and the members to append to its archive.
func runCgo(tc *gotool.Toolchain, m Manifest, workDir string) (goFiles, members []string, err error) {
	c := m.Cgo
	for _, file := range m.SFiles {
		if err := rejectGoAssembly(m.SrcDir, file); err != nil {
			return nil, nil, err
		}
	}
	// A missing header or library is the usual failure of every step
	// below, and the way out is not obvious from a compiler's message.
	hint := func(err error) error {
		return fmt.Errorf("%w\nif a header or library is missing, add it to packageOverrides.%q.buildInputs", err, m.ImportPath)
	}
	// resolve put ${SRCDIR} back where go list had expanded it to a
	// directory that does not exist here.
	expand := func(flags []string) []string {
		out := make([]string, len(flags))
		for i, flag := range flags {
			out[i] = strings.ReplaceAll(flag, "${SRCDIR}", m.SrcDir)
		}
		return out
	}
	// The compiler wrapper and pkg-config are configured through the
	// derivation's environment.
	env := tc.InheritedEnviron("TERM=dumb")

	var pcCflags, pcLibs []string
	if len(c.PkgConfig) > 0 {
		if pcCflags, pcLibs, err = cc.PkgConfig(env, m.SrcDir, c.PkgConfig); err != nil {
			return nil, nil, fmt.Errorf("%w\npackage %s uses pkg-config (%s): add pkg-config to packageOverrides.%q.nativeBuildInputs and the libraries to its buildInputs",
				err, m.ImportPath, strings.Join(c.PkgConfig, " "), m.ImportPath)
		}
	}

	envCPP, err := envList("CGO_CPPFLAGS", "")
	if err != nil {
		return nil, nil, err
	}
	envC, err := envList("CGO_CFLAGS", cgoDefaultFlags)
	if err != nil {
		return nil, nil, err
	}
	envCXX, err := envList("CGO_CXXFLAGS", cgoDefaultFlags)
	if err != nil {
		return nil, nil, err
	}
	envLD, err := envList("CGO_LDFLAGS", cgoDefaultFlags)
	if err != nil {
		return nil, nil, err
	}
	// The generated _cgo_export.h is in workDir; the package's C files
	// include it.
	objDir := workDir + string(filepath.Separator)
	cppflags := slices.Concat(envCPP, expand(c.CPPFLAGS), pcCflags, []string{"-I", objDir})
	cflags := slices.Concat(cppflags, envC, expand(c.CFLAGS))
	cxxflags := slices.Concat(cppflags, envCXX, expand(c.CXXFLAGS))
	ldflags := slices.Concat(envLD, expand(c.LDFLAGS), pcLibs)
	if len(c.MFiles) > 0 {
		ldflags = append(ldflags, "-lobjc")
	}

	// cgo records the link flags in _cgo_gotypes.go; the compiler puts
	// them into the archive and the Go linker hands them to the C linker.
	cgoArgs := []string{"-objdir", objDir, "-importpath", m.ImportPath}
	if len(ldflags) > 0 {
		quoted := make([]string, len(ldflags))
		for i, flag := range ldflags {
			quoted[i] = strconv.Quote(flag)
		}
		cgoArgs = append(cgoArgs, "-ldflags="+strings.Join(quoted, " "))
	}
	cgoArgs = append(cgoArgs, "--")
	cgoArgs = append(cgoArgs, cflags...)
	cgoArgs = append(cgoArgs, dotSlash(c.CgoFiles)...)
	// The flags are on the command line; cgo must not add them again.
	if err := tc.HostTool(m.SrcDir, []string{"TERM=dumb", "CGO_LDFLAGS="}, "cgo", cgoArgs...); err != nil {
		return nil, nil, hint(err)
	}

	target := cc.Target{GOOS: tc.GOOS, GOARCH: tc.GOARCH, GOMIPS: tc.Env("GOMIPS"), GOMIPS64: tc.Env("GOMIPS64")}
	compiler := func(command string) (*cc.Compiler, error) {
		cmd, err := gotool.SplitFlags([]string{command})
		if err != nil {
			return nil, fmt.Errorf("compiler command: %w", err)
		}
		if len(cmd) == 0 {
			return nil, fmt.Errorf("no C compiler: $CC and $CXX are not set and go env names none")
		}
		return cc.New(cmd, target, env, envC, envLD, workDir), nil
	}
	cCompiler, err := compiler(tc.CC())
	if err != nil {
		return nil, nil, err
	}
	linker := cCompiler
	var cxxCompiler *cc.Compiler
	if len(c.CXXFiles) > 0 {
		if cxxCompiler, err = compiler(tc.CXX()); err != nil {
			return nil, nil, err
		}
		linker = cxxCompiler
	}

	// The compiler must not record where the source is in the store,
	// unless the package is under test and keeps its paths.
	var srcMap cc.PathMap
	if m.TrimTo != "" {
		rootDir, rootName := trimRoot(m.SrcDir, m.TrimTo)
		srcMap = cc.PathMap{From: rootDir, To: "/_/" + rootName}
	}

	// Objects are numbered in cmd/go's order, before any compile starts,
	// so the archive does not depend on which finishes first.
	type job struct {
		compiler *cc.Compiler
		cc.Job
	}
	var jobs []job
	add := func(compiler *cc.Compiler, dir string, flags []string, file string) {
		obj := filepath.Join(workDir, fmt.Sprintf("_x%03d.o", len(jobs)+1))
		jobs = append(jobs, job{compiler, cc.Job{
			Dir: dir, IncDir: m.SrcDir, Flags: flags, File: file, Obj: obj, Seed: seed(m.ImportPath, file),
		}})
		members = append(members, obj)
	}
	add(cCompiler, workDir, cflags, "_cgo_export.c")
	goFiles = []string{filepath.Join(workDir, "_cgo_gotypes.go")}
	for _, file := range c.CgoFiles {
		base := strings.TrimSuffix(filepath.Base(file), ".go")
		add(cCompiler, workDir, cflags, base+".cgo2.c")
		goFiles = append(goFiles, filepath.Join(workDir, base+".cgo1.go"))
	}
	// In a cgo package the assembly files are for the C compiler.
	for _, file := range slices.Concat(m.SFiles, c.CFiles, c.MFiles) {
		add(cCompiler, m.SrcDir, cflags, file)
	}
	for _, file := range c.CXXFiles {
		add(cxxCompiler, m.SrcDir, cxxflags, file)
	}
	errs := make([]error, len(jobs))
	slots := make(chan struct{}, cores())
	var wg sync.WaitGroup
	for i, j := range jobs {
		wg.Add(1)
		slots <- struct{}{}
		go func() {
			defer wg.Done()
			errs[i] = j.compiler.Compile(j.Job, workDir, srcMap)
			<-slots
		}()
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return nil, nil, hint(err)
		}
	}

	// The Go linker, when it links without the C linker, must know which
	// libraries and symbols the C code takes from shared libraries. cgo
	// reads that off a trial link of the objects.
	mainObj := filepath.Join(workDir, "_cgo_main.o")
	mainJob := cc.Job{
		Dir: workDir, IncDir: m.SrcDir, Flags: cflags, File: "_cgo_main.c", Obj: mainObj, Seed: seed(m.ImportPath, "_cgo_main.c"),
	}
	if err := cCompiler.Compile(mainJob, workDir, srcMap); err != nil {
		return nil, nil, hint(err)
	}
	trial := filepath.Join(workDir, "_cgo_.o")
	objs := append([]string{mainObj}, members...)
	if out, err := linker.Link(m.SrcDir, m.SrcDir, workDir, trial, objs, trialLinkFlags(tc.GOOS, tc.GOARCH, ldflags)); err != nil {
		// Not an error: a symbol may resolve only in the final link. The
		// Go linker looks for a member of this name and then leaves the
		// link to the C linker.
		fmt.Fprintf(os.Stderr, "gonixgo: the trial link of %s failed. This is not an error: its binaries will be linked by the C linker. The linker said:\n%s\n", m.ImportPath, bytes.TrimSpace(out))
		fail := filepath.Join(workDir, "dynimportfail")
		if err := os.WriteFile(fail, nil, 0o644); err != nil {
			return nil, nil, err
		}
		return goFiles, append(members, fail), nil
	}
	importGo := filepath.Join(workDir, "_cgo_import.go")
	if err := tc.HostTool(workDir, []string{"TERM=dumb"}, "cgo", "-dynpackage", c.PkgName, "-dynimport", trial, "-dynout", importGo); err != nil {
		return nil, nil, err
	}
	return append(goFiles, importGo), members, nil
}

// envList splits the value of an environment variable into flags, or def
// when it is not set.
func envList(key, def string) ([]string, error) {
	value := os.Getenv(key)
	if value == "" {
		value = def
	}
	flags, err := gotool.SplitFlags([]string{value})
	if err != nil {
		return nil, fmt.Errorf("$%s: %w", key, err)
	}
	return flags, nil
}

// seed is the -frandom-seed of one compile: the same for the same file of
// the same package, wherever it is built.
func seed(importPath, file string) string {
	sum := sha256.Sum256([]byte(importPath + "\x00" + file))
	return hex.EncodeToString(sum[:10])
}

// rejectGoAssembly reports a file in Go assembler syntax in a cgo package,
// where assembly goes to the C compiler. cmd/go looks for the same words.
func rejectGoAssembly(srcDir, file string) error {
	data, err := os.ReadFile(filepath.Join(srcDir, file))
	if err != nil {
		return err
	}
	for _, word := range []string{"TEXT", "DATA", "GLOBL"} {
		if bytes.HasPrefix(data, []byte(word)) || bytes.Contains(data, []byte("\n"+word)) {
			return fmt.Errorf("package using cgo has Go assembly file %s", file)
		}
	}
	return nil
}

// trimRoot returns the directory the C compiler's path rewriting replaces
// and the name it gets: srcDir and trimTo without the path elements they
// share at their ends. cmd/go rewrites the module directory to the
// module's name; for internal/cadd of example.com/app in <store> that is
// <store> to example.com/app, which also covers a header in a sibling
// directory.
func trimRoot(srcDir, trimTo string) (dir, name string) {
	dir, name = filepath.Clean(srcDir), trimTo
	for {
		i := strings.LastIndex(name, "/")
		if i < 0 || filepath.Base(dir) != name[i+1:] {
			return dir, name
		}
		dir, name = filepath.Dir(dir), name[:i]
	}
}

// trialLinkFlags adjusts the link flags for the trial link as cmd/go does:
// linux/arm and android need a position-independent executable to report
// imported symbols accurately, which excludes a static one.
func trialLinkFlags(goos, goarch string, ldflags []string) []string {
	if !(goos == "linux" && goarch == "arm") && goos != "android" {
		return ldflags
	}
	if slices.Contains(ldflags, "-no-pie") {
		return ldflags
	}
	return append(slices.DeleteFunc(slices.Clone(ldflags), func(flag string) bool { return flag == "-static" }), "-pie")
}
