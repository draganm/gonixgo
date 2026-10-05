// Package lz4 uses liblz4 with a plain -l flag: no pkg-config tells the
// linker where the library is, so it links only if the buildInputs of its
// packageOverrides entry reach the link of the binary.
package lz4

/*
#cgo LDFLAGS: -llz4
#include <lz4.h>
*/
import "C"

// Bound is liblz4's worst-case compressed size of n bytes.
func Bound(n int) int { return int(C.LZ4_compressBound(C.int(n))) }
