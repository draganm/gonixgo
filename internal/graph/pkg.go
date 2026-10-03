package graph

import (
	"fmt"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/draganm/gonixgo/internal/golist"
	"github.com/draganm/gonixgo/internal/storepath"
)

// newPackage turns one non-standard go list package into a graph node. It
// also returns the module a third-party package comes from.
func newPackage(p *golist.Package, src string, byPath map[string]*golist.Package) (*Package, *Module, error) {
	m := p.Module
	switch {
	case m == nil:
		return nil, nil, fmt.Errorf("%s: not part of a module", p.ImportPath)
	case m.Replace != nil:
		return nil, nil, fmt.Errorf("%s: module %s is replaced; replace directives are not supported yet", p.ImportPath, m.Path)
	case hasNonGo(p):
		return nil, nil, fmt.Errorf("%s: cgo, C, C++, Objective-C, Fortran, SWIG and .syso files are not supported yet", p.ImportPath)
	}

	pkg := &Package{
		ImportPath: p.ImportPath,
		IsMain:     p.Name == "main",
		ModulePath: m.Path,
		Lang:       lang(m.GoVersion),
		GoFiles:    p.GoFiles,
		SFiles:     p.SFiles,
		Embed:      embedMap(p.EmbedPatterns, p.EmbedFiles),
	}
	for _, imp := range p.Imports {
		if mapped, ok := p.ImportMap[imp]; ok {
			imp = mapped
		}
		dep, ok := byPath[imp]
		if !ok {
			return nil, nil, fmt.Errorf("%s: imports %s, which go list did not report", p.ImportPath, imp)
		}
		if !dep.Standard {
			pkg.Deps = append(pkg.Deps, imp)
		}
	}
	sort.Strings(pkg.Deps)

	if m.Main {
		rel, err := filepath.Rel(src, p.Dir)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return nil, nil, fmt.Errorf("%s: directory %s is outside src %s", p.ImportPath, p.Dir, src)
		}
		pkg.Local = true
		pkg.Name = storepath.SanitizeName("golocal-" + p.ImportPath)
		pkg.SrcName = storepath.SanitizeName("gosrc-" + p.ImportPath)
		pkg.Subdir = slashDir(rel)
		pkg.TrimTo = p.ImportPath
		pkg.SrcFiles = srcFiles(pkg.Subdir, p)
		return pkg, nil, nil
	}

	if m.Version == "" || m.Dir == "" {
		return nil, nil, fmt.Errorf("%s: module %s has no version or is not in the module cache", p.ImportPath, m.Path)
	}
	rel, err := filepath.Rel(m.Dir, p.Dir)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, nil, fmt.Errorf("%s: directory %s is outside module directory %s", p.ImportPath, p.Dir, m.Dir)
	}
	key := m.Path + "@" + m.Version
	pkg.Name = storepath.SanitizeName("gopkg-" + p.ImportPath + "-" + m.Version)
	pkg.ModuleKey = key
	pkg.Subdir = slashDir(rel)
	// cmd/go rewrites a module-cache directory to module@version/subdir.
	pkg.TrimTo = key + strings.TrimPrefix(p.ImportPath, m.Path)
	mod := &Module{
		Key:     key,
		Path:    m.Path,
		Version: m.Version,
		Dir:     m.Dir,
		Name:    storepath.SanitizeName("gomod-" + m.Path + "-" + m.Version),
	}
	return pkg, mod, nil
}

func hasNonGo(p *golist.Package) bool {
	return len(p.CgoFiles)+len(p.CFiles)+len(p.CXXFiles)+len(p.MFiles)+len(p.FFiles)+
		len(p.SwigFiles)+len(p.SwigCXXFiles)+len(p.SysoFiles) > 0
}

// slashDir turns a relative directory into the form the graph uses: slash
// separated, "" for the root.
func slashDir(rel string) string {
	if rel == "." {
		return ""
	}
	return filepath.ToSlash(rel)
}

// srcFiles lists every file a local package's compile reads, relative to
// the source root.
func srcFiles(subdir string, p *golist.Package) []string {
	var files []string
	for _, group := range [][]string{p.GoFiles, p.SFiles, p.HFiles, p.EmbedFiles} {
		for _, f := range group {
			files = append(files, path.Join(subdir, f))
		}
	}
	sort.Strings(files)
	return slices.Compact(files)
}

// lang returns the -lang value cmd/go passes for a module's go directive:
// its major.minor, or go1.16 when the module has none.
func lang(goVersion string) string {
	if goVersion == "" {
		goVersion = "1.16"
	}
	end, dots := len(goVersion), 0
	for i, c := range goVersion {
		if c == '.' {
			dots++
			if dots < 2 {
				continue
			}
		} else if '0' <= c && c <= '9' {
			continue
		}
		end = i
		break
	}
	return "go" + goVersion[:end]
}

// execName names a main package's binary as cmd/go does: the last element
// of its import path, or the one before a major-version suffix.
func execName(importPath string) string {
	dir, elem := path.Split(importPath)
	if elem != importPath && isVersionElement(elem) {
		_, elem = path.Split(path.Clean(dir))
	}
	return elem
}

// isVersionElement reports whether s is a major-version path element such
// as v2 (not v0 or v1).
func isVersionElement(s string) bool {
	if len(s) < 2 || s[0] != 'v' || s[1] == '0' || s[1] == '1' && len(s) == 2 {
		return false
	}
	for i := 1; i < len(s); i++ {
		if s[i] < '0' || '9' < s[i] {
			return false
		}
	}
	return true
}

// embedMap assigns the files go list matched to the //go:embed patterns
// that matched them, the mapping the compiler's -embedcfg needs. go list
// reports only the union.
func embedMap(patterns, files []string) map[string][]string {
	if len(patterns) == 0 {
		return nil
	}
	m := make(map[string][]string, len(patterns))
	for _, pattern := range patterns {
		glob, all := strings.CutPrefix(pattern, "all:")
		matched := []string{}
		for _, file := range files {
			if embedMatches(glob, all, file) {
				matched = append(matched, file)
			}
		}
		m[pattern] = matched
	}
	return m
}

// embedMatches reports whether glob embeds file. A glob that matches the
// file itself embeds it. A glob that matches a directory above it embeds
// the directory's tree, minus names beginning with '.' or '_' unless the
// pattern had the all: prefix.
func embedMatches(glob string, all bool, file string) bool {
	if ok, _ := path.Match(glob, file); ok {
		return true
	}
	parts := strings.Split(file, "/")
	for i := 1; i < len(parts); i++ {
		if ok, _ := path.Match(glob, strings.Join(parts[:i], "/")); !ok {
			continue
		}
		if all {
			return true
		}
		hidden := false
		for _, elem := range parts[i:] {
			if strings.HasPrefix(elem, ".") || strings.HasPrefix(elem, "_") {
				hidden = true
			}
		}
		if !hidden {
			return true
		}
	}
	return false
}
