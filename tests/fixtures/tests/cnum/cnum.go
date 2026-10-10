// Package cnum is a cgo package. Its value is a macro that its
// packageOverrides entry defines through CGO_CFLAGS.
package cnum

/*
#ifndef FIXTURE_VALUE
#define FIXTURE_VALUE 0
#endif
static int value(void) { return FIXTURE_VALUE; }
*/
import "C"

// Value returns FIXTURE_VALUE.
func Value() int { return int(C.value()) }
