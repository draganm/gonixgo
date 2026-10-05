// Package cxx is a cgo package with a C++ file, so its binaries are linked
// by the C++ compiler.
package cxx

/*
#include <stdlib.h>
#include "length.h"
*/
import "C"

import "unsafe"

// Length is computed by std::string.
func Length(s string) int {
	cs := C.CString(s)
	defer C.free(unsafe.Pointer(cs))
	return int(C.cxx_length(cs))
}
