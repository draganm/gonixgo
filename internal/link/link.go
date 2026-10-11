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
	"strings"

	"github.com/draganm/gonixgo/internal/gotool"
	"github.com/draganm/gonixgo/internal/modinfo"
)

// Manifest describes one link. The link builder in nix/builders.nix
// produces it.
type Manifest struct {
	Go         string   `json:"go"`
	GOOS       string   `json:"goos"`
	GOARCH     string   `json:"goarch"`
	GOARM      string   `json:"goarm"` // "" for Go's default
	BinName    string   `json:"binName"`
	Main       string   `json:"main"`       // the main package's archive
	Importcfgs []string `json:"importcfgs"` // fragments for the standard library, the main package and its transitive imports
	Modinfo    string   `json:"modinfo"`    // module info to embed
	Godebug    string   `json:"godebug"`    // DefaultGODEBUG, "" for none
	LDFlags    []string `json:"ldflags"`
	Cgo        bool     `json:"cgo"`  // the binary contains a cgo package: link with the C toolchain
	CXX        bool     `json:"cxx"`  // one of its packages has C++ files: the C++ compiler links
	Test       bool     `json:"test"` // a test binary: testing.Testing reports true
}

// Run links the binary to outDir/bin/<BinName>.
func Run(m Manifest, outDir, workDir string) error {
	tc, err := gotool.New(m.Go, m.GOOS, m.GOARCH, m.GOARM, workDir)
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
	if m.Test {
		// After the caller's flags, where go test puts it.
		args = append(args, "-X=testing.testBinary=1")
	}

	// An empty GOROOT keeps the toolchain's path out of the binary, as
	// go build -trimpath does.
	env := []string{"GOROOT="}
	if !m.Cgo {
		return tc.Tool(workDir, env, "link", append(args, m.Main)...)
	}

	// C objects are in the link, so the Go linker hands it to the C
	// linker, which it runs through the compiler. That is the compiler
	// wrapper of the derivation, and it finds the libraries through the
	// derivation's environment.
	compiler := tc.CC()
	if m.CXX {
		compiler = tc.CXX()
	}
	args = append(withExtld(args, compiler), m.Main)
	if err := tc.HostTool(workDir, env, "link", args...); err != nil {
		return fmt.Errorf("%w\nif a library is missing, add it to the buildInputs of the packageOverrides entry of the cgo package that uses it", err)
	}
	return nil
}

// withExtld appends -extld=compiler unless flags already choose the C
// linker, as cmd/go does.
func withExtld(flags []string, compiler string) []string {
	for _, flag := range flags {
		if flag == "-extld" || strings.HasPrefix(flag, "-extld=") {
			return flags
		}
	}
	return append(flags, "-extld="+compiler)
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
