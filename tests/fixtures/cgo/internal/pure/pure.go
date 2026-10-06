// Package pure is a pure-Go package next to cgo packages: it must keep
// building without a C toolchain.
package pure

// Name names the package.
func Name() string { return "pure" }
