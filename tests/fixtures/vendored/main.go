// Command vendored prints the source paths the compiler recorded for
// go-cmp's Equal and for cdep's C code, both built from vendor/.
package main

import (
	"fmt"
	"reflect"
	"runtime"

	"example.com/cdep"
	"github.com/google/go-cmp/cmp"
)

func main() {
	fn := runtime.FuncForPC(reflect.ValueOf(cmp.Equal).Pointer())
	file, _ := fn.FileLine(fn.Entry())
	fmt.Println(file, cdep.Where())
}
