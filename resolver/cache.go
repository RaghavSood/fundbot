package resolver

import (
	"sync"
	"time"
)

type cacheEntry[T any] struct {
	value     T
	fetchedAt time.Time
}

// Cache is a simple in-memory TTL cache keyed by string.
type Cache[T any] struct {
	mu      sync.RWMutex
	entries map[string]cacheEntry[T]
	ttl     time.Duration
}

func NewCache[T any](ttl time.Duration) *Cache[T] {
	return &Cache[T]{
		entries: make(map[string]cacheEntry[T]),
		ttl:     ttl,
	}
}

// GetOrFetch returns a cached value or calls fetch to populate it.
func (c *Cache[T]) GetOrFetch(key string, fetch func() (T, error)) (T, error) {
	c.mu.RLock()
	if e, ok := c.entries[key]; ok && time.Since(e.fetchedAt) < c.ttl {
		c.mu.RUnlock()
		return e.value, nil
	}
	c.mu.RUnlock()

	c.mu.Lock()
	defer c.mu.Unlock()

	// Double-check after acquiring write lock.
	if e, ok := c.entries[key]; ok && time.Since(e.fetchedAt) < c.ttl {
		return e.value, nil
	}

	val, err := fetch()
	if err != nil {
		var zero T
		return zero, err
	}

	c.entries[key] = cacheEntry[T]{value: val, fetchedAt: time.Now()}
	return val, nil
}
