package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func tempStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	return NewStore(Paths{ConfigDir: filepath.Join(dir, "config"), CacheDir: filepath.Join(dir, "cache")})
}

func TestSavedQueriesFirstRunCreatesStarter(t *testing.T) {
	s := tempStore(t)
	queries, err := s.SavedQueries()
	if err != nil {
		t.Fatal(err)
	}
	if len(queries) < 3 {
		t.Fatalf("starter library too small: %d", len(queries))
	}
	if _, err := os.Stat(s.queriesPath()); err != nil {
		t.Fatal("starter file not written")
	}
	foundTooling := false
	for _, q := range queries {
		if q.Name == "" || q.Query == "" {
			t.Errorf("incomplete starter query: %+v", q)
		}
		foundTooling = foundTooling || q.Tooling
	}
	if !foundTooling {
		t.Error("starter library should demonstrate a tooling query")
	}
}

func TestSavedQueriesUserFile(t *testing.T) {
	s := tempStore(t)
	os.MkdirAll(s.paths.ConfigDir, 0o755)
	os.WriteFile(s.queriesPath(), []byte("queries:\n  - name: Mine\n    query: SELECT Id FROM Foo__c\n"), 0o644)
	queries, err := s.SavedQueries()
	if err != nil {
		t.Fatal(err)
	}
	if len(queries) != 1 || queries[0].Name != "Mine" {
		t.Fatalf("user file not honored: %+v", queries)
	}
}

func TestSavedQueriesMalformedYAML(t *testing.T) {
	s := tempStore(t)
	os.MkdirAll(s.paths.ConfigDir, 0o755)
	os.WriteFile(s.queriesPath(), []byte("queries: [unclosed"), 0o644)
	if _, err := s.SavedQueries(); err == nil {
		t.Fatal("malformed YAML should error, not panic or silently succeed")
	}
}

func TestHistoryRoundTripDedupAndCap(t *testing.T) {
	s := tempStore(t)
	if h := s.History(); h != nil {
		t.Fatalf("empty history should be nil, got %v", h)
	}
	s.AppendHistory("SELECT 1")
	s.AppendHistory("SELECT 2")
	entries, err := s.AppendHistory("SELECT 1")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0] != "SELECT 1" || entries[1] != "SELECT 2" {
		t.Fatalf("dedup/move-to-front wrong: %v", entries)
	}
	if got := s.History(); len(got) != 2 || got[0] != "SELECT 1" {
		t.Fatalf("persisted history wrong: %v", got)
	}

	for i := 0; i < historyCap+50; i++ {
		s.AppendHistory(string(rune('a'+i%26)) + string(rune('0'+i%10)) + json.Number(time.Now().String()).String() + string(rune(i)))
	}
	if got := s.History(); len(got) > historyCap {
		t.Fatalf("history exceeds cap: %d", len(got))
	}
}

func TestConcurrentAppendHistorySafe(t *testing.T) {
	s := tempStore(t)
	done := make(chan struct{})
	for i := 0; i < 8; i++ {
		go func(n int) {
			defer func() { done <- struct{}{} }()
			for j := 0; j < 20; j++ {
				s.AppendHistory("SELECT " + string(rune('A'+n)))
			}
		}(i)
	}
	for i := 0; i < 8; i++ {
		<-done
	}
	got := s.History()
	if len(got) != 8 {
		t.Fatalf("all 8 distinct queries must survive concurrent writes, got %d: %v", len(got), got)
	}
}

func TestCacheKeyCannotEscapeCacheDir(t *testing.T) {
	s := tempStore(t)
	hostile := "describe-00D1-../../../../victim/pwned"
	s.CachePut(hostile, map[string]string{"x": "y"})
	outside := filepath.Join(filepath.Dir(s.paths.CacheDir), "victim")
	if _, err := os.Stat(outside); err == nil {
		t.Fatalf("cache write escaped to %s", outside)
	}
	entries, err := os.ReadDir(s.paths.CacheDir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("expected one file inside the cache dir, got %v (%v)", entries, err)
	}
	var back map[string]string
	if !s.CacheGet(hostile, time.Minute, &back) || back["x"] != "y" {
		t.Fatal("hashed keys must still round-trip")
	}
}

func TestPersistedFilesArePrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		// Windows has no Unix mode bits; Go reports 0666/0777 regardless of
		// the ACLs that actually govern access under the user profile.
		t.Skip("permission bits are not meaningful on Windows")
	}
	s := tempStore(t)
	s.SavedQueries()
	s.AppendHistory("SELECT Id FROM Contact WHERE Email = 'a@b.c'")
	s.CachePut("k", 1)
	for _, p := range []string{s.queriesPath(), s.historyPath()} {
		info, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Errorf("%s has mode %o, want 600 — it can hold sensitive SOQL", p, perm)
		}
	}
	info, err := os.Stat(s.paths.ConfigDir)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Errorf("config dir has mode %o, want 700", perm)
	}
}

func TestCacheRoundTripAndTTL(t *testing.T) {
	s := tempStore(t)
	type payload struct{ Names []string }
	if ok := s.CacheGet("describe/foo", time.Minute, &payload{}); ok {
		t.Fatal("cache miss expected on empty cache")
	}
	s.CachePut("describe/foo", payload{Names: []string{"Account"}})
	var got payload
	if !s.CacheGet("describe/foo", time.Minute, &got) || got.Names[0] != "Account" {
		t.Fatalf("cache round trip failed: %+v", got)
	}
	if s.CacheGet("describe/foo", 0, &got) {
		t.Fatal("expired entry should miss")
	}
}
