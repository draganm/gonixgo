// Package emit prints a graph as the Nix function gonixgo's builders
// consume.
package emit

import (
	"fmt"
	"io"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/draganm/gonixgo/internal/graph"
)

// Nix writes g as a function from the builder set to the module, package,
// binary and test derivations. Output is deterministic: every set is sorted.
func Nix(w io.Writer, g *graph.Graph) error {
	var b strings.Builder
	b.WriteString("b: rec {\n")
	attr(&b, 1, "goVersion", quote(g.GoVersion))
	attr(&b, 1, "cgoEnabled", strconv.FormatBool(g.CgoEnabled))

	b.WriteString("  modules = {\n")
	for _, m := range g.ModuleList() {
		fmt.Fprintf(&b, "    %s = b.fetchModule {\n", quote(m.Key))
		attr(&b, 3, "name", quote(m.Name))
		attr(&b, 3, "path", quote(m.Path))
		attr(&b, 3, "version", quote(m.Version))
		attr(&b, 3, "hash", quote(m.Hash))
		b.WriteString("    };\n")
	}
	b.WriteString("  };\n")

	b.WriteString("  packages = {\n")
	for _, importPath := range slices.Sorted(maps.Keys(g.Packages)) {
		compileNode(&b, g, importPath, g.Packages[importPath])
	}
	b.WriteString("  };\n")

	b.WriteString("  bins = {\n")
	for _, bin := range g.Bins {
		linkNode(&b, g, bin.Name, bin)
	}
	b.WriteString("  };\n")

	tested := slices.Sorted(maps.Keys(g.Tests))
	b.WriteString("  testSources = {\n")
	for _, ip := range tested {
		t := g.Tests[ip]
		fmt.Fprintf(&b, "    %s = b.testDir {\n", quote(ip))
		attr(&b, 3, "name", quote(t.SrcName))
		attr(&b, 3, "importPath", quote(ip))
		attr(&b, 3, "module", quote(t.ModulePath))
		attr(&b, 3, "files", list(t.SrcFiles))
		attr(&b, 3, "trees", list(t.SrcTrees))
		b.WriteString("    };\n")
	}
	b.WriteString("  };\n")

	b.WriteString("  testPackages = {\n")
	for _, key := range slices.Sorted(maps.Keys(g.TestPackages)) {
		compileNode(&b, g, key, g.TestPackages[key])
	}
	b.WriteString("  };\n")

	b.WriteString("  testBins = {\n")
	for _, ip := range tested {
		linkNode(&b, g, ip, g.Tests[ip].Bin)
	}
	b.WriteString("  };\n")

	b.WriteString("  tests = {\n")
	for _, ip := range tested {
		t := g.Tests[ip]
		fmt.Fprintf(&b, "    %s = b.runTest {\n", quote(ip))
		attr(&b, 3, "name", quote(t.Name))
		attr(&b, 3, "importPath", quote(ip))
		attr(&b, 3, "module", quote(t.ModulePath))
		attr(&b, 3, "src", "testSources."+quote(ip))
		attr(&b, 3, "subdir", quote(t.Subdir))
		attr(&b, 3, "bin", "testBins."+quote(ip))
		attr(&b, 3, "binName", quote(t.Bin.Name))
		b.WriteString("    };\n")
	}
	b.WriteString("  };\n")

	b.WriteString("}\n")
	_, err := io.WriteString(w, b.String())
	return err
}

// compileNode prints p, the package node at key, as a b.compile call.
func compileNode(b *strings.Builder, g *graph.Graph, key string, p *graph.Package) {
	fmt.Fprintf(b, "    %s = b.compile {\n", quote(key))
	attr(b, 3, "name", quote(p.Name))
	attr(b, 3, "importPath", quote(p.ImportPath))
	if p.TestMain == "" {
		switch {
		case p.TestSrc != "":
			attr(b, 3, "src", "testSources."+quote(p.TestSrc))
		case p.Local:
			trees := ""
			if len(p.SrcTrees) > 0 {
				trees = fmt.Sprintf(" trees = %s;", list(p.SrcTrees))
			}
			attr(b, 3, "src", fmt.Sprintf("b.localDir { name = %s; files = %s;%s }", quote(p.SrcName), list(p.SrcFiles), trees))
		default:
			attr(b, 3, "src", "modules."+quote(p.ModuleKey))
		}
		attr(b, 3, "subdir", quote(p.Subdir))
	}
	attr(b, 3, "module", quote(p.ModulePath))
	if p.TrimTo == "" {
		// What is under test keeps its source paths.
		attr(b, 3, "trimTo", "null")
	} else {
		attr(b, 3, "trimTo", quote(p.TrimTo))
	}
	attr(b, 3, "lang", quote(p.Lang))
	attr(b, 3, "isMain", strconv.FormatBool(p.IsMain))
	if p.TestMain != "" {
		attr(b, 3, "testMain", quote(p.TestMain))
	} else {
		attr(b, 3, "goFiles", list(p.GoFiles))
		attr(b, 3, "sFiles", list(p.SFiles))
		attr(b, 3, "embed", embed(p.Embed))
	}
	attr(b, 3, "deps", refs(g, p.Deps))
	if c := p.Cgo; c != nil {
		// Its presence makes the compile builder use the C toolchain.
		b.WriteString("      cgo = {\n")
		attr(b, 4, "pkgName", quote(c.PkgName))
		attr(b, 4, "cgoFiles", list(c.CgoFiles))
		attr(b, 4, "cFiles", list(c.CFiles))
		attr(b, 4, "cxxFiles", list(c.CXXFiles))
		attr(b, 4, "mFiles", list(c.MFiles))
		attr(b, 4, "cppflags", list(c.CPPFLAGS))
		attr(b, 4, "cflags", list(c.CFLAGS))
		attr(b, 4, "cxxflags", list(c.CXXFLAGS))
		attr(b, 4, "ldflags", list(c.LDFLAGS))
		attr(b, 4, "pkgConfig", list(c.PkgConfig))
		b.WriteString("      };\n")
	}
	b.WriteString("    };\n")
}

// linkNode prints bin, the binary node at key, as a b.link call.
func linkNode(b *strings.Builder, g *graph.Graph, key string, bin *graph.Binary) {
	fmt.Fprintf(b, "    %s = b.link {\n", quote(key))
	attr(b, 3, "name", quote(bin.DrvName))
	attr(b, 3, "binName", quote(bin.Name))
	attr(b, 3, "main", ref(g, bin.Main))
	attr(b, 3, "deps", refs(g, bin.Deps))
	attr(b, 3, "modinfo", quote(bin.Modinfo))
	attr(b, 3, "godebug", quote(bin.Godebug))
	if bin.Cgo {
		attr(b, 3, "cgo", "true")
	}
	if bin.CXX {
		attr(b, 3, "cxx", "true")
	}
	if bin.Test {
		attr(b, 3, "test", "true")
	}
	b.WriteString("    };\n")
}

func attr(b *strings.Builder, depth int, name, value string) {
	fmt.Fprintf(b, "%s%s = %s;\n", strings.Repeat("  ", depth), name, value)
}

// list formats items as a Nix list of strings.
func list(items []string) string {
	if len(items) == 0 {
		return "[ ]"
	}
	quoted := make([]string, len(items))
	for i, item := range items {
		quoted[i] = quote(item)
	}
	return "[ " + strings.Join(quoted, " ") + " ]"
}

// ref is a reference to the package node at key, in testPackages or
// packages.
func ref(g *graph.Graph, key string) string {
	if _, ok := g.TestPackages[key]; ok {
		return "testPackages." + quote(key)
	}
	return "packages." + quote(key)
}

// refs formats keys as a Nix list of references to package nodes.
func refs(g *graph.Graph, keys []string) string {
	if len(keys) == 0 {
		return "[ ]"
	}
	out := make([]string, len(keys))
	for i, key := range keys {
		out[i] = ref(g, key)
	}
	return "[ " + strings.Join(out, " ") + " ]"
}

// embed formats the pattern-to-files map as a Nix attribute set.
func embed(m map[string][]string) string {
	if len(m) == 0 {
		return "{ }"
	}
	var b strings.Builder
	b.WriteString("{")
	for _, pattern := range slices.Sorted(maps.Keys(m)) {
		fmt.Fprintf(&b, " %s = %s;", quote(pattern), list(m[pattern]))
	}
	b.WriteString(" }")
	return b.String()
}

// quote formats s as a Nix double-quoted string. It serves for attribute
// names too.
func quote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		switch c := s[i]; c {
		case '\\', '"':
			b.WriteByte('\\')
			b.WriteByte(c)
		case '$':
			// Only "${" starts an interpolation.
			if i+1 < len(s) && s[i+1] == '{' {
				b.WriteByte('\\')
			}
			b.WriteByte('$')
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			b.WriteByte(c)
		}
	}
	b.WriteByte('"')
	return b.String()
}
