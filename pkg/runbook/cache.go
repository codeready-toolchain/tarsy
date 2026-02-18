package runbook

import (
	"sync"
	"time"
)

// cacheEntry holds cached content with a timestamp for TTL expiration.
type cacheEntry struct {
	content   string
	fetchedAt time.Time
}

// RunbookCache is a thread-safe in-memory cache with TTL expiration.
// Expired entries are cleaned up lazily on Get() — no background goroutine.
type RunbookCache struct {
	mu      sync.RWMutex
	entries map[string]*cacheEntry
	ttl     time.Duration
}

// NewRunbookCache creates a new cache with the given TTL.
func NewRunbookCache(ttl time.Duration) *RunbookCache {
	return &RunbookCache{
		entries: make(map[string]*cacheEntry),
		ttl:     ttl,
	}
}

// Get returns cached content if present and not expired.
func (c *RunbookCache) Get(url string) (string, bool) {
	c.mu.RLock()
	entry, ok := c.entries[url]
	c.mu.RUnlock()

	if !ok {
		return "", false
	}

	if time.Since(entry.fetchedAt) > c.ttl {
		// Expired — clean up lazily
		c.mu.Lock()
		delete(c.entries, url)
		c.mu.Unlock()
		return "", false
	}

	return entry.content, true
}

// Set stores content with the current timestamp.
func (c *RunbookCache) Set(url string, content string) {
	c.mu.Lock()
	c.entries[url] = &cacheEntry{
		content:   content,
		fetchedAt: time.Now(),
	}
	c.mu.Unlock()
}
