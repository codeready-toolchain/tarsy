// Package runbook provides GitHub-based runbook fetching, caching, and URL resolution.
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

// Cache is a thread-safe in-memory cache with TTL expiration.
// Expired entries are cleaned up lazily on Get() — no background goroutine.
type Cache struct {
	mu      sync.RWMutex
	entries map[string]*cacheEntry
	ttl     time.Duration
}

// NewCache creates a new cache with the given TTL.
func NewCache(ttl time.Duration) *Cache {
	return &Cache{
		entries: make(map[string]*cacheEntry),
		ttl:     ttl,
	}
}

// Get returns cached content if present and not expired.
func (c *Cache) Get(url string) (string, bool) {
	c.mu.RLock()
	entry, ok := c.entries[url]
	c.mu.RUnlock()

	if !ok {
		return "", false
	}

	if time.Since(entry.fetchedAt) > c.ttl {
		// Expired — clean up lazily.
		// Re-check under write lock: a concurrent Set() may have replaced
		// the entry with a fresh one between RUnlock and Lock.
		c.mu.Lock()
		if current, ok := c.entries[url]; ok && time.Since(current.fetchedAt) > c.ttl {
			delete(c.entries, url)
		}
		c.mu.Unlock()
		return "", false
	}

	return entry.content, true
}

// Set stores content with the current timestamp.
func (c *Cache) Set(url string, content string) {
	c.mu.Lock()
	c.entries[url] = &cacheEntry{
		content:   content,
		fetchedAt: time.Now(),
	}
	c.mu.Unlock()
}
