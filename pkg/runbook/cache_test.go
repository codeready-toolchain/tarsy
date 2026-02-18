package runbook

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestRunbookCache_SetAndGet(t *testing.T) {
	cache := NewCache(1 * time.Minute)

	cache.Set("https://example.com/runbook.md", "# Runbook Content")

	content, ok := cache.Get("https://example.com/runbook.md")
	assert.True(t, ok)
	assert.Equal(t, "# Runbook Content", content)
}

func TestRunbookCache_Miss(t *testing.T) {
	cache := NewCache(1 * time.Minute)

	content, ok := cache.Get("https://example.com/nonexistent.md")
	assert.False(t, ok)
	assert.Equal(t, "", content)
}

func TestRunbookCache_TTLExpiry(t *testing.T) {
	cache := NewCache(50 * time.Millisecond)

	cache.Set("https://example.com/runbook.md", "content")

	// Should be present immediately
	content, ok := cache.Get("https://example.com/runbook.md")
	assert.True(t, ok)
	assert.Equal(t, "content", content)

	// Wait for TTL to expire
	time.Sleep(60 * time.Millisecond)

	// Should be expired
	content, ok = cache.Get("https://example.com/runbook.md")
	assert.False(t, ok)
	assert.Equal(t, "", content)
}

func TestRunbookCache_Overwrite(t *testing.T) {
	cache := NewCache(1 * time.Minute)

	cache.Set("https://example.com/runbook.md", "old content")
	cache.Set("https://example.com/runbook.md", "new content")

	content, ok := cache.Get("https://example.com/runbook.md")
	assert.True(t, ok)
	assert.Equal(t, "new content", content)
}

func TestRunbookCache_MultipleKeys(t *testing.T) {
	cache := NewCache(1 * time.Minute)

	cache.Set("url1", "content1")
	cache.Set("url2", "content2")

	c1, ok1 := cache.Get("url1")
	c2, ok2 := cache.Get("url2")

	assert.True(t, ok1)
	assert.Equal(t, "content1", c1)
	assert.True(t, ok2)
	assert.Equal(t, "content2", c2)
}

func TestRunbookCache_ConcurrentAccess(t *testing.T) {
	cache := NewCache(1 * time.Minute)
	var wg sync.WaitGroup

	// Concurrent writes
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(_ int) {
			defer wg.Done()
			cache.Set("shared-key", "content")
		}(i)
	}

	// Concurrent reads
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cache.Get("shared-key")
		}()
	}

	wg.Wait()

	// Should still be readable
	content, ok := cache.Get("shared-key")
	assert.True(t, ok)
	assert.Equal(t, "content", content)
}
