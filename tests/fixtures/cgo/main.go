// Command cgofix is a gonixgo integration fixture: cgo packages, local and
// third-party, with C, C++, Objective-C and assembly, and two libraries
// that packageOverrides supplies.
package main

import (
	"fmt"

	pointer "github.com/mattn/go-pointer"

	"example.com/cgofix/internal/cadd"
	"example.com/cgofix/internal/cxx"
	"example.com/cgofix/internal/lz4"
	"example.com/cgofix/internal/objc"
	"example.com/cgofix/internal/pure"
	"example.com/cgofix/internal/zstd"
)

func main() {
	p := pointer.Save("saved")
	saved := pointer.Restore(p).(string)
	pointer.Unref(p)
	fmt.Println(cadd.Add(1, 2), cadd.Twice(4), cadd.Seven(), cxx.Length("four"), objc.Length("objc"),
		zstd.RoundTrip("zstd"), lz4.Bound(100) > 100, saved, pure.Name(), cadd.Where())
}
