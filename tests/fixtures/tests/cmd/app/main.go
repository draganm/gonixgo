// Command app prints a line from each package it uses.
package main

import (
	"fmt"

	"example.com/tests/cnum"
	"example.com/tests/q"
	"example.com/tests/xonly"
)

func line() string {
	return fmt.Sprintf("%d %s %d", q.Double(2), xonly.Name(), cnum.Value())
}

func main() { fmt.Println(line()) }
