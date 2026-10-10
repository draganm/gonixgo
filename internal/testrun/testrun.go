// Package testrun runs one package's test binary as go test runs it: in
// the package's directory, here in a writable copy of the package's test
// source, with go test's default flags and its summary line.
package testrun

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Manifest describes one test run. The runTest builder in
// nix/builders.nix produces it.
type Manifest struct {
	Go         string            `json:"go"`         // path to the go binary; its directory goes first on PATH
	ImportPath string            `json:"importPath"` // the tested package
	Bin        string            `json:"bin"`        // the test binary
	SrcDir     string            `json:"srcDir"`     // the test source
	Subdir     string            `json:"subdir"`     // the package's directory in it, "" for the root
	Flags      []string          `json:"flags"`      // checkFlags, spelt as for go test
	Env        map[string]string `json:"env"`        // checkEnv
}

// ErrFailed is returned when the test binary fails, is killed or does not
// start. The log says which.
var ErrFailed = errors.New("tests failed")

// defaultFlags are what go test passes before the user's flags.
var defaultFlags = []string{"-test.paniconexit0", "-test.timeout=10m0s"}

// killGrace is how long past its -test.timeout a binary may run before it
// is killed. The binary panics at the timeout itself; this catches one
// that cannot.
var killGrace = time.Minute

// passFlagToTest are the flags go test hands to the test binary with the
// test. prefix, and whether each takes a value
// (cmd/go/internal/test/flagdefs.go and testflag.go).
var passFlagToTest = map[string]bool{
	"artifacts": false, "bench": true, "benchmem": false, "benchtime": true,
	"blockprofile": true, "blockprofilerate": true, "count": true, "coverprofile": true,
	"cpu": true, "cpuprofile": true, "failfast": false, "fullpath": false,
	"fuzz": true, "fuzzminimizetime": true, "fuzztime": true, "list": true,
	"memprofile": true, "memprofilerate": true, "mutexprofile": true, "mutexprofilefraction": true,
	"outputdir": true, "parallel": true, "run": true, "short": false,
	"shuffle": true, "skip": true, "timeout": true, "trace": true, "v": false,
}

// Run copies the test source into workDir and runs the test binary in the
// package's directory there. The binary's output goes to the file at
// logPath and to stderr, followed by go test's summary line.
func Run(m Manifest, logPath, workDir string, stderr io.Writer) error {
	tree := filepath.Join(workDir, "src")
	if err := copyTree(m.SrcDir, tree); err != nil {
		return err
	}
	dir := filepath.Join(tree, filepath.FromSlash(m.Subdir))
	home := filepath.Join(workDir, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		return err
	}
	logFile, err := os.Create(logPath)
	if err != nil {
		return err
	}
	defer logFile.Close()
	out := io.MultiWriter(logFile, stderr)

	args := append(slices.Clone(defaultFlags), translateFlags(m.Flags)...)
	ctx := context.Background()
	if timeout := effectiveTimeout(args); timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout+killGrace)
		defer cancel()
	}
	path := filepath.Dir(m.Go)
	if p := os.Getenv("PATH"); p != "" {
		path += string(filepath.ListSeparator) + p
	}
	cmd := exec.CommandContext(ctx, m.Bin, args...)
	cmd.Dir = dir
	cmd.Env = environ(os.Environ(), map[string]string{"PATH": path, "HOME": home, "PWD": dir}, m.Env)
	// One writer for both keeps their output in order.
	cmd.Stdout, cmd.Stderr = out, out
	// A process the test leaves behind must not hold the run open.
	cmd.WaitDelay = 5 * time.Second

	start := time.Now()
	runErr := cmd.Run()
	elapsed := fmt.Sprintf("%.3fs", time.Since(start).Seconds())
	if runErr != nil {
		switch {
		case ctx.Err() != nil:
			fmt.Fprintf(out, "killed: still running %v after -test.timeout\n", killGrace)
		case !errors.As(runErr, new(*exec.ExitError)):
			fmt.Fprintln(out, runErr)
		}
		fmt.Fprintf(out, "FAIL\t%s\t%s\n", m.ImportPath, elapsed)
		return fmt.Errorf("%s: %w", m.ImportPath, ErrFailed)
	}
	fmt.Fprintf(out, "ok  \t%s\t%s\n", m.ImportPath, elapsed)
	return logFile.Close()
}

// translateFlags turns flags spelt for go test into the test binary's.
func translateFlags(flags []string) []string {
	out := []string{}
	for i := 0; i < len(flags); i++ {
		arg := flags[i]
		switch arg {
		case "-args", "--args":
			// The rest goes to the test as written.
			return append(out, flags[i+1:]...)
		case "--":
			return append(out, flags[i:]...)
		}
		name, value, hasValue := strings.Cut(strings.TrimPrefix(strings.TrimPrefix(arg, "-"), "-"), "=")
		takesValue, known := passFlagToTest[name]
		switch {
		case !strings.HasPrefix(arg, "-") || !known:
			out = append(out, arg)
		case hasValue:
			out = append(out, "-test."+name+"="+value)
		default:
			out = append(out, "-test."+name)
			if takesValue && i+1 < len(flags) {
				i++
				out = append(out, flags[i])
			}
		}
	}
	return out
}

// effectiveTimeout returns the value of the last -test.timeout in args,
// before any --.
func effectiveTimeout(args []string) time.Duration {
	var timeout time.Duration
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			break
		}
		name, value, hasValue := strings.Cut(strings.TrimPrefix(strings.TrimPrefix(arg, "-"), "-"), "=")
		if !strings.HasPrefix(arg, "-") || name != "test.timeout" {
			continue
		}
		if !hasValue {
			if i+1 == len(args) {
				break
			}
			i++
			value = args[i]
		}
		if d, err := time.ParseDuration(value); err == nil {
			timeout = d
		}
	}
	return timeout
}

// environ returns base with the variables of each layer set over it,
// later layers winning.
func environ(base []string, layers ...map[string]string) []string {
	set := map[string]string{}
	for _, layer := range layers {
		maps.Copy(set, layer)
	}
	env := make([]string, 0, len(base)+len(set))
	for _, kv := range base {
		key, _, _ := strings.Cut(kv, "=")
		if _, ok := set[key]; !ok {
			env = append(env, kv)
		}
	}
	for _, key := range slices.Sorted(maps.Keys(set)) {
		env = append(env, key+"="+set[key])
	}
	return env
}

// copyTree copies the tree at src to dst, keeping symlinks and execute
// bits and making everything writable: src is in the store, where nothing
// is.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.IsDir():
			return os.MkdirAll(target, info.Mode().Perm()|0o700)
		case d.Type()&fs.ModeSymlink != 0:
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		default:
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			return os.WriteFile(target, data, info.Mode().Perm()|0o200)
		}
	})
}
