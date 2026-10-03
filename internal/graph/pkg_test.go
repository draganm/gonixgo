package graph

import (
	"reflect"
	"testing"
)

func TestLang(t *testing.T) {
	tests := []struct{ in, want string }{
		{"", "go1.16"},
		{"1.21", "go1.21"},
		{"1.21.3", "go1.21"},
		{"1.21rc1", "go1.21"},
		{"1.24.0", "go1.24"},
		{"1.9", "go1.9"},
	}
	for _, tt := range tests {
		if got := lang(tt.in); got != tt.want {
			t.Errorf("lang(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestExecName(t *testing.T) {
	tests := []struct{ in, want string }{
		{"example.com/app", "app"},
		{"example.com/app/cmd/server", "server"},
		{"example.com/app/v2", "app"},
		{"example.com/app/v10", "app"},
		{"example.com/tool/v1", "v1"},
		{"example.com/tool/v0", "v0"},
		{"example.com/tool/vendor", "vendor"},
		{"app", "app"},
	}
	for _, tt := range tests {
		if got := execName(tt.in); got != tt.want {
			t.Errorf("execName(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestEmbedMap(t *testing.T) {
	files := []string{
		"static/index.html",
		"static/css/site.css",
		"static/.hidden",
		"static/_draft/a.txt",
		"tmpl/a b.tmpl",
	}
	patterns := []string{"static", "all:static", "static/*", "tmpl/*.tmpl", "static/index.html"}
	want := map[string][]string{
		// A directory embeds its tree without dot and underscore names.
		"static": {"static/index.html", "static/css/site.css"},
		// all: keeps them.
		"all:static": {"static/index.html", "static/css/site.css", "static/.hidden", "static/_draft/a.txt"},
		// A glob names hidden files and directories directly, so they are
		// embedded; only names below a matched directory are dropped.
		"static/*":          {"static/index.html", "static/css/site.css", "static/.hidden", "static/_draft/a.txt"},
		"tmpl/*.tmpl":       {"tmpl/a b.tmpl"},
		"static/index.html": {"static/index.html"},
	}
	if got := embedMap(patterns, files); !reflect.DeepEqual(got, want) {
		t.Fatalf("embedMap =\n%v\nwant\n%v", got, want)
	}
	if got := embedMap(nil, nil); got != nil {
		t.Fatalf("embedMap(nil, nil) = %v, want nil", got)
	}
}
