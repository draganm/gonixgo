//go:build !darwin

// Package objc is a cgo package with an Objective-C file on darwin and a
// pure-Go stand-in elsewhere.
package objc

// Length stands in for the Objective-C implementation.
func Length(s string) int { return len(s) }
