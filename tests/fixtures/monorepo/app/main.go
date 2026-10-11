// Command app prints the source paths the compiler recorded for its own
// file, for a sibling module's and for a replaced dependency's, then what
// the sibling module's closures saw.
package main

import (
	"fmt"
	"reflect"
	"runtime"

	"example.com/monorepo/lib"
	"github.com/google/go-cmp/cmp"
)

// fileOf returns the source file the compiler recorded for function f.
func fileOf(f any) string {
	fn := runtime.FuncForPC(reflect.ValueOf(f).Pointer())
	file, _ := fn.FileLine(fn.Entry())
	return file
}

func main() {
	_, file, _, _ := runtime.Caller(0)
	fmt.Println(file, lib.Where(), fileOf(cmp.Equal), lib.Captured())
}
