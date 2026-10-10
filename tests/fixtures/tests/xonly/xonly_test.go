package xonly_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"example.com/tests/xonly"
)

var mainRan bool

func TestMain(m *testing.M) {
	mainRan = true
	os.Exit(m.Run())
}

func TestName(t *testing.T) {
	if !mainRan {
		t.Fatal("TestMain did not run")
	}
	if xonly.Name() != "xonly" {
		t.Fatal(xonly.Name())
	}
}

// HOME is an empty directory the test may write to.
func TestHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "note"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Without an entry of its own the package gets the program's checkEnv and
// tools, and the build's Go comes first on PATH.
func TestEnvironment(t *testing.T) {
	if got := os.Getenv("FIXTURE_SCOPE"); got != "program" {
		t.Errorf("FIXTURE_SCOPE = %q, want program", got)
	}
	if out, err := exec.Command("hello").Output(); err != nil || string(out) != "Hello, world!\n" {
		t.Errorf("hello: %q, %v", out, err)
	}
	if out, err := exec.Command("go", "env", "GOROOT").Output(); err != nil || len(out) == 0 {
		t.Errorf("go env GOROOT: %q, %v", out, err)
	}
}
