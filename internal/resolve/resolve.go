// Package resolve is the evaluation-time pipeline: go list, graph, module
// hashes, Nix.
package resolve

import (
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

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
	GoWork      bool     `json:"goWork"`     // use the go.work at modRoot or above it inside src
	Vendor      bool     `json:"vendor"`     // let go use vendor/, as go build does
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
	dir, workFile, err := moduleDir(src, a.ModRoot, a.GoWork)
	if err != nil {
		return err
	}
	// A go command that fails as a whole may be about the go.work or the
	// vendor/ that gonixgo let it use.
	failed := func(err error) error { return listFailed(err, dir, workFile, a.Vendor) }
	moduleEnv, err := golist.ModuleEnv(a.Go, dir, opts.Stderr)
	if err != nil {
		return err
	}
	o := golist.Options{
		Go:           a.Go,
		Dir:          dir,
		GOOS:         a.GOOS,
		GOARCH:       a.GOARCH,
		GOARM:        a.GOARM,
		Tags:         a.Tags,
		ModuleEnv:    moduleEnv,
		Stderr:       opts.Stderr,
		GOWORK:       workFile,
		IgnoreVendor: !a.Vendor,
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
		return failed(err)
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
		return failed(err)
	}
	pkgs, err := golist.List(o, patterns(a.SubPackages)...)
	if err != nil {
		return failed(err)
	}
	sums, err := readSums(o, workFile)
	if err != nil {
		return failed(err)
	}

	g, err := graph.Build(graph.Input{Packages: pkgs, Src: src, Env: env, Tags: a.Tags, Sums: sums})
	if err != nil {
		var loadErr *graph.LoadError
		if crossCgoOff && errors.As(err, &loadErr) {
			loadErr.CrossCgoOff = true
		}
		return err
	}
	if crossCgoOff {
		warnCgoLeftOut(o, patterns(a.SubPackages), opts.Stderr)
	}
	if a.DoCheck && len(g.Tested) > 0 {
		if err := addTests(g, o, graph.Input{Src: src, Env: env, Tags: a.Tags, Sums: sums}); err != nil {
			var loadErr *graph.LoadError
			if !errors.As(err, &loadErr) {
				err = failed(err)
			}
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

// warnCgoLeftOut names the packages that a cross build, by leaving cgo
// off, builds without their cgo files. They load, so nothing else reports
// them, but they may not compile, or not work. It asks go list again with
// cgo on; should that fail, there is only no warning.
func warnCgoLeftOut(o golist.Options, patterns []string, stderr io.Writer) {
	o.CgoEnabled = "1"
	pkgs, err := golist.List(o, patterns...)
	if err != nil {
		return
	}
	var names []string
	for _, p := range pkgs {
		if !p.Standard && len(p.CgoFiles) > 0 {
			names = append(names, p.ImportPath)
		}
	}
	if len(names) == 0 {
		return
	}
	slices.Sort(names)
	fmt.Fprintf(stderr, "gonixgo: cgo is off in a cross build, so these packages build without their cgo files: %s; set CGO_ENABLED = 1 to build cgo packages\n",
		strings.Join(names, ", "))
}

// moduleDir is the directory go list runs in, modRoot inside src ("" is
// src itself), and with work set the go.work to use: the first one in it
// or a directory above it, up to src. It needs a go.mod or a go.work.
func moduleDir(src, modRoot string, work bool) (dir, workFile string, err error) {
	rel := filepath.FromSlash(modRoot)
	if rel == "" {
		rel = "."
	}
	if !filepath.IsLocal(rel) {
		return "", "", fmt.Errorf("modRoot %q must be a directory inside src", modRoot)
	}
	dir = filepath.Join(src, rel)
	if work {
		workFile = findWorkFile(src, dir)
	}
	if _, err := os.Stat(filepath.Join(dir, "go.mod")); err != nil && workFile == "" {
		msg := fmt.Sprintf("modRoot %q: no go.mod in %s", modRoot, dir)
		if work {
			msg += ", and no go.work in it or above it inside src"
		}
		return "", "", errors.New(msg)
	}
	return dir, workFile, nil
}

// findWorkFile returns the first go.work in dir or a directory above it,
// up to and including src, or "" if there is none. dir is inside src.
func findWorkFile(src, dir string) string {
	for {
		f := filepath.Join(dir, "go.work")
		if info, err := os.Stat(f); err == nil && !info.IsDir() {
			return f
		}
		parent := filepath.Dir(dir)
		if dir == src || parent == dir {
			return ""
		}
		dir = parent
	}
}

// readSums reads the checksums go.sum files hold: modRoot's in module
// mode; in workspace mode go.work.sum's and those of every directory the
// go.work uses.
func readSums(o golist.Options, workFile string) (map[string]string, error) {
	files := []string{filepath.Join(o.Dir, "go.sum")}
	if workFile != "" {
		uses, err := golist.WorkUses(o, workFile)
		if err != nil {
			return nil, err
		}
		files = []string{filepath.Join(filepath.Dir(workFile), "go.work.sum")}
		for _, dir := range uses {
			files = append(files, filepath.Join(dir, "go.sum"))
		}
	}
	sums := map[string]string{}
	for _, f := range files {
		// A module without go.sum has no checksums to give.
		if data, err := os.ReadFile(f); err == nil {
			maps.Copy(sums, modcache.ParseGoSum(data))
		}
	}
	return sums, nil
}

// listFailed adds to err, from a go command that failed as a whole, the
// go.work and vendor/ gonixgo let go use, and how to turn each off.
func listFailed(err error, dir, workFile string, vendor bool) error {
	var extra string
	if workFile != "" {
		extra += "\ngonixgo used the workspace " + workFile + "; set useGoWork = false to build without it"
	}
	if vendor && hasVendor(dir, workFile) {
		extra += "\ngonixgo let go use vendor/; set useVendor = false to fetch modules instead"
	}
	if extra == "" {
		return err
	}
	return fmt.Errorf("%w%s", err, extra)
}

// hasVendor reports whether vendor/modules.txt is in dir or next to the
// go.work.
func hasVendor(dir, workFile string) bool {
	paths := []string{filepath.Join(dir, "vendor", "modules.txt")}
	if workFile != "" {
		paths = append(paths, filepath.Join(filepath.Dir(workFile), "vendor", "modules.txt"))
	}
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	return false
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
