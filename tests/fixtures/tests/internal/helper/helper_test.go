package helper

import "testing"

func TestNeverRuns(t *testing.T) {
	t.Fatal("a package that only tests import was tested")
}
