package config

import (
	"encoding/json"
	"os"
	"path/filepath"
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
