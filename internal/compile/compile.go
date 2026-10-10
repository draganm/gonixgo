// Package compile builds one Go package into an archive, the way cmd/go
// does with -trimpath, without cmd/go.
package compile

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"github.com/draganm/gonixgo/internal/gotool"
)

// Manifest describes one package compile. The compile builder in
// nix/builders.nix produces it.
type Manifest struct {
	Go         string              `json:"go"`         // path to the go binary
	GOOS       string              `json:"goos"`       // "" for the host
	GOARCH     string              `json:"goarch"`     // "" for the host
	GOARM      string              `json:"goarm"`      // "" for Go's default
	ImportPath string              `json:"importPath"` //
	IsMain     bool                `json:"isMain"`     //
	SrcDir     string              `json:"srcDir"`     // directory holding the package's files
	TrimTo     string              `json:"trimTo"`     // what -trimpath rewrites SrcDir to, "" to keep it
	Lang       string              `json:"lang"`       // e.g. go1.24
	GoFiles    []string            `json:"goFiles"`    //
	SFiles     []string            `json:"sFiles"`     //
	Embed      map[string][]string `json:"embed"`      // //go:embed pattern to files
	Cgo        *Cgo                `json:"cgo"`        // nil for a pure package
	TestMain   string              `json:"testMain"`   // a test main's source; SrcDir and GoFiles are then unused
	Importcfgs []string            `json:"importcfgs"` // importcfg fragments of the standard library and direct imports
}

// Run compiles the package into outDir/pkg.a and writes outDir/importcfg,
// the fragment importers and the linker use to find it.
func Run(m Manifest, outDir, workDir string) error {
	tc, err := gotool.New(m.Go, m.GOOS, m.GOARCH, m.GOARM, workDir)
	if err != nil {
		return err
	}
	if m.TestMain != "" {
		// A test main has no source directory. Written into the work
		// directory, whose path is trimmed away, it is recorded as
		// _testmain.go, as under go test.
		if err := os.WriteFile(filepath.Join(workDir, "_testmain.go"), []byte(m.TestMain), 0o644); err != nil {
			return err
		}
		m.SrcDir, m.GoFiles = workDir, []string{"_testmain.go"}
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}
	importcfg := filepath.Join(workDir, "importcfg")
	if err := gotool.ConcatFiles(importcfg, m.Importcfgs); err != nil {
		return err
	}

	// In a cgo package the assembly files go to the C compiler with the
	// rest of the C side.
	sFiles := m.SFiles
	var cgoGoFiles, cgoMembers []string
	if m.Cgo != nil {
		if cgoGoFiles, cgoMembers, err = runCgo(tc, m, workDir); err != nil {
			return err
		}
		sFiles = nil
	}

	archive := filepath.Join(outDir, "pkg.a")
	pkgPath := m.ImportPath
	if m.IsMain {
		pkgPath = "main"
	}
	trim := workDir + "=>"
	if m.TrimTo != "" {
		trim = m.SrcDir + "=>" + m.TrimTo + ";" + trim
	}

	args := []string{"-o", archive, "-trimpath", trim, "-p", pkgPath, "-lang=" + m.Lang}
	if len(sFiles) == 0 && m.Cgo == nil {
		// Without assembly or C every function must have a body.
		args = append(args, "-complete")
	}
	args = append(args, "-buildid", "", "-c="+strconv.Itoa(cores()))
	if tc.Shared() {
		args = append(args, "-shared")
	}
	args = append(args, "-nolocalimports", "-importcfg", importcfg)
	if len(m.Embed) > 0 {
		embedcfg := filepath.Join(workDir, "embedcfg")
		if err := writeEmbedcfg(embedcfg, m.SrcDir, m.Embed); err != nil {
			return err
		}
		args = append(args, "-embedcfg", embedcfg)
	}
	args = append(args, "-pack")

	asm := []string{"-p", pkgPath, "-trimpath", trim, "-I", workDir, "-I", filepath.Join(tc.GOROOT, "pkg", "include")}
	asm = append(asm, tc.AsmDefines()...)
	if tc.Shared() {
		asm = append(asm, "-shared")
	}
	if len(sFiles) > 0 {
		// The compiler needs the assembly's symbol ABIs, and writes the
		// header the assembly includes.
		symabis := filepath.Join(workDir, "symabis")
		asmhdr := filepath.Join(workDir, "go_asm.h")
		if err := os.WriteFile(asmhdr, nil, 0o644); err != nil {
			return err
		}
		gen := slices.Concat(asm, []string{"-gensymabis", "-o", symabis}, dotSlash(sFiles))
		if err := tc.Tool(m.SrcDir, nil, "asm", gen...); err != nil {
			return err
		}
		args = append(args, "-symabis", symabis, "-asmhdr", asmhdr)
	}
	goFiles := append(dotSlash(m.GoFiles), cgoGoFiles...)
	if err := tc.Tool(m.SrcDir, nil, "compile", append(args, goFiles...)...); err != nil {
		return err
	}

	var objects []string
	for _, s := range sFiles {
		obj := filepath.Join(workDir, strings.TrimSuffix(filepath.Base(s), ".s")+".o")
		if err := tc.Tool(m.SrcDir, nil, "asm", slices.Concat(asm, []string{"-o", obj, "./" + s})...); err != nil {
			return err
		}
		objects = append(objects, obj)
	}
	if err := appendObjects(archive, append(objects, cgoMembers...)); err != nil {
		return err
	}

	line := "packagefile " + m.ImportPath + "=" + archive + "\n"
	return os.WriteFile(filepath.Join(outDir, "importcfg"), []byte(line), 0o644)
}

// cores is the compiler's backend concurrency.
func cores() int {
	if n, err := strconv.Atoi(os.Getenv("NIX_BUILD_CORES")); err == nil && n > 0 {
		return n
	}
	return runtime.NumCPU()
}

// dotSlash names files relative to the package directory, as cmd/go does.
func dotSlash(files []string) []string {
	out := make([]string, len(files))
	for i, f := range files {
		out[i] = "./" + f
	}
	return out
}

// writeEmbedcfg writes the file the compiler's -embedcfg flag reads:
// each pattern's files, and where each file is on disk.
func writeEmbedcfg(file, srcDir string, embed map[string][]string) error {
	cfg := struct {
		Patterns map[string][]string
		Files    map[string]string
	}{Patterns: embed, Files: map[string]string{}}
	for _, files := range embed {
		for _, f := range files {
			cfg.Files[f] = filepath.Join(srcDir, filepath.FromSlash(f))
		}
	}
	data, err := json.MarshalIndent(cfg, "", "\t")
	if err != nil {
		return err
	}
	return os.WriteFile(file, data, 0o644)
}

// appendObjects adds object files to a Go archive. The Go distribution no
// longer ships the pack tool, and cmd/go appends the same way.
func appendObjects(archive string, objects []string) error {
	if len(objects) == 0 {
		return nil
	}
	dst, err := os.OpenFile(archive, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		return err
	}
	defer dst.Close()
	w := bufio.NewWriter(dst)
	for _, object := range objects {
		data, err := os.ReadFile(object)
		if err != nil {
			return err
		}
		// The name field is 16 bytes. Build it by bytes: %-16s would pad
		// by runes and break the 60-byte header for a non-ASCII name.
		var name [16]byte
		copy(name[:], strings.Repeat(" ", 16))
		copy(name[:], filepath.Base(object))
		w.Write(name[:])
		fmt.Fprintf(w, "%-12d%-6d%-6d%-8o%-10d`\n", 0, 0, 0, 0o644, len(data))
		w.Write(data)
		if len(data)%2 != 0 {
			w.WriteByte(0)
		}
	}
	if err := w.Flush(); err != nil {
		return err
	}
	return dst.Close()
}
