package modinfo

import (
	"strconv"
	"strings"
	"testing"
)

// goldenEnv and goldenInfo reproduce what `go build -trimpath` with Go
// 1.26.8 embedded for module example.com/gx on darwin/arm64.
var goldenEnv = map[string]string{
	"CGO_ENABLED": "1", "GOARCH": "arm64", "GOOS": "darwin", "GOARM64": "v8.0", "GOAMD64": "v1",
}

const goldenGodebug = "containermaxprocs=0,cryptocustomrand=1,decoratemappings=0,tlssecpmlkem=0,tlssha1=1,updatemaxprocs=0,urlstrictcolons=0,x509sha256skid=0"

const goldenInfo = "path\texample.com/gx\n" +
	"mod\texample.com/gx\t(devel)\t\n" +
	"build\t-buildmode=exe\n" +
	"build\t-compiler=gc\n" +
	"build\t-trimpath=true\n" +
	"build\tDefaultGODEBUG=" + goldenGodebug + "\n" +
	"build\tCGO_ENABLED=1\n" +
	"build\tGOARCH=arm64\n" +
	"build\tGOOS=darwin\n" +
	"build\tGOARM64=v8.0\n"

func TestStringGolden(t *testing.T) {
	info := Info{
		Path:     "example.com/gx",
		Main:     Module{Path: "example.com/gx", Version: "(devel)"},
		Settings: Settings(goldenEnv, nil, goldenGodebug),
	}
	if got := info.String(); got != goldenInfo {
		t.Fatalf("String() =\n%q\nwant\n%q", got, goldenInfo)
	}
}

func TestStringWithDepsAndTags(t *testing.T) {
	info := Info{
		Path: "example.com/app/cmd/app",
		Main: Module{Path: "example.com/app", Version: "(devel)"},
		Deps: []Module{
			{Path: "github.com/fatih/color", Version: "v1.18.0", Sum: "h1:abc="},
			{Path: "golang.org/x/sys", Version: "v0.25.0"},
		},
		Settings: Settings(map[string]string{"CGO_ENABLED": "0", "GOARCH": "amd64", "GOOS": "linux", "GOAMD64": "v1"}, []string{"netgo", "osusergo"}, ""),
	}
	const want = "path\texample.com/app/cmd/app\n" +
		"mod\texample.com/app\t(devel)\t\n" +
		"dep\tgithub.com/fatih/color\tv1.18.0\th1:abc=\n" +
		"dep\tgolang.org/x/sys\tv0.25.0\t\n" +
		"build\t-buildmode=exe\n" +
		"build\t-compiler=gc\n" +
		"build\t-tags=netgo,osusergo\n" +
		"build\t-trimpath=true\n" +
		"build\tCGO_ENABLED=0\n" +
		"build\tGOARCH=amd64\n" +
		"build\tGOOS=linux\n" +
		"build\tGOAMD64=v1\n"
	if got := info.String(); got != want {
		t.Fatalf("String() =\n%q\nwant\n%q", got, want)
	}
}

func TestStringQuotesValuesWithSpaces(t *testing.T) {
	info := Info{Path: "p", Main: Module{Path: "m", Version: "(devel)"}, Settings: []Setting{{"-tags", "a b"}}}
	if got := info.String(); !strings.HasSuffix(got, "build\t-tags=\"a b\"\n") {
		t.Fatalf("String() = %q, want the value quoted", got)
	}
}

func TestWrap(t *testing.T) {
	// The quoted form go build -x printed in importcfg.link.
	const wantPrefix = `"0w\xaf\f\x92t\b\x02A\xe1\xc1\a\xe6\xd6\x18\xe6path\t`
	const wantSuffix = `GOARM64=v8.0\n\xf92C1\x86\x18 r\x00\x82B\x10A\x16\xd8\xf2"`
	got := strconv.Quote(Wrap(goldenInfo))
	if !strings.HasPrefix(got, wantPrefix) || !strings.HasSuffix(got, wantSuffix) {
		t.Fatalf("Quote(Wrap(info)) = %s", got)
	}
}
