package main

import (
	"io"
	"os"

	"github.com/draganm/gonixgo/internal/link"
)

func init() { commands["link"] = linkCmd }

// linkCmd is the builder of a binary derivation.
func linkCmd(_ []string, _ io.Writer) error {
	var m link.Manifest
	out, err := loadManifest(&m)
	if err != nil {
		return err
	}
	work, err := os.MkdirTemp("", "gonixgo-link-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	return link.Run(m, out, work)
}
