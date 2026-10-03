// Package modinfo produces the module information `go build -trimpath`
// embeds in a binary, the text `go version -m` prints.
package modinfo

import (
	"fmt"
	"strconv"
	"strings"
)

// Module is one mod or dep line.
type Module struct {
	Path    string
	Version string
	Sum     string
}

// Setting is one build line.
type Setting struct {
	Key   string
	Value string
}

// Info mirrors runtime/debug.BuildInfo without the Go version, which the
// linker records separately.
type Info struct {
	Path     string   // import path of the main package
	Main     Module   // the main module
	Deps     []Module // modules providing packages to the binary, sorted by path
	Settings []Setting
}

// String formats i as runtime/debug.BuildInfo.String does.
func (i Info) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "path\t%s\n", i.Path)
	fmt.Fprintf(&b, "mod\t%s\t%s\t%s\n", i.Main.Path, i.Main.Version, i.Main.Sum)
	for _, d := range i.Deps {
		fmt.Fprintf(&b, "dep\t%s\t%s\t%s\n", d.Path, d.Version, d.Sum)
	}
	for _, s := range i.Settings {
		key, value := s.Key, s.Value
		if key == "" || strings.ContainsAny(key, "= \t\r\n\"`") {
			key = strconv.Quote(key)
		}
		if strings.ContainsAny(value, " \t\r\n\"`") {
			value = strconv.Quote(value)
		}
		fmt.Fprintf(&b, "build\t%s=%s\n", key, value)
	}
	return b.String()
}

// archKey names the go env variable recorded for each GOARCH.
var archKey = map[string]string{
	"386":      "GO386",
	"amd64":    "GOAMD64",
	"arm":      "GOARM",
	"arm64":    "GOARM64",
	"mips":     "GOMIPS",
	"mipsle":   "GOMIPS",
	"mips64":   "GOMIPS64",
	"mips64le": "GOMIPS64",
	"ppc64":    "GOPPC64",
	"ppc64le":  "GOPPC64",
	"riscv64":  "GORISCV64",
	"wasm":     "GOWASM",
}

// Settings returns the build settings cmd/go records for a -trimpath
// build, in its order. Under -trimpath cmd/go omits -ldflags and the CGO_*
// flag variables.
func Settings(env map[string]string, tags []string, godebug string) []Setting {
	s := []Setting{{"-buildmode", "exe"}, {"-compiler", "gc"}}
	if len(tags) > 0 {
		s = append(s, Setting{"-tags", strings.Join(tags, ",")})
	}
	s = append(s, Setting{"-trimpath", "true"})
	if godebug != "" {
		s = append(s, Setting{"DefaultGODEBUG", godebug})
	}
	s = append(s,
		Setting{"CGO_ENABLED", env["CGO_ENABLED"]},
		Setting{"GOARCH", env["GOARCH"]},
		Setting{"GOOS", env["GOOS"]},
	)
	if key := archKey[env["GOARCH"]]; key != "" && env[key] != "" {
		s = append(s, Setting{key, env[key]})
	}
	return s
}

// The markers cmd/go puts around the module info so tools can find it in
// the binary.
const (
	infoStart = "\x30\x77\xaf\x0c\x92\x74\x08\x02\x41\xe1\xc1\x07\xe6\xd6\x18\xe6"
	infoEnd   = "\xf9\x32\x43\x31\x86\x18\x20\x72\x00\x82\x42\x10\x41\x16\xd8\xf2"
)

// Wrap returns info between the markers, the value of the linker
// importcfg's modinfo line.
func Wrap(info string) string {
	return infoStart + info + infoEnd
}
