// Package resolve is the evaluation-time pipeline: go list, graph, module
// hashes, Nix.
package resolve

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"

	"github.com/draganm/gonixgo/internal/emit"
	"github.com/draganm/gonixgo/internal/golist"
	"github.com/draganm/gonixgo/internal/graph"
	"github.com/draganm/gonixgo/internal/modcache"
)

// Args is the JSON argument buildGoApplication passes.
type Args struct {
	Go          string   `json:"go"`
	Src         string   `json:"src"`
	StoreDir    string   `json:"storeDir"`
	ModRoot     string   `json:"modRoot"`
	SubPackages []string `json:"subPackages"`
	Tags        []string `json:"tags"`
	GOOS        string   `json:"goos"`
	GOARCH      string   `json:"goarch"`
	GOARM       string   `json:"goarm"`      // "" for Go's default
	Cross       bool     `json:"cross"`      // build and host platforms differ: cgo is off unless asked for
	CgoEnabled  *bool    `json:"cgoEnabled"` // nil: Go's default for the target
	DoCheck     bool     `json:"doCheck"`    // run the -test pass and add the tests
}

// Options are the parts of the environment Run depends on.
type Options struct {
	Stderr   io.Writer
	CacheDir string                                 // module hash cache, "" for none
	Add      func(name, dir string) (string, error) // store pre-seeding, nil for none
}

// envKeys are the go env values the graph and the module info need.
var envKeys = []string{
	"GOVERSION", "GOOS", "GOARCH", "CGO_ENABLED",
	"GO386", "GOAMD64", "GOARM", "GOARM64", "GOMIPS", "GOMIPS64", "GOPPC64", "GORISCV64", "GOWASM",
}

// Run resolves the package graph of a.Src and writes it to stdout as Nix.
// Nothing is written when it fails.
func Run(a Args, opts Options, stdout io.Writer) error {
	// go reports directories with symlinks resolved; compare like with like.
	src, err := filepath.EvalSymlinks(a.Src)
	if err != nil {
		return err
	}
	dir, err := moduleDir(src, a.ModRoot)
	if err != nil {
		return err
	}
	moduleEnv, err := golist.ModuleEnv(a.Go, dir, opts.Stderr)
	if err != nil {
		return err
	}
	o := golist.Options{
		Go:        a.Go,
		Dir:       dir,
		GOOS:      a.GOOS,
		GOARCH:    a.GOARCH,
		GOARM:     a.GOARM,
		Tags:      a.Tags,
		ModuleEnv: moduleEnv,
		Stderr:    opts.Stderr,
	}
	// nixpkgs' Go turns cgo on for every target, so a cross build would
	// need a C toolchain for the target even for pure Go. Unless asked
	// for, cgo is off there.
	crossCgoOff := a.Cross && a.CgoEnabled == nil
	switch {
	case a.CgoEnabled != nil:
		o.CgoEnabled = "0"
		if *a.CgoEnabled {
			o.CgoEnabled = "1"
		}
	case crossCgoOff:
		o.CgoEnabled = "0"
	}

	// go list needs a build cache, where go list -test also writes the test
	// mains. With the caller's off, a temporary one serves this run.
	cache, err := golist.Env(o, "GOCACHE")
	if err != nil {
		return err
	}
	if cache["GOCACHE"] == "off" {
		dir, err := os.MkdirTemp("", "gonixgo-gocache-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(dir)
		o.GOCACHE = dir
	}

	env, err := golist.Env(o, envKeys...)
	if err != nil {
		return err
	}
	pkgs, err := golist.List(o, patterns(a.SubPackages)...)
	if err != nil {
		return err
	}
	sums := map[string]string{}
	if data, err := os.ReadFile(filepath.Join(o.Dir, "go.sum")); err == nil {
		sums = modcache.ParseGoSum(data)
	}

	g, err := graph.Build(graph.Input{Packages: pkgs, Src: src, Env: env, Tags: a.Tags, Sums: sums})
	if err != nil {
		var loadErr *graph.LoadError
		if crossCgoOff && errors.As(err, &loadErr) {
			loadErr.CrossCgoOff = true
		}
		return err
	}
	if a.DoCheck && len(g.Tested) > 0 {
		if err := addTests(g, o, graph.Input{Src: src, Env: env, Tags: a.Tags, Sums: sums}); err != nil {
			return err
		}
	}

	seeder := modcache.Seeder{
		StoreDir: a.StoreDir,
		CacheDir: opts.CacheDir,
		Add:      opts.Add,
		Warn: func(format string, args ...any) {
			fmt.Fprintf(opts.Stderr, format+"\n", args...)
		},
	}
	if err := seeder.Prepare(g.ModuleList()); err != nil {
		return err
	}
	return emit.Nix(stdout, g)
}

// moduleDir is the directory of go.mod: modRoot, which must stay inside
// src. "" means src itself.
func moduleDir(src, modRoot string) (string, error) {
	rel := filepath.FromSlash(modRoot)
	if rel == "" {
		rel = "."
	}
	if !filepath.IsLocal(rel) {
		return "", fmt.Errorf("modRoot %q must be a directory inside src", modRoot)
	}
	dir := filepath.Join(src, rel)
	if _, err := os.Stat(filepath.Join(dir, "go.mod")); err != nil {
		return "", fmt.Errorf("modRoot %q: no go.mod in %s", modRoot, dir)
	}
	return dir, nil
}

// addTests runs the -test pass over the tested packages of g and adds
// their tests to it.
func addTests(g *graph.Graph, o golist.Options, in graph.Input) error {
	var err error
	if in.Packages, err = golist.ListTests(o, g.Tested...); err != nil {
		return err
	}
	return g.AddTests(in)
}

// patterns turns subPackages into go list patterns relative to the module
// root: "." stays, anything else gets a "./" prefix.
func patterns(subPackages []string) []string {
	if len(subPackages) == 0 {
		return []string{"."}
	}
	out := make([]string, len(subPackages))
	for i, sp := range subPackages {
		sp = path.Clean(sp)
		if sp != "." {
			sp = "./" + sp
		}
		out[i] = sp
	}
	return out
}
