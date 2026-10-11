package main

import (
	"fmt"
	"testing"

	"example.com/monorepo/lib"
)

// The main module's tests run.
func TestCaptured(t *testing.T) {
	if got := fmt.Sprint(lib.Captured()); got != "[3 3 3]" {
		t.Fatalf("lib.Captured() = %s, want [3 3 3]: lib compiles with its own go directive", got)
	}
}
