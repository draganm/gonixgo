package modcache

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/draganm/gonixgo/internal/graph"
	"github.com/draganm/gonixgo/internal/nar"
	"github.com/draganm/gonixgo/internal/storepath"
	"github.com/draganm/gonixgo/internal/testutil"
)

func module(t *testing.T, key string, files map[string]string) *graph.Module {
	t.Helper()
	path, version, _ := strings.Cut(key, "@")
	return &graph.Module{
		Key: key, Path: path, Version: version, Sum: "h1:" + key,
		Dir:  testutil.WriteTree(t, files),
		Name: storepath.SanitizeName("gomod-" + path + "-" + version),
	}
}

func sri(t *testing.T, dir string) string {
	t.Helper()
	sum, err := nar.Hash(dir)
	if err != nil {
		t.Fatal(err)
	}
	return nar.SRI(sum)
}

// fakeStore answers Add with the path Nix would give and records the call.
type fakeStore struct {
	mu    sync.Mutex
	dir   string
	added []string
}

func (s *fakeStore) add(name, dir string) (string, error) {
	sum, err := nar.Hash(dir)
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.added = append(s.added, name)
	return storepath.FixedOutput(s.dir, name, sum), nil
}

func TestParseGoSum(t *testing.T) {
	const goSum = `github.com/fatih/color v1.18.0 h1:S8gINlzdQ840/4pfAwic/ZE0djQEH3wM94VfqLTZcOM=
github.com/fatih/color v1.18.0/go.mod h1:4FelSpRwEGDpQ12mAdzqdOukCy4u8WUtOY6lkT/6HfU=

malformed line
golang.org/x/sys v0.25.0 h1:r+8e+loiHxRqhXVl6ML1nO3l1+oFoWbnlu2Ehimmi34=
`
	want := map[string]string{
		"github.com/fatih/color@v1.18.0": "h1:S8gINlzdQ840/4pfAwic/ZE0djQEH3wM94VfqLTZcOM=",
		"golang.org/x/sys@v0.25.0":       "h1:r+8e+loiHxRqhXVl6ML1nO3l1+oFoWbnlu2Ehimmi34=",
	}
	if got := ParseGoSum([]byte(goSum)); !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseGoSum = %v, want %v", got, want)
	}
}

func TestPrepareHashesAndSeeds(t *testing.T) {
	a := module(t, "example.com/a@v1.0.0", map[string]string{"a.go": "package a\n"})
	b := module(t, "example.com/b@v2.0.0", map[string]string{"b.go": "package b\n", "sub/c.go": "package sub\n"})
	store := &fakeStore{dir: t.TempDir()}
	s := &Seeder{StoreDir: store.dir, CacheDir: t.TempDir(), Add: store.add}

	if err := s.Prepare([]*graph.Module{a, b}); err != nil {
		t.Fatal(err)
	}
	if a.Hash != sri(t, a.Dir) || b.Hash != sri(t, b.Dir) {
		t.Fatalf("hashes = %q, %q", a.Hash, b.Hash)
	}
	sort.Strings(store.added)
	if want := []string{"gomod-example.com-a-v1.0.0", "gomod-example.com-b-v2.0.0"}; !reflect.DeepEqual(store.added, want) {
		t.Fatalf("added = %v, want %v", store.added, want)
	}
}

func TestPrepareNoModules(t *testing.T) {
	if err := (&Seeder{StoreDir: t.TempDir()}).Prepare(nil); err != nil {
		t.Fatal(err)
	}
}

func TestPrepareSkipsPathsAlreadyInStore(t *testing.T) {
	m := module(t, "example.com/a@v1.0.0", map[string]string{"a.go": "package a\n"})
	store := &fakeStore{dir: t.TempDir()}
	sum, err := nar.Hash(m.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(storepath.FixedOutput(store.dir, m.Name, sum), 0o755); err != nil {
		t.Fatal(err)
	}
	s := &Seeder{StoreDir: store.dir, Add: store.add}
	if err := s.Prepare([]*graph.Module{m}); err != nil {
		t.Fatal(err)
	}
	if len(store.added) != 0 {
		t.Fatalf("added = %v, want nothing", store.added)
	}
}

func TestPrepareUsesCache(t *testing.T) {
	m := module(t, "example.com/a@v1.0.0", map[string]string{"a.go": "package a\n"})
	s := &Seeder{StoreDir: t.TempDir(), CacheDir: t.TempDir()}
	if err := s.Prepare([]*graph.Module{m}); err != nil {
		t.Fatal(err)
	}
	first := m.Hash

	// Without pre-seeding nothing checks the directory again, so a changed
	// directory still yields the cached hash.
	if err := os.WriteFile(filepath.Join(m.Dir, "new.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m.Hash = ""
	if err := s.Prepare([]*graph.Module{m}); err != nil {
		t.Fatal(err)
	}
	if m.Hash != first {
		t.Fatalf("hash = %s, want the cached %s", m.Hash, first)
	}
}

func TestPrepareRecoversFromStaleCache(t *testing.T) {
	m := module(t, "example.com/a@v1.0.0", map[string]string{"a.go": "package a\n"})
	cache := t.TempDir()
	if err := (&Seeder{StoreDir: t.TempDir(), CacheDir: cache}).Prepare([]*graph.Module{m}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(m.Dir, "new.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	store := &fakeStore{dir: t.TempDir()}
	if err := (&Seeder{StoreDir: store.dir, CacheDir: cache, Add: store.add}).Prepare([]*graph.Module{m}); err != nil {
		t.Fatal(err)
	}
	want := sri(t, m.Dir)
	if m.Hash != want {
		t.Fatalf("hash = %s, want the directory's current %s", m.Hash, want)
	}

	// The cache entry was corrected.
	m.Hash = ""
	if err := (&Seeder{StoreDir: t.TempDir(), CacheDir: cache}).Prepare([]*graph.Module{m}); err != nil {
		t.Fatal(err)
	}
	if m.Hash != want {
		t.Fatalf("cached hash = %s, want %s", m.Hash, want)
	}
}

func TestPrepareWithoutSumDoesNotCache(t *testing.T) {
	m := module(t, "example.com/a@v1.0.0", map[string]string{"a.go": "package a\n"})
	m.Sum = ""
	cache := t.TempDir()
	if err := (&Seeder{StoreDir: t.TempDir(), CacheDir: cache}).Prepare([]*graph.Module{m}); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(cache)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 || m.Hash == "" {
		t.Fatalf("cache has %d entries, hash = %q", len(entries), m.Hash)
	}
}

func TestPrepareWarnsWhenAddFails(t *testing.T) {
	m := module(t, "example.com/a@v1.0.0", map[string]string{"a.go": "package a\n"})
	var warnings []string
	s := &Seeder{
		StoreDir: t.TempDir(),
		Add:      func(string, string) (string, error) { return "", errors.New("daemon unreachable") },
		Warn:     func(format string, args ...any) { warnings = append(warnings, format) },
	}
	if err := s.Prepare([]*graph.Module{m}); err != nil {
		t.Fatalf("Prepare failed, want a warning: %v", err)
	}
	if len(warnings) != 1 || m.Hash != sri(t, m.Dir) {
		t.Fatalf("warnings = %v, hash = %q", warnings, m.Hash)
	}
}

func TestPrepareFailsOnMismatch(t *testing.T) {
	m := module(t, "example.com/a@v1.0.0", map[string]string{"a.go": "package a\n"})
	s := &Seeder{StoreDir: t.TempDir(), Add: func(string, string) (string, error) { return "/elsewhere/x", nil }}
	err := s.Prepare([]*graph.Module{m})
	if err == nil || !strings.Contains(err.Error(), "example.com/a@v1.0.0") || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("err = %v, want a mismatch naming the module", err)
	}
}

func TestPrepareMissingDir(t *testing.T) {
	m := &graph.Module{Key: "example.com/a@v1.0.0", Dir: filepath.Join(t.TempDir(), "absent"), Name: "gomod-a"}
	if err := (&Seeder{StoreDir: t.TempDir()}).Prepare([]*graph.Module{m}); err == nil {
		t.Fatal("Prepare of a missing directory succeeded")
	}
}

func TestNixStoreAdd(t *testing.T) {
	if _, err := exec.LookPath("nix"); err != nil {
		t.Skip("nix not on PATH")
	}
	dir := testutil.WriteTree(t, map[string]string{"go.mod": "module example.com/seedtest\n", "a.go": "package a\n"})
	const name = "gomod-example.com-seedtest-v0.0.1"
	got, err := NixStoreAdd(name, dir)
	if err != nil {
		t.Fatal(err)
	}
	sum, err := nar.Hash(dir)
	if err != nil {
		t.Fatal(err)
	}
	if want := storepath.FixedOutput(filepath.Dir(got), name, sum); got != want {
		t.Fatalf("nix store add gave %s, FixedOutput computes %s", got, want)
	}
}
