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

// Nix writes g as a function from the builder set to the module, package
// and binary derivations. Output is deterministic: every set is sorted.
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
		p := g.Packages[importPath]
		fmt.Fprintf(&b, "    %s = b.compile {\n", quote(importPath))
		attr(&b, 3, "name", quote(p.Name))
		attr(&b, 3, "importPath", quote(p.ImportPath))
		if p.Local {
			attr(&b, 3, "src", fmt.Sprintf("b.localDir { name = %s; files = %s; }", quote(p.SrcName), list(p.SrcFiles)))
		} else {
			attr(&b, 3, "src", "modules."+quote(p.ModuleKey))
		}
		attr(&b, 3, "subdir", quote(p.Subdir))
		attr(&b, 3, "module", quote(p.ModulePath))
		attr(&b, 3, "trimTo", quote(p.TrimTo))
		attr(&b, 3, "lang", quote(p.Lang))
		attr(&b, 3, "isMain", strconv.FormatBool(p.IsMain))
		attr(&b, 3, "goFiles", list(p.GoFiles))
		attr(&b, 3, "sFiles", list(p.SFiles))
		attr(&b, 3, "embed", embed(p.Embed))
		attr(&b, 3, "deps", refs(p.Deps))
		b.WriteString("    };\n")
	}
	b.WriteString("  };\n")

	b.WriteString("  bins = {\n")
	for _, bin := range g.Bins {
		fmt.Fprintf(&b, "    %s = b.link {\n", quote(bin.Name))
		attr(&b, 3, "name", quote(bin.DrvName))
		attr(&b, 3, "binName", quote(bin.Name))
		attr(&b, 3, "main", "packages."+quote(bin.Main))
		attr(&b, 3, "deps", refs(bin.Deps))
		attr(&b, 3, "modinfo", quote(bin.Modinfo))
		attr(&b, 3, "godebug", quote(bin.Godebug))
		b.WriteString("    };\n")
	}
	b.WriteString("  };\n")

	b.WriteString("}\n")
	_, err := io.WriteString(w, b.String())
	return err
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

// refs formats import paths as a Nix list of references into packages.
func refs(importPaths []string) string {
	if len(importPaths) == 0 {
		return "[ ]"
	}
	out := make([]string, len(importPaths))
	for i, ip := range importPaths {
		out[i] = "packages." + quote(ip)
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
