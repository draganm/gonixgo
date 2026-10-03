package resolve

import (
	"bytes"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/draganm/gonixgo/internal/graph"
	"github.com/draganm/gonixgo/internal/testutil"
)

var appFiles = map[string]string{
	"go.mod": "module example.com/app\n\ngo 1.21\n",
	"main.go": `package main

import (
	"fmt"

	"example.com/app/internal/greet"
)

func main() { fmt.Println(greet.Hello()) }
`,
	"internal/greet/greet.go": "package greet\n\nfunc Hello() string { return \"hello\" }\n",
	"cmd/second/main.go":      "package main\n\nfunc main() {}\n",
}

func run(t *testing.T, a Args) (string, error) {
	t.Helper()
	a.Go = testutil.Go(t)
	a.StoreDir = "/nix/store"
	var out bytes.Buffer
	err := Run(a, Options{Stderr: io.Discard, CacheDir: t.TempDir()}, &out)
	return out.String(), err
}

func TestRunLocalOnly(t *testing.T) {
	out, err := run(t, Args{Src: testutil.WriteTree(t, appFiles), ModRoot: ".", SubPackages: []string{"."}})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"b: rec {\n",
		"  modules = {\n  };\n",
		`    "example.com/app" = b.compile {`,
		`      src = b.localDir { name = "gosrc-example.com-app"; files = [ "main.go" ]; };`,
		`    "example.com/app/internal/greet" = b.compile {`,
		`      deps = [ packages."example.com/app/internal/greet" ];`,
		`    "app" = b.link {`,
		`      lang = "go1.21";`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "cmd/second") {
		t.Errorf("output includes a package outside subPackages:\n%s", out)
	}
}

func TestRunSeveralSubPackages(t *testing.T) {
	out, err := run(t, Args{Src: testutil.WriteTree(t, appFiles), ModRoot: ".", SubPackages: []string{".", "cmd/second"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"app" = b.link {`, `"second" = b.link {`} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

func TestRunModRoot(t *testing.T) {
	files := map[string]string{}
	for name, content := range appFiles {
		files["services/api/"+name] = content
	}
	out, err := run(t, Args{Src: testutil.WriteTree(t, files), ModRoot: "services/api", SubPackages: []string{"."}})
	if err != nil {
		t.Fatal(err)
	}
	if want := `files = [ "services/api/internal/greet/greet.go" ]`; !strings.Contains(out, want) {
		t.Errorf("output lacks %q:\n%s", want, out)
	}
	if want := `subdir = "services/api/internal/greet";`; !strings.Contains(out, want) {
		t.Errorf("output lacks %q:\n%s", want, out)
	}
}

func TestRunReportsLoadErrors(t *testing.T) {
	files := map[string]string{
		"go.mod":  "module example.com/app\n\ngo 1.21\n",
		"main.go": "package main\n\nimport _ \"example.com/app/missing\"\n\nfunc main() {}\n",
	}
	out, err := run(t, Args{Src: testutil.WriteTree(t, files), ModRoot: ".", SubPackages: []string{"."}})
	var loadErr *graph.LoadError
	if !errors.As(err, &loadErr) {
		t.Fatalf("err = %v, want a *graph.LoadError", err)
	}
	if out != "" {
		t.Fatalf("stdout = %q, want nothing on failure", out)
	}
}

func TestRunCgoDisabled(t *testing.T) {
	off := false
	out, err := run(t, Args{Src: testutil.WriteTree(t, appFiles), ModRoot: ".", SubPackages: []string{"."}, CgoEnabled: &off})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "cgoEnabled = false;") || !strings.Contains(out, `build\tCGO_ENABLED=0\n`) {
		t.Fatalf("cgo setting not reflected:\n%s", out)
	}
}

func TestPatterns(t *testing.T) {
	tests := []struct {
		in   []string
		want []string
	}{
		{nil, []string{"."}},
		{[]string{"."}, []string{"."}},
		{[]string{"cmd/app", "./cmd/tool", "cmd/x/"}, []string{"./cmd/app", "./cmd/tool", "./cmd/x"}},
	}
	for _, tt := range tests {
		if got := patterns(tt.in); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("patterns(%v) = %v, want %v", tt.in, got, tt.want)
		}
	}
}
