package cnum

import "testing"

// The package's entry reaches the test's copy of it too.
func TestValue(t *testing.T) {
	if got := Value(); got != 42 {
		t.Fatalf("Value() = %d, want 42", got)
	}
}
