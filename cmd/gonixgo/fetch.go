package main

import (
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/draganm/gonixgo/internal/fetch"
)

func init() { commands["fetch"] = fetchCmd }

// fetchCmd is the builder of a module's fixed-output derivation. It runs
// only when the module was not pre-seeded into the store at evaluation.
func fetchCmd(_ []string, _ io.Writer) error {
	var m fetch.Manifest
	out, err := loadManifest(&m)
	if err != nil {
		return err
	}
	work, err := os.MkdirTemp("", "gonixgo-fetch-")
	if err != nil {
		return err
	}
	defer func() {
		// The private module cache is read-only; make it removable.
		filepath.WalkDir(work, func(path string, d fs.DirEntry, err error) error {
			if err == nil && d.IsDir() {
				os.Chmod(path, 0o755)
			}
			return nil
		})
		os.RemoveAll(work)
	}()
	return fetch.Run(m, out, work)
}
