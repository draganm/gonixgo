// Package objc is a cgo package with an Objective-C file on darwin and a
// pure-Go stand-in elsewhere.
package objc

/*
#cgo LDFLAGS: -framework Foundation
#include <stdlib.h>

int objc_length(const char *s);
*/
import "C"

import "unsafe"

// Length is computed by NSString.
func Length(s string) int {
	cs := C.CString(s)
	defer C.free(unsafe.Pointer(cs))
	return int(C.objc_length(cs))
}
