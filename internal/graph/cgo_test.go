package graph

import (
	"reflect"
	"testing"
)

func TestRestoreSrcDir(t *testing.T) {
	tests := []struct {
		in, want []string
	}{
		{nil, nil},
		{[]string{"-O2", "-lm"}, []string{"-O2", "-lm"}},
		{[]string{"-I/mod/pkg/include"}, []string{"-I${SRCDIR}/include"}},
		{[]string{"-I/mod/pkg"}, []string{"-I${SRCDIR}"}},
		{[]string{"-DA=/mod/pkg/a:/mod/pkg/b"}, []string{"-DA=${SRCDIR}/a:${SRCDIR}/b"}},
		{[]string{"-I/mod/pkg/../other"}, []string{"-I${SRCDIR}/../other"}},
	}
	for _, tt := range tests {
		if got := restoreSrcDir(tt.in, "/mod/pkg"); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("restoreSrcDir(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestSrcDirRefs(t *testing.T) {
	tests := []struct {
		in   string
		want []string
	}{
		{"-O2", nil},
		{"-I${SRCDIR}/include", []string{"/include"}},
		{"${SRCDIR}", []string{""}},
		{"${SRCDIR}/libfoo.a", []string{"/libfoo.a"}},
		{"-Wl,-rpath,${SRCDIR}/lib,-z,now", []string{"/lib"}},
		{`-DX="${SRCDIR}/data"`, []string{"/data"}},
		{"-DA=${SRCDIR}/a:${SRCDIR}/b", []string{"/a", "/b"}},
		{"-I${SRCDIR}/../shared", []string{"/../shared"}},
	}
	for _, tt := range tests {
		if got := srcDirRefs([]string{tt.in}); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("srcDirRefs(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
	if got := srcDirRefs([]string{"-I${SRCDIR}/a", "-L${SRCDIR}/b"}); !reflect.DeepEqual(got, []string{"/a", "/b"}) {
		t.Errorf("srcDirRefs over two flags = %q", got)
	}
}
