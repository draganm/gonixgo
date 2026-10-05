package graph

import (
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/draganm/gonixgo/internal/golist"
)

const srcDirVar = "${SRCDIR}"

// restoreSrcDir undoes go list's expansion of ${SRCDIR} in a package's
// #cgo flags. go list substitutes the directory it found the package in,
// which is the module cache's or the evaluation-time source's; the package
// is built somewhere else.
func restoreSrcDir(flags []string, dir string) []string {
	if len(flags) == 0 {
		return nil
	}
	out := make([]string, len(flags))
	for i, flag := range flags {
		out[i] = strings.ReplaceAll(flag, dir, srcDirVar)
	}
	return out
}

// srcDirRefs returns what follows each ${SRCDIR} in flags, up to the
// character that ends a path inside a flag.
func srcDirRefs(flags []string) []string {
	var refs []string
	for _, flag := range flags {
		for {
			i := strings.Index(flag, srcDirVar)
			if i < 0 {
				break
			}
			flag = flag[i+len(srcDirVar):]
			end := strings.IndexAny(flag, ",=:\"' \t")
			if end < 0 {
				end = len(flag)
			}
			refs = append(refs, flag[:end])
		}
	}
	return refs
}

// addNamedPaths adds to a local cgo package's source every file and
// directory its #cgo directives name through ${SRCDIR}. go list reports
// only the files in the package directory that it recognises, so a header
// directory named by -I${SRCDIR}/include would otherwise be missing from
// the build.
func addNamedPaths(pkg *Package, p *golist.Package, src string, stat func(string) (isDir, exists bool)) error {
	c := pkg.Cgo
	for _, ref := range srcDirRefs(slices.Concat(c.CPPFLAGS, c.CFLAGS, c.CXXFLAGS, c.LDFLAGS)) {
		if ref != "" && !strings.HasPrefix(ref, "/") {
			continue // ${SRCDIR}x names nothing under the package directory
		}
		abs := filepath.Join(p.Dir, filepath.FromSlash(ref))
		rel, err := filepath.Rel(src, abs)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return fmt.Errorf("%s: a #cgo directive names %s, which is outside src %s", p.ImportPath, abs, src)
		}
		isDir, exists := stat(abs)
		switch {
		case !exists:
			// A directive may name a directory only some checkouts have.
		case isDir:
			pkg.SrcTrees = append(pkg.SrcTrees, filepath.ToSlash(rel))
		default:
			pkg.SrcFiles = append(pkg.SrcFiles, filepath.ToSlash(rel))
		}
	}
	sort.Strings(pkg.SrcFiles)
	pkg.SrcFiles = slices.Compact(pkg.SrcFiles)
	sort.Strings(pkg.SrcTrees)
	pkg.SrcTrees = slices.Compact(pkg.SrcTrees)
	return nil
}
