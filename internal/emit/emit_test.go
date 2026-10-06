package emit

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/draganm/gonixgo/internal/graph"
)

func testGraph() *graph.Graph {
	return &graph.Graph{
		GoVersion: "1.26.8", GOOS: "darwin", GOARCH: "arm64", CgoEnabled: true,
		Modules: map[string]*graph.Module{
			"github.com/fatih/color@v1.18.0": {
				Key: "github.com/fatih/color@v1.18.0", Path: "github.com/fatih/color", Version: "v1.18.0",
				Name: "gomod-github.com-fatih-color-v1.18.0", Hash: "sha256-AAAA",
			},
		},
		Packages: map[string]*graph.Package{
			"example.com/app": {
				ImportPath: "example.com/app", Name: "golocal-example.com-app", SrcName: "gosrc-example.com-app",
				Local: true, IsMain: true, ModulePath: "example.com/app", TrimTo: "example.com/app", Lang: "go1.24",
				GoFiles:  []string{"main.go"},
				SrcFiles: []string{"main.go", "static/a b.txt"},
				Embed:    map[string][]string{"static/*": {"static/a b.txt"}},
				Deps:     []string{"github.com/fatih/color"},
			},
			"github.com/fatih/color": {
				ImportPath: "github.com/fatih/color", Name: "gopkg-github.com-fatih-color-v1.18.0",
				ModuleKey: "github.com/fatih/color@v1.18.0", ModulePath: "github.com/fatih/color",
				TrimTo: "github.com/fatih/color@v1.18.0", Lang: "go1.17",
				GoFiles: []string{"color.go", "doc.go"},
			},
		},
		Bins: []*graph.Binary{{
			Name: "app", DrvName: "gobin-app", Main: "example.com/app",
			Deps:    []string{"github.com/fatih/color"},
			Modinfo: "path\texample.com/app\n", Godebug: "x=1",
		}},
	}
}

const golden = `b: rec {
  goVersion = "1.26.8";
  cgoEnabled = true;
  modules = {
    "github.com/fatih/color@v1.18.0" = b.fetchModule {
      name = "gomod-github.com-fatih-color-v1.18.0";
      path = "github.com/fatih/color";
      version = "v1.18.0";
      hash = "sha256-AAAA";
    };
  };
  packages = {
    "example.com/app" = b.compile {
      name = "golocal-example.com-app";
      importPath = "example.com/app";
      src = b.localDir { name = "gosrc-example.com-app"; files = [ "main.go" "static/a b.txt" ]; };
      subdir = "";
      module = "example.com/app";
      trimTo = "example.com/app";
      lang = "go1.24";
      isMain = true;
      goFiles = [ "main.go" ];
      sFiles = [ ];
      embed = { "static/*" = [ "static/a b.txt" ]; };
      deps = [ packages."github.com/fatih/color" ];
    };
    "github.com/fatih/color" = b.compile {
      name = "gopkg-github.com-fatih-color-v1.18.0";
      importPath = "github.com/fatih/color";
      src = modules."github.com/fatih/color@v1.18.0";
      subdir = "";
      module = "github.com/fatih/color";
      trimTo = "github.com/fatih/color@v1.18.0";
      lang = "go1.17";
      isMain = false;
      goFiles = [ "color.go" "doc.go" ];
      sFiles = [ ];
      embed = { };
      deps = [ ];
    };
  };
  bins = {
    "app" = b.link {
      name = "gobin-app";
      binName = "app";
      main = packages."example.com/app";
      deps = [ packages."github.com/fatih/color" ];
      modinfo = "path\texample.com/app\n";
      godebug = "x=1";
    };
  };
}
`

func TestNixGolden(t *testing.T) {
	var buf bytes.Buffer
	if err := Nix(&buf, testGraph()); err != nil {
		t.Fatal(err)
	}
	if got := buf.String(); got != golden {
		t.Fatalf("Nix() =\n%s\nwant\n%s", got, golden)
	}
}

func TestNixEmptyGraphSections(t *testing.T) {
	g := testGraph()
	g.Modules = map[string]*graph.Module{}
	var buf bytes.Buffer
	if err := Nix(&buf, g); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(buf.Bytes(), []byte("  modules = {\n  };\n")) {
		t.Fatalf("empty modules set not emitted:\n%s", buf.String())
	}
}

func TestQuote(t *testing.T) {
	tests := []struct{ in, want string }{
		{`plain`, `"plain"`},
		{`with "quotes"`, `"with \"quotes\""`},
		{`back\slash`, `"back\\slash"`},
		{`${interp}`, `"\${interp}"`},
		{`$${x}`, `"$\${x}"`},
		{`cost $5`, `"cost $5"`},
		{"tab\tline\nreturn\r", `"tab\tline\nreturn\r"`},
	}
	for _, tt := range tests {
		if got := quote(tt.in); got != tt.want {
			t.Errorf("quote(%q) = %s, want %s", tt.in, got, tt.want)
		}
	}
}

func TestQuoteRoundTripsThroughNix(t *testing.T) {
	nix, err := exec.LookPath("nix-instantiate")
	if err != nil {
		t.Skip("nix-instantiate not on PATH")
	}
	for _, s := range []string{
		`plain`, `sp ace`, `with "quotes"`, `back\slash`, `${interp}`, `$${x}`, `''two quotes''`,
		"tab\tline\nreturn\r", `päth/ünïcode`, `static/a b ${c}.txt`,
	} {
		out, err := exec.Command(nix, "--eval", "--strict", "--json", "-E", quote(s)).Output()
		if err != nil {
			t.Errorf("nix-instantiate rejected %s: %v", quote(s), err)
			continue
		}
		var got string
		if err := json.Unmarshal(out, &got); err != nil {
			t.Errorf("decoding %s: %v", out, err)
			continue
		}
		if got != s {
			t.Errorf("%q came back from Nix as %q", s, got)
		}
	}
}

func TestNixOutputEvaluates(t *testing.T) {
	nix, err := exec.LookPath("nix-instantiate")
	if err != nil {
		t.Skip("nix-instantiate not on PATH")
	}
	file := filepath.Join(t.TempDir(), "graph.nix")
	var buf bytes.Buffer
	if err := Nix(&buf, testGraph()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	// Builders that return the node's name show the wiring evaluates.
	expr := fmt.Sprintf("import %s { fetchModule = a: a.name; localDir = a: a.name; compile = a: a.name; link = a: a.name; }", file)
	out, err := exec.Command(nix, "--eval", "--strict", "--json", "-E", expr).Output()
	if err != nil {
		t.Fatalf("nix-instantiate: %v", err)
	}
	var got struct {
		GoVersion string            `json:"goVersion"`
		Packages  map[string]string `json:"packages"`
		Bins      map[string]string `json:"bins"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if got.GoVersion != "1.26.8" || got.Packages["example.com/app"] != "golocal-example.com-app" || got.Bins["app"] != "gobin-app" {
		t.Fatalf("evaluated to %+v", got)
	}
}

// cgoGraph is testGraph plus a local cgo package, linked into a binary
// that needs the C++ compiler.
func cgoGraph() *graph.Graph {
	g := testGraph()
	g.Packages["example.com/app/internal/cadd"] = &graph.Package{
		ImportPath: "example.com/app/internal/cadd", Name: "golocal-example.com-app-internal-cadd",
		SrcName: "gosrc-example.com-app-internal-cadd", Local: true, ModulePath: "example.com/app",
		Subdir: "internal/cadd", TrimTo: "example.com/app/internal/cadd", Lang: "go1.24",
		GoFiles:  []string{"plain.go"},
		SrcFiles: []string{"internal/cadd/add.c", "internal/cadd/cadd.go", "internal/cadd/plain.go"},
		SrcTrees: []string{"internal/cadd/include"},
		Cgo: &graph.Cgo{
			PkgName: "cadd", CgoFiles: []string{"cadd.go"}, CFiles: []string{"add.c"},
			CFLAGS: []string{"-DBONUS=0", "-I${SRCDIR}/include"}, LDFLAGS: []string{"-lm"},
		},
	}
	g.Bins[0].Cgo, g.Bins[0].CXX = true, true
	return g
}

func TestNixCgoGolden(t *testing.T) {
	var buf bytes.Buffer
	if err := Nix(&buf, cgoGraph()); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`      src = b.localDir { name = "gosrc-example.com-app-internal-cadd"; files = [ "internal/cadd/add.c" "internal/cadd/cadd.go" "internal/cadd/plain.go" ]; trees = [ "internal/cadd/include" ]; };
`,
		`      deps = [ ];
      cgo = {
        pkgName = "cadd";
        cgoFiles = [ "cadd.go" ];
        cFiles = [ "add.c" ];
        cxxFiles = [ ];
        mFiles = [ ];
        cppflags = [ ];
        cflags = [ "-DBONUS=0" "-I\${SRCDIR}/include" ];
        cxxflags = [ ];
        ldflags = [ "-lm" ];
        pkgConfig = [ ];
      };
    };
`,
		`      godebug = "x=1";
      cgo = true;
      cxx = true;
    };
`,
	} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("output lacks\n%s\ngot\n%s", want, buf.String())
		}
	}
}

func TestNixCgoLinkWithoutCXX(t *testing.T) {
	g := cgoGraph()
	g.Bins[0].CXX = false
	var buf bytes.Buffer
	if err := Nix(&buf, g); err != nil {
		t.Fatal(err)
	}
	if want := "      godebug = \"x=1\";\n      cgo = true;\n    };\n"; !strings.Contains(buf.String(), want) {
		t.Errorf("output lacks %q:\n%s", want, buf.String())
	}
}

// A pure graph must print exactly what it printed before cgo existed, or
// every derivation of every pure project would change.
func TestNixPureGraphHasNoCgo(t *testing.T) {
	var buf bytes.Buffer
	if err := Nix(&buf, testGraph()); err != nil {
		t.Fatal(err)
	}
	for _, unwanted := range []string{"cgo = ", "cxx", "trees"} {
		if strings.Contains(buf.String(), unwanted) {
			t.Errorf("pure graph output contains %q:\n%s", unwanted, buf.String())
		}
	}
}

func TestNixCgoRoundTripsThroughNix(t *testing.T) {
	nix, err := exec.LookPath("nix-instantiate")
	if err != nil {
		t.Skip("nix-instantiate not on PATH")
	}
	g := cgoGraph()
	cflags := []string{"-I${SRCDIR}/include", `-DGREETING="hello world"`, `-DPATH='a\b'`}
	g.Packages["example.com/app/internal/cadd"].Cgo.CFLAGS = cflags
	file := filepath.Join(t.TempDir(), "graph.nix")
	var buf bytes.Buffer
	if err := Nix(&buf, g); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	expr := fmt.Sprintf(`
		let g = import %s {
			fetchModule = a: a.name;
			localDir = a: a.trees or [ ];
			compile = a: { cflags = a.cgo.cflags or [ ]; pkgName = a.cgo.pkgName or ""; trees = a.src; };
			link = a: { cgo = a.cgo or false; cxx = a.cxx or false; };
		};
		in { cadd = g.packages."example.com/app/internal/cadd"; bin = g.bins.app; }`, file)
	out, err := exec.Command(nix, "--eval", "--strict", "--json", "-E", expr).Output()
	if err != nil {
		t.Fatalf("nix-instantiate: %v", err)
	}
	var got struct {
		Cadd struct {
			Cflags  []string `json:"cflags"`
			PkgName string   `json:"pkgName"`
			Trees   []string `json:"trees"`
		} `json:"cadd"`
		Bin struct {
			Cgo bool `json:"cgo"`
			CXX bool `json:"cxx"`
		} `json:"bin"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Cadd.Cflags, cflags) {
		t.Errorf("cflags came back from Nix as %q, want %q", got.Cadd.Cflags, cflags)
	}
	if got.Cadd.PkgName != "cadd" || !reflect.DeepEqual(got.Cadd.Trees, []string{"internal/cadd/include"}) || !got.Bin.Cgo || !got.Bin.CXX {
		t.Errorf("evaluated to %+v", got)
	}
}
