// Package config owns everything sf9s persists: saved queries, query
// history, and the describe cache. Tokens are deliberately never stored.
package config

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

const appDir = "sf9s"

// Paths resolves the per-user directories sf9s uses.
type Paths struct {
	ConfigDir string
	CacheDir  string
}

func DefaultPaths() (Paths, error) {
	cfg, err := os.UserConfigDir()
	if err != nil {
		return Paths{}, err
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return Paths{}, err
	}
	return Paths{
		ConfigDir: filepath.Join(cfg, appDir),
		CacheDir:  filepath.Join(cache, appDir),
	}, nil
}

// SavedQuery is one entry in the user's query library.
type SavedQuery struct {
	Name    string `yaml:"name"`
	Query   string `yaml:"query"`
	Tooling bool   `yaml:"tooling,omitempty"`
}

const starterQueries = `# sf9s saved queries — add your own; they appear in the query view picker (ctrl+s).
# tooling: true runs the query against the Tooling API.
queries:
  - name: Recent users
    query: SELECT Id, Name, Username, Profile.Name, LastLoginDate FROM User WHERE IsActive = true ORDER BY LastLoginDate DESC NULLS LAST LIMIT 50
  - name: Recently modified Apex classes
    query: SELECT Id, Name, LastModifiedBy.Name, LastModifiedDate, ApiVersion, Status FROM ApexClass ORDER BY LastModifiedDate DESC LIMIT 50
    tooling: true
  - name: Org info
    query: SELECT Id, Name, OrganizationType, IsSandbox, InstanceName, NamespacePrefix, TrialExpirationDate FROM Organization
  - name: Record counts by object
    query: SELECT COUNT(Id) records, COUNT_DISTINCT(OwnerId) owners FROM Account
  - name: Failed login attempts (last 7 days)
    query: SELECT UserId, User.Name, LoginTime, Status, SourceIp, Application FROM LoginHistory WHERE Status != 'Success' AND LoginTime = LAST_N_DAYS:7 ORDER BY LoginTime DESC LIMIT 100
`

// ExportDir is where result exports are written. Writing to the working
// directory drops files into whatever repository you happened to launch from,
// so prefer ~/Downloads when it exists.
func (s *Store) ExportDir() string {
	if dir := os.Getenv("SF9S_EXPORT_DIR"); dir != "" {
		return dir
	}
	if home, err := os.UserHomeDir(); err == nil {
		downloads := filepath.Join(home, "Downloads")
		if info, err := os.Stat(downloads); err == nil && info.IsDir() {
			return downloads
		}
	}
	if cwd, err := os.Getwd(); err == nil {
		return cwd
	}
	return "."
}

// Store is the entry point for all persisted state. History writes are
// serialized and atomic: concurrent queries must never corrupt or truncate
// the user's history file.
type Store struct {
	paths  Paths
	histMu sync.Mutex
}

func NewStore(paths Paths) *Store {
	return &Store{paths: paths}
}

func (s *Store) queriesPath() string { return filepath.Join(s.paths.ConfigDir, "queries.yaml") }
func (s *Store) historyPath() string { return filepath.Join(s.paths.ConfigDir, "history.json") }

// SavedQueries loads the query library, creating a starter file on first run.
func (s *Store) SavedQueries() ([]SavedQuery, error) {
	raw, err := os.ReadFile(s.queriesPath())
	if errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(s.paths.ConfigDir, 0o700); err != nil {
			return nil, err
		}
		if err := os.WriteFile(s.queriesPath(), []byte(starterQueries), 0o600); err != nil {
			return nil, err
		}
		raw = []byte(starterQueries)
	} else if err != nil {
		return nil, err
	}
	var doc struct {
		Queries []SavedQuery `yaml:"queries"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		// The Go type names in a yaml error mean nothing to the person who
		// has to fix the file; the path does.
		return nil, fmt.Errorf("%s is not valid YAML: %s", s.queriesPath(), firstLine(err.Error()))
	}
	return doc.Queries, nil
}

// firstLine keeps an error to one line: the status bar is one line, and a
// multi-line message pushes the app's own chrome off the screen.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}

const historyCap = 500

// History returns persisted query history, most recent first.
func (s *Store) History() []string {
	s.histMu.Lock()
	defer s.histMu.Unlock()
	return s.readHistory()
}

func (s *Store) readHistory() []string {
	raw, err := os.ReadFile(s.historyPath())
	if err != nil {
		return nil
	}
	var entries []string
	if json.Unmarshal(raw, &entries) != nil {
		return nil
	}
	return entries
}

// AppendHistory prepends a query, dedups it against prior entries, caps the
// list, and persists it atomically. Best-effort: persistence errors are
// returned but the in-memory result is always usable.
func (s *Store) AppendHistory(query string) ([]string, error) {
	s.histMu.Lock()
	defer s.histMu.Unlock()
	entries := []string{query}
	for _, e := range s.readHistory() {
		if e != query {
			entries = append(entries, e)
		}
	}
	if len(entries) > historyCap {
		entries = entries[:historyCap]
	}
	if err := os.MkdirAll(s.paths.ConfigDir, 0o700); err != nil {
		return entries, err
	}
	raw, err := json.MarshalIndent(entries, "", " ")
	if err != nil {
		return entries, err
	}
	return entries, atomicWrite(s.historyPath(), raw)
}

// atomicWrite lands the file via a same-directory temp file + rename so a
// reader can never observe a truncated write.
func atomicWrite(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), path)
}

type cacheEnvelope struct {
	SavedAt time.Time       `json:"savedAt"`
	Data    json.RawMessage `json:"data"`
}

// cachePath hashes the key so server-supplied strings (sObject and metadata
// type names) can never escape the cache directory.
func (s *Store) cachePath(key string) string {
	sum := sha256.Sum256([]byte(key))
	return filepath.Join(s.paths.CacheDir, hex.EncodeToString(sum[:])+".json")
}

// CacheGet loads a cached value if it exists and is younger than maxAge. A
// maxAge of zero or less means "do not use the cache": on a coarse clock the
// age of a just-written entry can read as exactly zero.
func (s *Store) CacheGet(key string, maxAge time.Duration, into any) bool {
	if maxAge <= 0 {
		return false
	}
	raw, err := os.ReadFile(s.cachePath(key))
	if err != nil {
		return false
	}
	var env cacheEnvelope
	if json.Unmarshal(raw, &env) != nil {
		return false
	}
	if time.Since(env.SavedAt) > maxAge {
		return false
	}
	return json.Unmarshal(env.Data, into) == nil
}

// CachePut stores a value under key; failures are silent by design (cache
// is an optimization, never a source of truth).
func (s *Store) CachePut(key string, value any) {
	data, err := json.Marshal(value)
	if err != nil {
		return
	}
	raw, err := json.Marshal(cacheEnvelope{SavedAt: time.Now(), Data: data})
	if err != nil {
		return
	}
	if err := os.MkdirAll(s.paths.CacheDir, 0o700); err != nil {
		return
	}
	_ = os.WriteFile(s.cachePath(key), raw, 0o600)
}
