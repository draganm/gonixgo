package lib

import "testing"

// The tests of every workspace module run.
func TestWhere(t *testing.T) {
	if Where() == "" {
		t.Fatal("no file recorded")
	}
}
