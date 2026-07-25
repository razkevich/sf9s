package api

import (
	"context"
	"sync"
	"time"
)

// cachedTokenSource memoizes credentials resolved by a fetch function for a
// short window so hot paths (describe-per-keystroke, pagination) don't spawn
// a CLI process each time. force refetches unconditionally.
type cachedTokenSource struct {
	fetch func(ctx context.Context) (Credentials, error)
	ttl   time.Duration

	mu        sync.Mutex
	creds     Credentials
	fetchedAt time.Time
}

// NewCachedTokenSource wraps fetch with a 20-minute in-memory cache.
func NewCachedTokenSource(fetch func(ctx context.Context) (Credentials, error)) TokenSource {
	return &cachedTokenSource{fetch: fetch, ttl: 20 * time.Minute}
}

func (s *cachedTokenSource) Credentials(ctx context.Context, force bool) (Credentials, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !force && s.creds.AccessToken != "" && time.Since(s.fetchedAt) < s.ttl {
		return s.creds, nil
	}
	creds, err := s.fetch(ctx)
	if err != nil {
		return Credentials{}, err
	}
	s.creds, s.fetchedAt = creds, time.Now()
	return creds, nil
}
