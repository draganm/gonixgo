package gotool

import (
	"reflect"
	"testing"
)

func TestSplitFlags(t *testing.T) {
	tests := []struct {
		in   []string
		want []string
	}{
		{nil, nil},
		{[]string{"-s", "-w"}, []string{"-s", "-w"}},
		{[]string{"-s -w"}, []string{"-s", "-w"}},
		{[]string{"-X main.version=1.2.3"}, []string{"-X", "main.version=1.2.3"}},
		{[]string{"-X 'main.msg=hello world'"}, []string{"-X", "main.msg=hello world"}},
		{[]string{`-X "main.msg=it's"`}, []string{"-X", "main.msg=it's"}},
		{[]string{"  -s  \t -w  "}, []string{"-s", "-w"}},
		{[]string{"-X", "main.v=1"}, []string{"-X", "main.v=1"}},
	}
	for _, tt := range tests {
		got, err := SplitFlags(tt.in)
		if err != nil {
			t.Errorf("SplitFlags(%q): %v", tt.in, err)
			continue
		}
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("SplitFlags(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
	if _, err := SplitFlags([]string{"-X 'main.msg=unterminated"}); err == nil {
		t.Error("SplitFlags accepted an unterminated quote")
	}
}
