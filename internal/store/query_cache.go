package store

type queryEmbeddingCache struct {
	capacity int
	order    []string
	values   map[string][]float32
}

func newQueryEmbeddingCache(capacity int) *queryEmbeddingCache {
	if capacity < 0 {
		capacity = 0
	}
	return &queryEmbeddingCache{
		capacity: capacity,
		order:    make([]string, 0, capacity),
		values:   make(map[string][]float32, capacity),
	}
}

func (c *queryEmbeddingCache) get(key string) ([]float32, bool) {
	if c == nil {
		return nil, false
	}
	value, ok := c.values[key]
	if !ok {
		return nil, false
	}
	return cloneEmbedding(value), true
}

func (c *queryEmbeddingCache) put(key string, value []float32) {
	if c == nil || c.capacity == 0 {
		return
	}
	if _, ok := c.values[key]; ok {
		c.values[key] = cloneEmbedding(value)
		return
	}
	if len(c.order) >= c.capacity {
		oldest := c.order[0]
		delete(c.values, oldest)
		c.order = c.order[1:]
	}
	c.order = append(c.order, key)
	c.values[key] = cloneEmbedding(value)
}

func cloneEmbedding(value []float32) []float32 {
	if value == nil {
		return nil
	}
	copyValue := make([]float32, len(value))
	copy(copyValue, value)
	return copyValue
}
