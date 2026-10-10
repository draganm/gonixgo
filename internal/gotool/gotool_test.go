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
	tc, err := New(testutil.Go(t), "", "", "", t.TempDir())
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
	tc, err := New(testutil.Go(t), "linux", "amd64", "", t.TempDir())
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

func TestNewDisablesTelemetry(t *testing.T) {
	work := t.TempDir()
	tc, err := New(testutil.Go(t), "", "", "", work)
	if err != nil {
		t.Fatal(err)
	}
	if err := tc.Tool(t.TempDir(), nil, "compile", "-V"); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(work, "home")
	for _, dir := range []string{
		filepath.Join(home, "Library", "Application Support", "go", "telemetry"),
		filepath.Join(home, ".config", "go", "telemetry"),
	} {
		mode, err := os.ReadFile(filepath.Join(dir, "mode"))
		if err != nil || strings.TrimSpace(string(mode)) != "off" {
			t.Errorf("%s/mode = %q, %v; want off", dir, mode, err)
		}
		if _, err := os.Stat(filepath.Join(dir, "local")); err == nil {
			t.Errorf("telemetry wrote %s/local", dir)
		}
	}
}

func TestToolReportsFailure(t *testing.T) {
	tc, err := New(testutil.Go(t), "", "", "", t.TempDir())
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

// envMap indexes an environment by key and fails the test if a key
// appears twice: which entry wins would depend on the program reading it.
func envMap(t *testing.T, env []string) map[string]string {
	t.Helper()
	m := map[string]string{}
	for _, kv := range env {
		key, value, _ := strings.Cut(kv, "=")
		if _, dup := m[key]; dup {
			t.Errorf("%s appears twice in the environment", key)
		}
		m[key] = value
	}
	return m
}

func TestInheritedEnviron(t *testing.T) {
	t.Setenv("NIX_CFLAGS_COMPILE", "-isystem /x")
	t.Setenv("GOFLAGS", "-mod=mod")
	t.Setenv("HOME", "/caller")
	work := t.TempDir()
	tc, err := New(testutil.Go(t), "", "", "", work)
	if err != nil {
		t.Fatal(err)
	}

	env := envMap(t, tc.InheritedEnviron())
	// What the compiler wrapper reads comes through.
	if got := env["NIX_CFLAGS_COMPILE"]; got != "-isystem /x" {
		t.Errorf("NIX_CFLAGS_COMPILE = %q, want the caller's", got)
	}
	// What the toolchain pins does not.
	if got, ok := env["GOFLAGS"]; !ok || got != "" {
		t.Errorf("GOFLAGS = %q (set: %v), want it pinned to empty", got, ok)
	}
	if got, want := env["HOME"], filepath.Join(work, "home"); got != want {
		t.Errorf("HOME = %q, want the private %q", got, want)
	}
	if env["GOENV"] != "off" || env["GOTOOLCHAIN"] != "local" {
		t.Errorf("GOENV = %q, GOTOOLCHAIN = %q", env["GOENV"], env["GOTOOLCHAIN"])
	}

	env = envMap(t, tc.InheritedEnviron("TERM=dumb", "GOFLAGS=x"))
	if env["TERM"] != "dumb" || env["GOFLAGS"] != "x" {
		t.Errorf("extra entries do not win: TERM = %q, GOFLAGS = %q", env["TERM"], env["GOFLAGS"])
	}
}

func TestHostToolRunsAndReportsFailure(t *testing.T) {
	tc, err := New(testutil.Go(t), "", "", "", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := tc.HostTool(t.TempDir(), nil, "compile", "-V"); err != nil {
		t.Fatal(err)
	}
	err = tc.HostTool(t.TempDir(), nil, "compile", "-no-such-flag")
	if err == nil || !strings.Contains(err.Error(), "go tool compile") {
		t.Fatalf("err = %v, want it to name the tool", err)
	}
}

func TestCCAndCXX(t *testing.T) {
	t.Setenv("CC", "my-cc -m64")
	t.Setenv("CXX", "")
	tc, err := New(testutil.Go(t), "", "", "", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got := tc.CC(); got != "my-cc -m64" {
		t.Errorf("CC() = %q, want $CC", got)
	}
	// With $CXX unset the toolchain's own default applies.
	if got := tc.CXX(); got == "" || got != tc.Env("CXX") {
		t.Errorf("CXX() = %q, want the toolchain default %q", got, tc.Env("CXX"))
	}
}

// GOARM reaches every tool run, and the assembler's defines follow it.
func TestNewARMVersion(t *testing.T) {
	tc, err := New(testutil.Go(t), "linux", "arm", "6", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got := envMap(t, tc.environ())["GOARM"]; got != "6" {
		t.Errorf("GOARM = %q in the tool environment, want 6", got)
	}
	want := []string{"-D", "GOOS_linux", "-D", "GOARCH_arm", "-D", "GOARM_6", "-D", "GOARM_5"}
	if got := tc.AsmDefines(); !reflect.DeepEqual(got, want) {
		t.Errorf("AsmDefines = %v, want %v", got, want)
	}

	// Without one, Go's default applies and the environment sets none.
	tc, err = New(testutil.Go(t), "linux", "arm64", "", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := envMap(t, tc.environ())["GOARM"]; ok {
		t.Errorf("GOARM = %q in the tool environment, want none", got)
	}
}
