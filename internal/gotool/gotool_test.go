package gotool

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/draganm/gonixgo/internal/testutil"
)

func TestNew(t *testing.T) {
	tc, err := New(testutil.Go(t), "", "", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if tc.GOOS != runtime.GOOS || tc.GOARCH != runtime.GOARCH {
		t.Errorf("target = %s/%s, want the host's %s/%s", tc.GOOS, tc.GOARCH, runtime.GOOS, runtime.GOARCH)
	}
	for _, file := range []string{
		filepath.Join(tc.ToolDir, "compile"),
		filepath.Join(tc.ToolDir, "asm"),
		filepath.Join(tc.ToolDir, "link"),
		filepath.Join(tc.GOROOT, "pkg", "include", "textflag.h"),
	} {
		if _, err := os.Stat(file); err != nil {
			t.Errorf("missing %s", file)
		}
	}
}

func TestNewCrossTarget(t *testing.T) {
	tc, err := New(testutil.Go(t), "linux", "amd64", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if tc.GOOS != "linux" || tc.GOARCH != "amd64" || tc.PIE() || tc.Shared() {
		t.Errorf("toolchain = %+v, PIE = %v", tc, tc.PIE())
	}
	want := []string{"-D", "GOOS_linux", "-D", "GOARCH_amd64", "-D", "GOAMD64_v1"}
	if got := tc.AsmDefines(); !reflect.DeepEqual(got, want) {
		t.Errorf("AsmDefines = %v, want %v", got, want)
	}
}

func TestPIEAndShared(t *testing.T) {
	tests := []struct {
		goos        string
		pie, shared bool
	}{
		{"linux", false, false},
		{"darwin", true, true},
		{"ios", true, true},
		{"android", true, true},
		{"windows", true, false},
		{"freebsd", false, false},
	}
	for _, tt := range tests {
		tc := &Toolchain{GOOS: tt.goos}
		if tc.PIE() != tt.pie || tc.Shared() != tt.shared {
			t.Errorf("%s: PIE = %v, Shared = %v, want %v, %v", tt.goos, tc.PIE(), tc.Shared(), tt.pie, tt.shared)
		}
	}
}

func TestAsmDefines(t *testing.T) {
	tests := []struct {
		goarch string
		env    map[string]string
		want   []string
	}{
		{"arm64", map[string]string{"GOARM64": "v8.0"}, nil},
		{"arm64", map[string]string{"GOARM64": "v8.1,lse"}, []string{"GOARM64_LSE"}},
		{"amd64", map[string]string{"GOAMD64": "v3"}, []string{"GOAMD64_v3"}},
		{"386", map[string]string{"GO386": "sse2"}, []string{"GO386_sse2"}},
		{"arm", map[string]string{"GOARM": "7"}, []string{"GOARM_7", "GOARM_6", "GOARM_5"}},
		{"arm", map[string]string{"GOARM": "6,softfloat"}, []string{"GOARM_6", "GOARM_5"}},
		{"arm", map[string]string{"GOARM": "5"}, []string{"GOARM_5"}},
		{"ppc64le", map[string]string{"GOPPC64": "power9"}, []string{"GOPPC64_power9", "GOPPC64_power8"}},
		{"riscv64", map[string]string{"GORISCV64": "rva20u64"}, []string{"GORISCV64_rva20u64"}},
		{"mipsle", map[string]string{"GOMIPS": "hardfloat"}, []string{"GOMIPS_hardfloat"}},
		{"mips64", map[string]string{"GOMIPS64": "hardfloat"}, []string{"GOMIPS64_hardfloat"}},
	}
	for _, tt := range tests {
		tc := &Toolchain{GOOS: "linux", GOARCH: tt.goarch, env: tt.env}
		want := []string{"-D", "GOOS_linux", "-D", "GOARCH_" + tt.goarch}
		for _, d := range tt.want {
			want = append(want, "-D", d)
		}
		if got := tc.AsmDefines(); !reflect.DeepEqual(got, want) {
			t.Errorf("%s %v: AsmDefines = %v, want %v", tt.goarch, tt.env, got, want)
		}
	}
}

func TestToolReportsFailure(t *testing.T) {
	tc, err := New(testutil.Go(t), "", "", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	err = tc.Tool(t.TempDir(), nil, "compile", "-no-such-flag")
	if err == nil || !strings.Contains(err.Error(), "go tool compile") {
		t.Fatalf("err = %v, want it to name the tool", err)
	}
}

func TestConcatFiles(t *testing.T) {
	dir := testutil.WriteTree(t, map[string]string{"a": "one\n", "b": "two\n"})
	dst := filepath.Join(dir, "out")
	if err := ConcatFiles(dst, []string{filepath.Join(dir, "a"), filepath.Join(dir, "b")}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "one\ntwo\n" {
		t.Fatalf("concatenation = %q", got)
	}
	if err := ConcatFiles(dst, []string{filepath.Join(dir, "absent")}); err == nil {
		t.Fatal("ConcatFiles of a missing file succeeded")
	}
}
