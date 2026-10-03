// Command asmembed is a gonixgo integration fixture: assembly, embedded
// files, a value set with -X, and no third-party dependencies.
package main

import (
	"fmt"

	"example.com/asmembed/internal/add"
	"example.com/asmembed/internal/web"
)

var version = "unset"

func main() {
	fmt.Println(add.Add(1, 2), web.Index(), web.Names(), version)
}
