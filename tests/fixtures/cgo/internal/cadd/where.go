package cadd

/*
static const char *where(void) { return __FILE__; }
*/
import "C"

// Where is the name the C compiler recorded for this file. Built with
// -trimpath it is the same wherever the source is, and it names no
// directory of the machine that built it.
func Where() string { return C.GoString(C.where()) }
