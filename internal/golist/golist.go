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

	EmbedPatterns []string
	EmbedFiles    []string

	Imports   []string
	ImportMap map[string]string

	Error *PackageError
}

// Options says how to run the go command.
type Options struct {
	Go         string   // path to the go binary
	Dir        string   // working directory, the module root
	GOOS       string   // "" keeps the environment's
	GOARCH     string   // "" keeps the environment's
	CgoEnabled string   // "0", "1", or "" for Go's default
	Tags       []string // build tags
	Stderr     io.Writer
}

// environ is the caller's environment with the variables that decide the
// package graph pinned. GOPROXY, GOPRIVATE, GOMODCACHE, NETRC and the Go
// env file are left alone so private modules resolve as they do for the
// user.
func (o Options) environ() []string {
	set := map[string]string{
		"GOFLAGS":     "-mod=readonly",
		"GOWORK":      "off",
		"GOTOOLCHAIN": "local",
		// os/exec only sets PWD when Env is nil. go derives package
		// directories from it, so it must name Dir.
		"PWD": o.Dir,
	}
	if o.GOOS != "" {
		set["GOOS"] = o.GOOS
	}
	if o.GOARCH != "" {
		set["GOARCH"] = o.GOARCH
	}
	if o.CgoEnabled != "" {
		set["CGO_ENABLED"] = o.CgoEnabled
	}
	var env []string
	for _, kv := range os.Environ() {
		key, _, _ := strings.Cut(kv, "=")
		if _, pinned := set[key]; !pinned {
			env = append(env, kv)
		}
	}
	for k, v := range set {
		env = append(env, k+"="+v)
	}
	return env
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
	args := []string{"list", "-e", "-deps", "-json"}
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
	out, err := o.command(append([]string{"env", "-json"}, keys...)...).Output()
	if err != nil {
		return nil, fmt.Errorf("go env: %w", err)
	}
	env := map[string]string{}
	if err := json.Unmarshal(out, &env); err != nil {
		return nil, fmt.Errorf("decoding go env output: %w", err)
	}
	return env, nil
}
