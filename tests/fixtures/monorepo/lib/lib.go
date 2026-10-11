// Package lib is a sibling module that the program reaches through a
// directory replace.
package lib

import "runtime"

// Where returns the path the compiler recorded for this file.
func Where() string {
	_, file, _, _ := runtime.Caller(0)
	return file
}

// Captured collects a loop variable through closures. Under go 1.21, this
// module's version, every closure sees the variable's last value.
func Captured() []int {
	var fs []func() int
	for i := 0; i < 3; i++ {
		fs = append(fs, func() int { return i })
	}
	var out []int
	for _, f := range fs {
		out = append(out, f())
	}
	return out
}
