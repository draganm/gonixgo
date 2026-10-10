// Package golist runs `go list` and `go env` and decodes their output.
package golist

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// Module is the part of go list's Module that gonixgo uses.
type Module struct {
	Path      string
	Version   string
	Main      bool
	Dir       string
	GoVersion string
	Replace   *Module
}

// PackageError is a package load error reported by go list -e.
type PackageError struct {
	Pos string
	Err string
}

// Package is the part of go list's Package that gonixgo uses.
type Package struct {
	Dir            string
	ImportPath     string
	Name           string
	Standard       bool
	DepOnly        bool
	ForTest        string // on the copies -test makes, "X [P.test]": P
	Module         *Module
	DefaultGODEBUG string

	GoFiles      []string
	CgoFiles     []string
	CFiles       []string
	CXXFiles     []string
	MFiles       []string
	HFiles       []string
	FFiles       []string
	SFiles       []string
	SwigFiles    []string
	SwigCXXFiles []string
	SysoFiles    []string

	// The flags of the package's #cgo directives, ${SRCDIR} expanded to Dir.
	CgoCFLAGS    []string
	CgoCPPFLAGS  []string
	CgoCXXFLAGS  []string
	CgoLDFLAGS   []string
	CgoPkgConfig []string

	EmbedPatterns []string
	EmbedFiles    []string

	// The test files and their embeds, which go list reports with or
	// without -test.
	TestGoFiles        []string
	XTestGoFiles       []string
	TestEmbedPatterns  []string
	XTestEmbedPatterns []string

	Imports   []string
	ImportMap map[string]string

	Error *PackageError
}

// Options says how to run the go command.
type Options struct {
	Go         string            // path to the go binary
	Dir        string            // working directory, the module root
	GOOS       string            // "" for the host's
	GOARCH     string            // "" for the host's
	GOARM      string            // "" for Go's default
	CgoEnabled string            // "0", "1", or "" for Go's default
	Tags       []string          // build tags
	ModuleEnv  map[string]string // where modules come from, see ModuleEnv
	GOCACHE    string            // build cache, "" for the caller's
	Stderr     io.Writer
}

// buildConfig are the variables that change what gets built. The builders
// run with GOENV=off and none of them set, so go list must not see the
// caller's values either, or the graph and the module info would describe
// a build that does not happen.
var buildConfig = []string{
	"GOOS", "GOARCH", "CGO_ENABLED",
	"GO386", "GOAMD64", "GOARM", "GOARM64", "GOMIPS", "GOMIPS64", "GOPPC64", "GORISCV64", "GOWASM",
	"GOEXPERIMENT", "GOFIPS140", "GO111MODULE", "GOROOT",
}

// moduleKeys are the settings that say where modules come from.
var moduleKeys = []string{
	"GOPROXY", "GOPRIVATE", "GONOPROXY", "GONOSUMDB", "GOSUMDB", "GOINSECURE", "GOVCS",
	"GOMODCACHE", "GOPATH", "GOAUTH",
}

// environ is the environment for the go invocations that decide the
// package graph. It is hermetic in the build configuration: the Go env file
// is off (an empty variable would not override it) and the caller's
// buildConfig values are dropped, so the same source resolves to the same
// graph in every shell. Where modules come from is carried over explicitly
// through ModuleEnv, so private modules resolve as they do for the user.
// NETRC, the HTTP proxy variables, HOME, PATH and SSH_AUTH_SOCK stay
// inherited because downloads need them and they do not change the graph.
func (o Options) environ() []string {
	return o.environWith(map[string]string{"GOENV": "off"})
}

// environWith is environ without GOENV=off, plus extra.
func (o Options) environWith(extra map[string]string) []string {
	set := map[string]string{
		"GOFLAGS":     "-mod=readonly",
		"GOWORK":      "off",
		"GOTOOLCHAIN": "local",
		// os/exec only sets PWD when Env is nil. go derives package
		// directories from it, so it must name Dir.
		"PWD": o.Dir,
	}
	for k, v := range extra {
		set[k] = v
	}
	if o.GOCACHE != "" {
		set["GOCACHE"] = o.GOCACHE
	}
	for k, v := range o.ModuleEnv {
		set[k] = v
	}
	if o.GOOS != "" {
		set["GOOS"] = o.GOOS
	}
	if o.GOARCH != "" {
		set["GOARCH"] = o.GOARCH
	}
	if o.GOARM != "" {
		set["GOARM"] = o.GOARM
	}
	if o.CgoEnabled != "" {
		set["CGO_ENABLED"] = o.CgoEnabled
	}
	drop := map[string]bool{}
	for _, k := range buildConfig {
		drop[k] = true
	}
	var env []string
	for _, kv := range os.Environ() {
		key, _, _ := strings.Cut(kv, "=")
		if _, pinned := set[key]; !pinned && !drop[key] {
			env = append(env, kv)
		}
	}
	for k, v := range set {
		env = append(env, k+"="+v)
	}
	return env
}

// ModuleEnv reports where modules come from for the caller, in dir: the
// shell's values and the Go env file's both count. Pass the result as
// Options.ModuleEnv. The caller's build configuration is still dropped,
// because go refuses to run at all with, say, an unknown GOEXPERIMENT.
func ModuleEnv(goBin, dir string, stderr io.Writer) (map[string]string, error) {
	o := Options{Go: goBin, Dir: dir, Stderr: stderr}
	cmd := o.command(append([]string{"env", "-json"}, moduleKeys...)...)
	cmd.Env = o.environWith(nil)
	return decodeEnv(cmd)
}

func (o Options) command(args ...string) *exec.Cmd {
	cmd := exec.Command(o.Go, args...)
	cmd.Dir = o.Dir
	cmd.Env = o.environ()
	cmd.Stderr = o.Stderr
	if cmd.Stderr == nil {
		cmd.Stderr = os.Stderr
	}
	return cmd
}

// List runs `go list -e -deps -json` for patterns. Packages that fail to
// load are returned with Error set; List itself fails only when the go
// command does.
func List(o Options, patterns ...string) ([]Package, error) {
	return list(o, nil, patterns)
}

// ListTests is List with -test for the packages at importPaths. The output
// adds each package's test copies "X [P.test]" and its test main P.test,
// whose one file is the source go generated, in the build cache.
func ListTests(o Options, importPaths ...string) ([]Package, error) {
	return list(o, []string{"-test"}, importPaths)
}

func list(o Options, flags, patterns []string) ([]Package, error) {
	args := append([]string{"list", "-e", "-deps"}, flags...)
	args = append(args, "-json")
	if len(o.Tags) > 0 {
		args = append(args, "-tags", strings.Join(o.Tags, ","))
	}
	args = append(args, "--")
	args = append(args, patterns...)
	out, err := o.command(args...).Output()
	if err != nil {
		return nil, fmt.Errorf("go list: %w", err)
	}
	return Decode(bytes.NewReader(out))
}

// Decode reads the concatenated JSON objects go list -json prints.
func Decode(r io.Reader) ([]Package, error) {
	var pkgs []Package
	dec := json.NewDecoder(r)
	for {
		var p Package
		err := dec.Decode(&p)
		if err == io.EOF {
			return pkgs, nil
		}
		if err != nil {
			return nil, fmt.Errorf("decoding go list output: %w", err)
		}
		pkgs = append(pkgs, p)
	}
}

// Env runs `go env -json` for keys.
func Env(o Options, keys ...string) (map[string]string, error) {
	return decodeEnv(o.command(append([]string{"env", "-json"}, keys...)...))
}

func decodeEnv(cmd *exec.Cmd) (map[string]string, error) {
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go env: %w", err)
	}
	env := map[string]string{}
	if err := json.Unmarshal(out, &env); err != nil {
		return nil, fmt.Errorf("decoding go env output: %w", err)
	}
	return env, nil
}
