package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/draganm/gonixgo/internal/modcache"
	"github.com/draganm/gonixgo/internal/resolve"
)

func init() { commands["resolve"] = resolveCmd }

// resolveCmd runs at evaluation time under builtins.exec. Its stdout is
// parsed as a Nix expression, so everything else goes to stderr.
func resolveCmd(args []string, stdout io.Writer) error {
	if len(args) != 1 {
		return errors.New("usage: gonixgo resolve <json>")
	}
	var a resolve.Args
	if err := json.Unmarshal([]byte(args[0]), &a); err != nil {
		return fmt.Errorf("parsing arguments: %w", err)
	}
	opts := resolve.Options{Stderr: os.Stderr}
	if dir, err := os.UserCacheDir(); err == nil {
		opts.CacheDir = filepath.Join(dir, "gonixgo", "narhash")
	}
	if _, err := exec.LookPath("nix"); err == nil {
		opts.Add = modcache.NixStoreAdd
	} else {
		fmt.Fprintln(os.Stderr, "gonixgo: nix is not on PATH; modules will be fetched at build time")
	}
	return resolve.Run(a, opts, stdout)
}
