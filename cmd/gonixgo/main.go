// Command gonixgo resolves Go package graphs into Nix expressions and runs
// the build steps of the derivations those expressions describe.
package main

import (
	"fmt"
	"io"
	"os"
)

// command is one gonixgo subcommand. args are the arguments after its name.
type command func(args []string, stdout io.Writer) error

// commands is filled by the init function of each subcommand's file.
var commands = map[string]command{}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: gonixgo <resolve|compile|link|fetch> ...")
		return 2
	}
	cmd, ok := commands[args[0]]
	if !ok {
		fmt.Fprintf(stderr, "gonixgo: unknown command %q\n", args[0])
		return 2
	}
	if err := cmd(args[1:], stdout); err != nil {
		fmt.Fprintf(stderr, "gonixgo %s: %v\n", args[0], err)
		return 1
	}
	return 0
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}
