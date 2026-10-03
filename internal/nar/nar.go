// Package nar serialises file trees in the Nix archive format and hashes
// them the way Nix does for recursive fixed-output paths.
package nar

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Write serialises the file tree rooted at root to w as a NAR.
func Write(w io.Writer, root string) error {
	if err := writeString(w, "nix-archive-1"); err != nil {
		return err
	}
	return writeNode(w, root)
}

// Hash returns the SHA-256 of the NAR serialisation of root.
func Hash(root string) ([32]byte, error) {
	var sum [32]byte
	h := sha256.New()
	if err := Write(h, root); err != nil {
		return sum, err
	}
	h.Sum(sum[:0])
	return sum, nil
}

// SRI formats sum as the "sha256-<base64>" string Nix's outputHash accepts.
func SRI(sum [32]byte) string {
	return "sha256-" + base64.StdEncoding.EncodeToString(sum[:])
}

// ParseSRI is the inverse of SRI.
func ParseSRI(s string) ([32]byte, error) {
	var sum [32]byte
	b64, ok := strings.CutPrefix(s, "sha256-")
	if !ok {
		return sum, fmt.Errorf("nar: %q is not a sha256 SRI hash", s)
	}
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil || len(raw) != len(sum) {
		return sum, fmt.Errorf("nar: %q is not a sha256 SRI hash", s)
	}
	copy(sum[:], raw)
	return sum, nil
}

func writeNode(w io.Writer, path string) error {
	fi, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if err := writeStrings(w, "(", "type"); err != nil {
		return err
	}
	switch mode := fi.Mode(); {
	case mode.IsRegular():
		err = writeRegular(w, path, fi)
	case mode&os.ModeSymlink != 0:
		var target string
		if target, err = os.Readlink(path); err == nil {
			err = writeStrings(w, "symlink", "target", target)
		}
	case mode.IsDir():
		err = writeDir(w, path)
	default:
		err = fmt.Errorf("nar: %s: unsupported file type %s", path, mode.Type())
	}
	if err != nil {
		return err
	}
	return writeString(w, ")")
}

func writeRegular(w io.Writer, path string, fi os.FileInfo) error {
	if err := writeString(w, "regular"); err != nil {
		return err
	}
	// Nix records only the owner's execute bit.
	if fi.Mode()&0o100 != 0 {
		if err := writeStrings(w, "executable", ""); err != nil {
			return err
		}
	}
	if err := writeString(w, "contents"); err != nil {
		return err
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	size := fi.Size()
	if err := writeLen(w, uint64(size)); err != nil {
		return err
	}
	if _, err := io.CopyN(w, f, size); err != nil {
		return fmt.Errorf("nar: %s: %w", path, err)
	}
	return writePad(w, uint64(size))
}

func writeDir(w io.Writer, path string) error {
	if err := writeString(w, "directory"); err != nil {
		return err
	}
	// os.ReadDir sorts by file name, the order the format requires.
	entries, err := os.ReadDir(path)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err := writeStrings(w, "entry", "(", "name", e.Name(), "node"); err != nil {
			return err
		}
		if err := writeNode(w, filepath.Join(path, e.Name())); err != nil {
			return err
		}
		if err := writeString(w, ")"); err != nil {
			return err
		}
	}
	return nil
}

func writeStrings(w io.Writer, ss ...string) error {
	for _, s := range ss {
		if err := writeString(w, s); err != nil {
			return err
		}
	}
	return nil
}

// writeString writes a length-prefixed string padded to 8 bytes.
func writeString(w io.Writer, s string) error {
	if err := writeLen(w, uint64(len(s))); err != nil {
		return err
	}
	if _, err := io.WriteString(w, s); err != nil {
		return err
	}
	return writePad(w, uint64(len(s)))
}

func writeLen(w io.Writer, n uint64) error {
	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:], n)
	_, err := w.Write(buf[:])
	return err
}

func writePad(w io.Writer, n uint64) error {
	var zeros [8]byte
	_, err := w.Write(zeros[:(8-n%8)%8])
	return err
}
