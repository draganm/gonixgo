// Package zstd uses libzstd, found through pkg-config. Both come from
// packageOverrides.
package zstd

/*
#cgo pkg-config: libzstd
#include <stdlib.h>
#include <zstd.h>
*/
import "C"

import "unsafe"

// RoundTrip compresses s and decompresses it again.
func RoundTrip(s string) string {
	src := C.CString(s)
	defer C.free(unsafe.Pointer(src))
	n := C.size_t(len(s))

	bound := C.ZSTD_compressBound(n)
	packed := C.malloc(bound)
	defer C.free(packed)
	size := C.ZSTD_compress(packed, bound, unsafe.Pointer(src), n, 3)
	if C.ZSTD_isError(size) != 0 {
		return "compress failed"
	}

	plain := C.malloc(n + 1)
	defer C.free(plain)
	got := C.ZSTD_decompress(plain, n, packed, size)
	if C.ZSTD_isError(got) != 0 {
		return "decompress failed"
	}
	return C.GoStringN((*C.char)(plain), C.int(got))
}
