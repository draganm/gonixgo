// Command app prints the source paths the compiler recorded for its own
// file, for lib's, a module of its workspace, and for go-cmp's Equal,
// which lib requires, then what lib's closures saw.
package main

import (
	"fmt"
	"runtime"

	"example.com/work/lib"
)

func main() {
	_, file, _, _ := runtime.Caller(0)
	fmt.Println(file, lib.Where(), lib.CmpFile(), lib.Captured())
}
