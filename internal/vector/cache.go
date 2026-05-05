package vector

import "sync"

type embeddingCache struct {
	mu       sync.Mutex
	capacity int
	entries  map[string][]float32
	order    []string
}

func newEmbeddingCache(capacity int) *embeddingCache {
	return &embeddingCache{
		capacity: capacity,
		entries:  make(map[string][]float32),
	}
}

func (c *embeddingCache) get(key string) ([]float32, bool) {
	if c == nil || c.capacity <= 0 {
		return nil, false
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	value, ok := c.entries[key]
	if !ok {
		return nil, false
	}

	return cloneEmbedding(value), true
}

func (c *embeddingCache) put(key string, value []float32) {
	if c == nil || c.capacity <= 0 {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if _, exists := c.entries[key]; !exists {
		c.order = append(c.order, key)
	}
	c.entries[key] = cloneEmbedding(value)

	for len(c.order) > c.capacity {
		oldest := c.order[0]
		c.order = c.order[1:]
		delete(c.entries, oldest)
	}
}

func cloneEmbedding(value []float32) []float32 {
	if value == nil {
		return nil
	}

	cloned := make([]float32, len(value))
	copy(cloned, value)
	return cloned
}
