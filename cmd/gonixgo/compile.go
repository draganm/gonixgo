package main

import (
	"io"
	"os"

	"github.com/draganm/gonixgo/internal/compile"
)

func init() { commands["compile"] = compileCmd }

// compileCmd is the builder of a package derivation.
func compileCmd(_ []string, _ io.Writer) error {
	var m compile.Manifest
	out, err := loadManifest(&m)
	if err != nil {
		return err
	}
	work, err := os.MkdirTemp("", "gonixgo-compile-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	return compile.Run(m, out, work)
}
