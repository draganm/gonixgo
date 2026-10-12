package main

import (
	"fmt"
	"testing"

	"example.com/work/lib"
)

// The tests of every workspace module run.
func TestCaptured(t *testing.T) {
	if got := fmt.Sprint(lib.Captured()); got != "[3 3 3]" {
		t.Fatalf("lib.Captured() = %s, want [3 3 3]: lib compiles with its own go directive", got)
	}
}
