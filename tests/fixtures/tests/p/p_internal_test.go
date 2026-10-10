package p

import (
	_ "embed"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

//go:embed testdata/embedded.txt
var embedded string

func TestAdd(t *testing.T) {
	if Add(1, 2) != 3 {
		t.Fatal("1+2 != 3")
	}
}

// The working directory is the package's, in a writable copy.
func TestTestdata(t *testing.T) {
	data, err := os.ReadFile("testdata/golden.txt")
	if err != nil || string(data) != "golden\n" {
		t.Fatalf("testdata/golden.txt: %q, %v", data, err)
	}
	if err := os.WriteFile("testdata/written.txt", data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// The package under test keeps its source paths, so runtime.Caller names
// a file that exists.
func TestCaller(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok || !filepath.IsAbs(file) {
		t.Fatalf("runtime.Caller(0) = %q", file)
	}
	data, err := os.ReadFile(filepath.Join(filepath.Dir(file), "testdata", "golden.txt"))
	if err != nil || string(data) != "golden\n" {
		t.Fatalf("golden.txt beside %s: %q, %v", file, data, err)
	}
}

func TestEmbed(t *testing.T) {
	if embedded != "embedded\n" {
		t.Fatalf("embedded = %q", embedded)
	}
}

// shared/ is outside the package; its testExtraSrc brings it.
func TestShared(t *testing.T) {
	data, err := os.ReadFile("../shared/fixture.txt")
	if err != nil || string(data) != "shared\n" {
		t.Fatalf("../shared/fixture.txt: %q, %v", data, err)
	}
}

// The package's checkEnv wins over the program's, whose other values stay.
func TestEnv(t *testing.T) {
	if got := os.Getenv("FIXTURE_SCOPE"); got != "package" {
		t.Errorf("FIXTURE_SCOPE = %q, want package", got)
	}
	if got := os.Getenv("FIXTURE_PROGRAM"); got != "1" {
		t.Errorf("FIXTURE_PROGRAM = %q, want 1", got)
	}
}

// The program's nativeCheckInputs and the package's are both on PATH.
func TestTools(t *testing.T) {
	for _, tool := range []string{"hello", "jq"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Error(err)
		}
	}
}

// The package's checkFlags skip it.
func TestSkipped(t *testing.T) {
	t.Fatal("TestSkipped ran; the package's -skip did not reach the test binary")
}

// tests/run.sh sets FIXTURE_FAIL to see a failing test fail the build.
func TestFailSwitch(t *testing.T) {
	if os.Getenv("FIXTURE_FAIL") != "" {
		t.Fatal("FIXTURE_FAIL is set")
	}
}
