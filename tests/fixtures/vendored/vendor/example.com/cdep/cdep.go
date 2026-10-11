// Package cdep is a cgo package that go.mod replaces by a directory and
// go mod vendor copies into vendor/.
package cdep

// static const char *where(void) { return __FILE__; }
import "C"

// Where returns the path the C compiler recorded for this file.
func Where() string { return C.GoString(C.where()) }
