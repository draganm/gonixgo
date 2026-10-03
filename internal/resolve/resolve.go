// Package resolve is the evaluation-time pipeline: go list, graph, module
// hashes, Nix.
package resolve

import (
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
	CgoEnabled  *bool    `json:"cgoEnabled"` // nil: Go's default for the target
	DoCheck     bool     `json:"doCheck"`    // accepted, unused until tests are supported
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
	dir := filepath.Join(src, filepath.FromSlash(a.ModRoot))
	moduleEnv, err := golist.ModuleEnv(a.Go, dir, opts.Stderr)
	if err != nil {
		return err
	}
	o := golist.Options{
		Go:        a.Go,
		Dir:       dir,
		GOOS:      a.GOOS,
		GOARCH:    a.GOARCH,
		Tags:      a.Tags,
		ModuleEnv: moduleEnv,
		Stderr:    opts.Stderr,
	}
	if a.CgoEnabled != nil {
		o.CgoEnabled = "0"
		if *a.CgoEnabled {
			o.CgoEnabled = "1"
		}
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
		return err
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
