package main

import (
	"io"
	"os"

	"github.com/draganm/gonixgo/internal/testrun"
)

func init() { commands["test"] = testCmd }

// testCmd is the builder of a test run derivation. The test log is its
// output.
func testCmd(_ []string, _ io.Writer) error {
	var m testrun.Manifest
	out, err := loadManifest(&m)
	if err != nil {
		return err
	}
	work, err := os.MkdirTemp("", "gonixgo-test-")
	if err != nil {
		return err
	}
	// A test may leave read-only files, such as a module cache under
	// HOME; the build directory goes away anyway.
	defer os.RemoveAll(work)
	return testrun.Run(m, out, work, os.Stderr)
}
