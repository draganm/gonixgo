// Package cadd is a cgo package with a C file, a header in a directory
// that only a #cgo directive names, a Go function that C calls, and an
// assembly file for the C compiler.
package cadd

/*
#cgo CFLAGS: -DBONUS=0 -I${SRCDIR}/include
#include "add.h"
*/
import "C"

// Add adds in C.
func Add(a, b int) int { return int(C.add(C.int(a), C.int(b))) }

//export goTwice
func goTwice(x C.int) C.int { return 2 * x }

// Twice doubles x in Go, by way of C.
func Twice(x int) int { return int(C.call_twice(C.int(x))) }

// Seven is implemented in assembly.
func Seven() int { return int(C.asm_seven()) }
