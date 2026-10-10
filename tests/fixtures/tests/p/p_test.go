package p_test

import (
	_ "embed"
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"

	"example.com/tests/internal/helper"
	"example.com/tests/p"
	"example.com/tests/q"
)

//go:embed testdata/xembedded.txt
var xembedded string

// q imports p, so this binary has q recompiled against p's test copy.
func TestDouble(t *testing.T) {
	if diff := cmp.Diff(helper.Want(4), q.Double(2)); diff != "" {
		t.Fatal(diff)
	}
}

func TestXEmbed(t *testing.T) {
	if xembedded != "xembedded\n" {
		t.Fatalf("xembedded = %q", xembedded)
	}
}

func ExampleAdd() {
	fmt.Println(p.Add(2, 3))
	// Output: 5
}
