package storepath

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestFixedOutputGolden(t *testing.T) {
	// `nix store add --name gomod-example.com-mod-v1.0.0 --mode nar` on a
	// tree whose NAR hash is the one below printed this path.
	raw, err := base64.StdEncoding.DecodeString("nxE+RQ9BqCLb4kQ7dEgKNtudirqbZBN+Np7RzJT3Qws=")
	if err != nil {
		t.Fatal(err)
	}
	var sum [32]byte
	copy(sum[:], raw)
	const want = "/nix/store/13f9vr10w98dsl8n79bpqi28i0bg6awr-gomod-example.com-mod-v1.0.0"
	if got := FixedOutput("/nix/store", "gomod-example.com-mod-v1.0.0", sum); got != want {
		t.Fatalf("FixedOutput = %s, want %s", got, want)
	}
}

func TestSanitizeName(t *testing.T) {
	tests := []struct{ in, want string }{
		{"gomod-github.com/fatih/color-v1.18.0", "gomod-github.com-fatih-color-v1.18.0"},
		{"gomod-github.com/BurntSushi/toml-v1.6.0", "gomod-github.com-BurntSushi-toml-v1.6.0"},
		{"gopkg-gopkg.in/yaml.v3-v3.0.1", "gopkg-gopkg.in-yaml.v3-v3.0.1"},
		{"gomod-example.com/a-v0.0.0-20240101000000-abcdef123456+incompatible", "gomod-example.com-a-v0.0.0-20240101000000-abcdef123456+incompatible"},
		{"golocal-example.com/a b@c~d!e", "golocal-example.com-a-b-c-d-e"},
		{"golocal-example.com/päth", "golocal-example.com-p--th"},
	}
	for _, tt := range tests {
		if got := SanitizeName(tt.in); got != tt.want {
			t.Errorf("SanitizeName(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestSanitizeNameBoundsLength(t *testing.T) {
	a := SanitizeName("gopkg-" + strings.Repeat("a", 300) + "/x")
	b := SanitizeName("gopkg-" + strings.Repeat("a", 300) + "/y")
	if len(a) > 211 || len(b) > 211 {
		t.Fatalf("lengths %d and %d exceed 211", len(a), len(b))
	}
	if a == b {
		t.Fatal("different inputs truncated to the same name")
	}
	if a != SanitizeName("gopkg-"+strings.Repeat("a", 300)+"/x") {
		t.Fatal("SanitizeName is not deterministic")
	}
}
