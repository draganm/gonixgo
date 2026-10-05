// Package link links one main package into a binary, the way cmd/go does
// with -trimpath, without cmd/go.
package link

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/draganm/gonixgo/internal/gotool"
	"github.com/draganm/gonixgo/internal/modinfo"
)

// Manifest describes one link. The link builder in nix/builders.nix
// produces it.
type Manifest struct {
	Go         string   `json:"go"`
	GOOS       string   `json:"goos"`
	GOARCH     string   `json:"goarch"`
	BinName    string   `json:"binName"`
	Main       string   `json:"main"`       // the main package's archive
	Importcfgs []string `json:"importcfgs"` // fragments for the standard library, the main package and its transitive imports
	Modinfo    string   `json:"modinfo"`    // module info to embed
	Godebug    string   `json:"godebug"`    // DefaultGODEBUG, "" for none
	LDFlags    []string `json:"ldflags"`
}

// Run links the binary to outDir/bin/<BinName>.
func Run(m Manifest, outDir, workDir string) error {
	tc, err := gotool.New(m.Go, m.GOOS, m.GOARCH, workDir)
	if err != nil {
		return err
	}
	ldflags, err := gotool.SplitFlags(m.LDFlags)
	if err != nil {
		return fmt.Errorf("ldflags %w", err)
	}

	importcfg := filepath.Join(workDir, "importcfg.link")
	if err := gotool.ConcatFiles(importcfg, m.Importcfgs); err != nil {
		return err
	}
	f, err := os.OpenFile(importcfg, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(f, "modinfo %q\n", modinfo.Wrap(m.Modinfo))
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}

	binDir := filepath.Join(outDir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return err
	}
	args := []string{"-o", filepath.Join(binDir, m.BinName), "-importcfg", importcfg}
	if m.Godebug != "" {
		args = append(args, "-X=runtime.godebugDefault="+m.Godebug)
	}
	mode := "exe"
	if tc.PIE() {
		mode = "pie"
	}
	args = append(args, "-buildmode="+mode, "-buildid="+buildID(m))
	args = append(args, ldflags...)
	args = append(args, m.Main)

	// An empty GOROOT keeps the toolchain's path out of the binary, as
	// go build -trimpath does.
	return tc.Tool(workDir, []string{"GOROOT="}, "link", args...)
}

// buildID derives the binary's build ID from its manifest. The linker
// turns the build ID into the Mach-O LC_UUID and the ELF GNU build ID, so a
// constant would give every binary the same identity. In a Nix build the
// manifest names the store path of every input, so the ID changes exactly
// when an input does and stays reproducible.
func buildID(m Manifest) string {
	// A struct of strings and string slices always marshals.
	data, _ := json.Marshal(m)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:20])
}
