package main

import "testing"

func TestLine(t *testing.T) {
	if got, want := line(), "4 xonly 42"; got != want {
		t.Fatalf("line() = %q, want %q", got, want)
	}
}

// The link marks a test binary, so testing.Testing reports true.
func TestTesting(t *testing.T) {
	if !testing.Testing() {
		t.Fatal("testing.Testing() is false")
	}
}
