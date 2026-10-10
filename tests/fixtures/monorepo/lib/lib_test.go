package lib

import "testing"

// The tests of a module behind a directory replace are not the program's,
// and gonixgo does not run them.
func TestNotRun(t *testing.T) {
	t.Fatal("the tests of a replaced module ran")
}
