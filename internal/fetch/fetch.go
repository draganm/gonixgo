// Package fetch downloads one Go module and keeps its extracted source
// tree, the content a module's fixed-output derivation is hashed over.
package fetch

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/draganm/gonixgo/internal/gotool"
)

// Manifest names the module to download. The fetchModule builder in
// nix/builders.nix produces it.
type Manifest struct {
	Go      string `json:"go"`
	Path    string `json:"path"`
	Version string `json:"version"`
}

// Run downloads the module into a private module cache under workDir and
// copies its extracted tree to outDir. Download metadata is left behind so
// the result does not depend on which proxy served it.
func Run(m Manifest, outDir, workDir string) error {
	modCache := filepath.Join(workDir, "modcache")
	home := filepath.Join(workDir, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		return err
	}
	if err := gotool.DisableTelemetry(home); err != nil {
		return err
	}
	cmd := exec.Command(m.Go, "mod", "download", m.Path+"@"+m.Version)
	cmd.Dir = workDir
	// GOPROXY, NETRC and the proxy variables come from the environment:
	// the derivation lists them as impureEnvVars.
	cmd.Env = append(os.Environ(),
		"HOME="+home,
		"XDG_CONFIG_HOME="+filepath.Join(home, ".config"),
		"GOMODCACHE="+modCache,
		"GOCACHE="+filepath.Join(workDir, "gocache"),
		"GOENV=off",
		"GOFLAGS=",
		"GOWORK=off",
		"GOTOOLCHAIN=local",
		// The derivation's output hash is the integrity check.
		"GOSUMDB=off",
	)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("go mod download %s@%s: %w", m.Path, m.Version, err)
	}
	return copyTree(filepath.Join(modCache, Escape(m.Path)+"@"+Escape(m.Version)), outDir)
}

// Escape applies the module cache's case encoding: an upper-case letter
// becomes '!' followed by its lower-case form.
func Escape(s string) string {
	var b strings.Builder
	for _, r := range s {
		if 'A' <= r && r <= 'Z' {
			b.WriteByte('!')
			r += 'a' - 'A'
		}
		b.WriteRune(r)
	}
	return b.String()
}

// copyTree copies src to dst, keeping what a NAR records: file contents,
// the execute bit, symlinks and directory structure. The copy is writable
// even though the module cache is not.
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
		switch {
		case d.IsDir():
			return os.MkdirAll(target, 0o755)
		case d.Type()&fs.ModeSymlink != 0:
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		case d.Type().IsRegular():
			info, err := d.Info()
			if err != nil {
				return err
			}
			mode := fs.FileMode(0o644)
			if info.Mode()&0o100 != 0 {
				mode = 0o755
			}
			return copyFile(path, target, mode)
		default:
			return fmt.Errorf("%s: unsupported file type %s", path, d.Type())
		}
	})
}

func copyFile(src, dst string, mode fs.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
