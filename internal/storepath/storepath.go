// Package storepath computes Nix store names and fixed-output store paths.
package storepath

import (
	"crypto/sha256"
	"encoding/hex"
)

// maxNameLen is the longest name Nix accepts in a store path.
const maxNameLen = 211

// SanitizeName maps s to a valid store path name: every byte outside
// [A-Za-z0-9+._?=-] becomes '-'. Names longer than Nix allows are cut and
// given a hash suffix so distinct inputs stay distinct.
func SanitizeName(s string) string {
	b := []byte(s)
	for i, c := range b {
		switch {
		case 'a' <= c && c <= 'z', 'A' <= c && c <= 'Z', '0' <= c && c <= '9':
		case c == '+', c == '-', c == '.', c == '_', c == '?', c == '=':
		default:
			b[i] = '-'
		}
	}
	if len(b) <= maxNameLen {
		return string(b)
	}
	sum := sha256.Sum256([]byte(s))
	return string(b[:maxNameLen-17]) + "-" + hex.EncodeToString(sum[:8])
}

// FixedOutput returns the store path of a fixed-output path with the given
// name whose content has the given recursive (NAR) SHA-256 hash. It is the
// path both `nix store add --mode nar` and a fixed-output derivation with
// outputHashMode = "recursive" produce.
func FixedOutput(storeDir, name string, narHash [32]byte) string {
	fingerprint := "source:sha256:" + hex.EncodeToString(narHash[:]) + ":" + storeDir + ":" + name
	digest := sha256.Sum256([]byte(fingerprint))
	var folded [20]byte
	for i, b := range digest {
		folded[i%len(folded)] ^= b
	}
	return storeDir + "/" + base32(folded[:]) + "-" + name
}

// alphabet is Nix's base-32 alphabet: no e, o, u or t.
const alphabet = "0123456789abcdfghijklmnpqrsvwxyz"

// base32 encodes b the way Nix does, least significant bits last.
func base32(b []byte) string {
	n := (len(b)*8-1)/5 + 1
	out := make([]byte, 0, n)
	for i := n - 1; i >= 0; i-- {
		bit := i * 5
		idx, off := bit/8, uint(bit%8)
		c := b[idx] >> off
		if idx+1 < len(b) {
			c |= b[idx+1] << (8 - off)
		}
		out = append(out, alphabet[c&0x1f])
	}
	return string(out)
}
