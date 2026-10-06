package cc

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestSplitPkgConfigOutput(t *testing.T) {
	tests := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"\n", nil},
		{"-I/a -L/b -lfoo\n", []string{"-I/a", "-L/b", "-lfoo"}},
		{"  -I/a \t -lfoo  \n", []string{"-I/a", "-lfoo"}},
		{`-I/with\ space -DX="a b" -DY='c d'`, []string{"-I/with space", `-DX=a b`, `-DY=c d`}},
		{`-DQ=\"quoted\"`, []string{`-DQ="quoted"`}},
		{`-DX="a \"b\" \\ c"`, []string{`-DX=a "b" \ c`}},
		{`-DX="keep \n as is"`, []string{`-DX=keep \n as is`}},
		{`-DX='single \ stays'`, []string{`-DX=single \ stays`}},
		{"-I/a \\\n-I/b", []string{"-I/a", "-I/b"}},
		{`-DE="" ''`, []string{"-DE=", ""}},
	}
	for _, tt := range tests {
		got, err := SplitPkgConfigOutput([]byte(tt.in))
		if err != nil {
			t.Errorf("SplitPkgConfigOutput(%q): %v", tt.in, err)
			continue
		}
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("SplitPkgConfigOutput(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
	for _, in := range []string{`-DX="unterminated`, `-DX='unterminated`, `-I$HOME/include`, "-la; rm -rf /", "-I`pwd`", `trailing\`} {
		if got, err := SplitPkgConfigOutput([]byte(in)); err == nil {
			t.Errorf("SplitPkgConfigOutput(%q) = %q, want an error", in, got)
		}
	}
}

// fakePkgConfig logs its arguments on one line and answers --cflags and
// --libs; it fails for the package "nope" as pkg-config does.
const fakePkgConfig = `#!/bin/sh
echo "$*" >>"$FAKE_LOG"
case "$*" in *nope*) echo "Package nope was not found in the pkg-config search path." >&2; exit 1;; esac
case "$1" in
  --cflags) echo "-I/inc -DX=\"a b\"";;
  --libs) echo "-L/lib -lz";;
esac
`

func TestPkgConfig(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "fake-pkg-config")
	if err := os.WriteFile(bin, []byte(fakePkgConfig), 0o755); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(dir, "log")
	env := append(os.Environ(), "PKG_CONFIG="+bin, "FAKE_LOG="+log)

	cflags, ldflags, err := PkgConfig(env, dir, []string{"--static", "zlib", "--", "libpng"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cflags, []string{"-I/inc", "-DX=a b"}) || !reflect.DeepEqual(ldflags, []string{"-L/lib", "-lz"}) {
		t.Errorf("cflags = %q, ldflags = %q", cflags, ldflags)
	}
	got, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	// Flags go before a single "--", names after it.
	if want := "--cflags --static -- zlib libpng\n--libs --static -- zlib libpng\n"; string(got) != want {
		t.Errorf("pkg-config ran as\n%s\nwant\n%s", got, want)
	}

	_, _, err = PkgConfig(env, dir, []string{"nope"})
	if err == nil || !strings.Contains(err.Error(), "Package nope was not found") {
		t.Errorf("err = %v, want pkg-config's own message", err)
	}
}

func TestPkgConfigMissing(t *testing.T) {
	env := append(os.Environ(), "PKG_CONFIG=/nonexistent/pkg-config")
	_, _, err := PkgConfig(env, t.TempDir(), []string{"zlib"})
	if err == nil || !strings.Contains(err.Error(), "/nonexistent/pkg-config") {
		t.Fatalf("err = %v, want it to name the missing program", err)
	}
}
