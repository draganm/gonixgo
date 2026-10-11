// Package gotool locates and runs the Go toolchain's compile, asm and link
// binaries for one target.
package gotool

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Toolchain is a Go toolchain aimed at one GOOS/GOARCH.
type Toolchain struct {
	GOOS    string
	GOARCH  string
	GOARM   string // the ARM version, "" for Go's default
	GOROOT  string
	ToolDir string

	env  map[string]string // go env values, including the GO<arch> keys
	home string
}

var envKeys = []string{
	"GOOS", "GOARCH", "GOROOT", "GOTOOLDIR",
	"GO386", "GOAMD64", "GOARM", "GOARM64", "GOMIPS", "GOMIPS64", "GOPPC64", "GORISCV64",
	"CC", "CXX",
}

// New asks goBin about itself. goos and goarch may be empty for the host,
// and goarm for Go's default. workDir is a scratch directory the
// toolchain may write to.
func New(goBin, goos, goarch, goarm, workDir string) (*Toolchain, error) {
	t := &Toolchain{GOOS: goos, GOARCH: goarch, GOARM: goarm, home: filepath.Join(workDir, "home")}
	if err := os.MkdirAll(t.home, 0o755); err != nil {
		return nil, err
	}
	if err := DisableTelemetry(t.home); err != nil {
		return nil, err
	}
	cmd := exec.Command(goBin, append([]string{"env", "-json"}, envKeys...)...)
	cmd.Env = t.environ()
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go env: %w", err)
	}
	if err := json.Unmarshal(out, &t.env); err != nil {
		return nil, fmt.Errorf("decoding go env output: %w", err)
	}
	t.GOOS, t.GOARCH = t.env["GOOS"], t.env["GOARCH"]
	t.GOROOT, t.ToolDir = t.env["GOROOT"], t.env["GOTOOLDIR"]
	return t, nil
}

// DisableTelemetry turns Go's telemetry off for a go command run with
// HOME=home and XDG_CONFIG_HOME=home/.config. Otherwise every go invocation
// writes counter files into home and starts a detached child process that
// outlives the build step. Go reads the mode from os.UserConfigDir, which
// is under Library/Application Support on darwin and XDG_CONFIG_HOME
// elsewhere.
func DisableTelemetry(home string) error {
	for _, dir := range []string{
		filepath.Join(home, "Library", "Application Support", "go", "telemetry"),
		filepath.Join(home, ".config", "go", "telemetry"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, "mode"), []byte("off"), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// environ is a minimal environment: nothing from the caller's Go
// configuration leaks into a build step.
func (t *Toolchain) environ(extra ...string) []string {
	env := []string{
		"PATH=" + os.Getenv("PATH"),
		"TMPDIR=" + os.Getenv("TMPDIR"),
		"HOME=" + t.home,
		"XDG_CONFIG_HOME=" + filepath.Join(t.home, ".config"),
		"GOCACHE=" + filepath.Join(t.home, "gocache"),
		"GOENV=off",
		"GOFLAGS=",
		"GOTOOLCHAIN=local",
	}
	if t.GOOS != "" {
		env = append(env, "GOOS="+t.GOOS)
	}
	if t.GOARCH != "" {
		env = append(env, "GOARCH="+t.GOARCH)
	}
	if t.GOARM != "" {
		env = append(env, "GOARM="+t.GOARM)
	}
	return append(env, extra...)
}

// InheritedEnviron is the caller's environment with the variables environ
// pins replaced by their pinned values, plus extra. The C compiler wrapper,
// pkg-config and an external link read their configuration from the
// derivation's environment, so they cannot run in the minimal one.
func (t *Toolchain) InheritedEnviron(extra ...string) []string {
	// A later entry for a key replaces an earlier one, here rather than in
	// whatever reads the environment.
	pinned := t.environ(extra...)
	last := map[string]int{}
	for i, kv := range pinned {
		key, _, _ := strings.Cut(kv, "=")
		last[key] = i
	}
	var env []string
	for _, kv := range os.Environ() {
		key, _, _ := strings.Cut(kv, "=")
		if _, isPinned := last[key]; !isPinned {
			env = append(env, kv)
		}
	}
	for i, kv := range pinned {
		key, _, _ := strings.Cut(kv, "=")
		if last[key] == i {
			env = append(env, kv)
		}
	}
	return env
}

// Tool runs a toolchain binary such as compile, asm or link in dir. Its
// output goes to stderr.
func (t *Toolchain) Tool(dir string, extraEnv []string, name string, args ...string) error {
	return t.run(t.environ(toolEnv(dir, extraEnv)...), dir, name, args)
}

// HostTool is Tool with InheritedEnviron: for cgo, which runs the C
// compiler, and for a link that runs the C linker.
func (t *Toolchain) HostTool(dir string, extraEnv []string, name string, args ...string) error {
	return t.run(t.InheritedEnviron(toolEnv(dir, extraEnv)...), dir, name, args)
}

// toolEnv is what every tool run adds to its environment. The tools
// resolve relative file arguments against PWD, as cmd/go arranges;
// -trimpath matches on the resulting absolute paths.
func toolEnv(dir string, extraEnv []string) []string {
	return append([]string{"PWD=" + dir}, extraEnv...)
}

func (t *Toolchain) run(env []string, dir, name string, args []string) error {
	cmd := exec.Command(filepath.Join(t.ToolDir, name), args...)
	cmd.Dir = dir
	cmd.Env = env
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("go tool %s: %w", name, err)
	}
	return nil
}

// Env returns a go env value New read: the GO<arch> keys, CC and CXX.
func (t *Toolchain) Env(key string) string {
	return t.env[key]
}

// CC is the C compiler command: $CC when set, otherwise the toolchain's
// default. It may hold arguments.
func (t *Toolchain) CC() string {
	return t.compiler("CC")
}

// CXX is CC for C++.
func (t *Toolchain) CXX() string {
	return t.compiler("CXX")
}

func (t *Toolchain) compiler(key string) string {
	if cmd := os.Getenv(key); cmd != "" {
		return cmd
	}
	return t.env[key]
}

// PIE reports whether the target's default build mode is a
// position-independent executable.
func (t *Toolchain) PIE() bool {
	switch t.GOOS {
	case "android", "ios", "windows", "darwin":
		return true
	}
	return false
}

// Shared reports whether compile and asm need -shared, which cmd/go passes
// for PIE targets other than Windows.
func (t *Toolchain) Shared() bool {
	return t.PIE() && t.GOOS != "windows"
}

// AsmDefines returns the -D flags cmd/go passes to the assembler.
func (t *Toolchain) AsmDefines() []string {
	d := []string{"-D", "GOOS_" + t.GOOS, "-D", "GOARCH_" + t.GOARCH}
	def := func(names ...string) {
		for _, name := range names {
			d = append(d, "-D", name)
		}
	}
	switch t.GOARCH {
	case "386":
		def("GO386_" + t.env["GO386"])
	case "amd64":
		def("GOAMD64_" + t.env["GOAMD64"])
	case "arm":
		switch {
		case strings.Contains(t.env["GOARM"], "7"):
			def("GOARM_7", "GOARM_6", "GOARM_5")
		case strings.Contains(t.env["GOARM"], "6"):
			def("GOARM_6", "GOARM_5")
		default:
			def("GOARM_5")
		}
	case "arm64":
		if strings.Contains(t.env["GOARM64"], ",lse") {
			def("GOARM64_LSE")
		}
	case "mips", "mipsle":
		def("GOMIPS_" + t.env["GOMIPS"])
	case "mips64", "mips64le":
		def("GOMIPS64_" + t.env["GOMIPS64"])
	case "ppc64", "ppc64le":
		switch t.env["GOPPC64"] {
		case "power10":
			def("GOPPC64_power10", "GOPPC64_power9", "GOPPC64_power8")
		case "power9":
			def("GOPPC64_power9", "GOPPC64_power8")
		default:
			def("GOPPC64_power8")
		}
	case "riscv64":
		def("GORISCV64_" + t.env["GORISCV64"])
	}
	return d
}

// ConcatFiles writes the contents of srcs, in order, to dst. It builds an
// importcfg from the fragments each package derivation outputs.
func ConcatFiles(dst string, srcs []string) error {
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	for _, src := range srcs {
		in, err := os.Open(src)
		if err != nil {
			out.Close()
			return err
		}
		_, err = io.Copy(out, in)
		in.Close()
		if err != nil {
			out.Close()
			return err
		}
	}
	return out.Close()
}
