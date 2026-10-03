// Command hello is a gonixgo integration fixture: a main package, a local
// library and third-party dependencies.
package main

import (
	"fmt"

	"github.com/fatih/color"

	"example.com/hello/internal/greet"
)

func main() {
	color.NoColor = true
	fmt.Println(color.GreenString(greet.Greeting("gonixgo")))
}
