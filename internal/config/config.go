// Package config owns everything sf9s persists: saved queries, query
// history, and the describe cache. Tokens are deliberately never stored.
package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
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

// Store is the entry point for all persisted state.
type Store struct {
	paths Paths
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
		if err := os.MkdirAll(s.paths.ConfigDir, 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(s.queriesPath(), []byte(starterQueries), 0o644); err != nil {
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
		return nil, err
	}
	return doc.Queries, nil
}

const historyCap = 500

// History returns persisted query history, most recent first.
func (s *Store) History() []string {
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
// list, and persists it. Best-effort: persistence errors are returned but
// the in-memory result is always usable.
func (s *Store) AppendHistory(query string) ([]string, error) {
	entries := []string{query}
	for _, e := range s.History() {
		if e != query {
			entries = append(entries, e)
		}
	}
	if len(entries) > historyCap {
		entries = entries[:historyCap]
	}
	if err := os.MkdirAll(s.paths.ConfigDir, 0o755); err != nil {
		return entries, err
	}
	raw, err := json.MarshalIndent(entries, "", " ")
	if err != nil {
		return entries, err
	}
	return entries, os.WriteFile(s.historyPath(), raw, 0o644)
}

type cacheEnvelope struct {
	SavedAt time.Time       `json:"savedAt"`
	Data    json.RawMessage `json:"data"`
}

// CacheGet loads a cached value if it exists and is younger than maxAge.
func (s *Store) CacheGet(key string, maxAge time.Duration, into any) bool {
	raw, err := os.ReadFile(filepath.Join(s.paths.CacheDir, key+".json"))
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
	path := filepath.Join(s.paths.CacheDir, key+".json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	_ = os.WriteFile(path, raw, 0o644)
}
