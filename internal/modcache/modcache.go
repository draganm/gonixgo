// Package modcache hashes module directories from the Go module cache and
// pre-seeds them into the Nix store, so the fetch derivations for them are
// already built.
package modcache

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/draganm/gonixgo/internal/graph"
	"github.com/draganm/gonixgo/internal/nar"
	"github.com/draganm/gonixgo/internal/storepath"
)

// ParseGoSum returns the h1: sum of each module zip in a go.sum file, keyed
// by path@version. The /go.mod lines are skipped.
func ParseGoSum(data []byte) map[string]string {
	sums := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Fields(line)
		if len(f) != 3 || strings.HasSuffix(f[1], "/go.mod") {
			continue
		}
		sums[f[0]+"@"+f[1]] = f[2]
	}
	return sums
}

// Seeder computes module hashes and adds module directories to the store.
type Seeder struct {
	StoreDir string
	// CacheDir holds computed hashes. "" disables the cache.
	CacheDir string
	// Add puts dir in the store under name and returns its store path.
	// nil disables pre-seeding.
	Add func(name, dir string) (string, error)
	// Warn reports problems that do not stop evaluation. nil discards them.
	Warn func(format string, args ...any)
}

// Prepare fills in the Hash of every module and, when pre-seeding is on,
// makes sure each module's store path exists.
func (s *Seeder) Prepare(mods []*graph.Module) error {
	jobs := make(chan *graph.Module)
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		problems []string
	)
	for range min(runtime.NumCPU(), len(mods)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for m := range jobs {
				if err := s.prepare(m); err != nil {
					mu.Lock()
					problems = append(problems, fmt.Sprintf("module %s: %v", m.Key, err))
					mu.Unlock()
				}
			}
		}()
	}
	for _, m := range mods {
		jobs <- m
	}
	close(jobs)
	wg.Wait()
	if len(problems) == 0 {
		return nil
	}
	sort.Strings(problems)
	return errors.New(strings.Join(problems, "\n"))
}

func (s *Seeder) prepare(m *graph.Module) error {
	sum, cached, err := s.hash(m)
	if err != nil {
		return err
	}
	path := storepath.FixedOutput(s.StoreDir, m.Name, sum)
	if _, statErr := os.Lstat(path); s.Add != nil && statErr != nil {
		added, err := s.Add(m.Name, m.Dir)
		switch {
		case err != nil:
			s.warn("gonixgo: could not add %s to the store, it will be fetched at build time: %v", m.Key, err)
		case added != path && cached:
			// The cached hash no longer describes the directory.
			if sum, err = nar.Hash(m.Dir); err != nil {
				return err
			}
			if storepath.FixedOutput(s.StoreDir, m.Name, sum) != added {
				return fmt.Errorf("store path %s does not match the hash of %s", added, m.Dir)
			}
			s.remember(m, sum)
		case added != path:
			return fmt.Errorf("store path %s does not match the hash of %s; the module cache changed while it was read", added, m.Dir)
		}
	}
	m.Hash = nar.SRI(sum)
	return nil
}

// hash returns the NAR hash of the module's directory and whether it came
// from the cache.
func (s *Seeder) hash(m *graph.Module) (sum [32]byte, cached bool, err error) {
	if file := s.cacheFile(m); file != "" {
		if data, readErr := os.ReadFile(file); readErr == nil {
			if sum, parseErr := nar.ParseSRI(strings.TrimSpace(string(data))); parseErr == nil {
				return sum, true, nil
			}
		}
	}
	if sum, err = nar.Hash(m.Dir); err != nil {
		return sum, false, err
	}
	s.remember(m, sum)
	return sum, false, nil
}

// cacheFile names the cache entry for m. A module without a go.sum line is
// not cached: nothing ties its content to its version.
func (s *Seeder) cacheFile(m *graph.Module) string {
	if s.CacheDir == "" || m.Sum == "" {
		return ""
	}
	key := sha256.Sum256([]byte("v1\n" + m.Key + "\n" + m.Sum + "\n"))
	return filepath.Join(s.CacheDir, hex.EncodeToString(key[:]))
}

// remember stores sum in the cache. Failures are ignored: the cache only
// saves time.
func (s *Seeder) remember(m *graph.Module, sum [32]byte) {
	file := s.cacheFile(m)
	if file == "" {
		return
	}
	if err := os.MkdirAll(s.CacheDir, 0o755); err != nil {
		return
	}
	tmp, err := os.CreateTemp(s.CacheDir, "tmp-*")
	if err != nil {
		return
	}
	_, writeErr := tmp.WriteString(nar.SRI(sum) + "\n")
	closeErr := tmp.Close()
	if writeErr != nil || closeErr != nil || os.Rename(tmp.Name(), file) != nil {
		os.Remove(tmp.Name())
	}
}

func (s *Seeder) warn(format string, args ...any) {
	if s.Warn != nil {
		s.Warn(format, args...)
	}
}

// NixStoreAdd adds dir to the Nix store as a recursive SHA-256 path named
// name and returns the store path.
func NixStoreAdd(name, dir string) (string, error) {
	cmd := exec.Command("nix", "--extra-experimental-features", "nix-command",
		"store", "add", "--name", name, "--mode", "nar", "--hash-algo", "sha256", dir)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("nix store add: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(string(out)), nil
}
