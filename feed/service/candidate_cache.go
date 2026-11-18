package service

import (
	"strconv"
	"sync"
	"time"
)

type candidateEntry struct {
	ids []string
	exp time.Time
}
type candidateCache struct {
	mu   sync.Mutex
	data map[string]candidateEntry
}

func newCandidateCache() *candidateCache {
	return &candidateCache{data: make(map[string]candidateEntry)}
}
func candidateKey(kind, uid string, size int64) string {
	return "v1:" + kind + ":" + uid + ":" + strconv.FormatInt(size, 10)
}
func (c *candidateCache) get(key string) []string {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.data[key]
	if !ok {
		return nil
	}
	if !time.Now().Before(entry.exp) {
		delete(c.data, key)
		return nil
	}
	return append([]string(nil), entry.ids...)
}
func (c *candidateCache) set(key string, ids []string) {
	if c == nil || len(ids) == 0 || len(ids) > 50 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.data[key]; !ok && len(c.data) >= 2000 {
		for k, entry := range c.data {
			if !time.Now().Before(entry.exp) {
				delete(c.data, k)
			}
		}
		if len(c.data) >= 2000 {
			for k := range c.data {
				delete(c.data, k)
				break
			}
		}
	}
	c.data[key] = candidateEntry{ids: append([]string(nil), ids...), exp: time.Now().Add(10 * time.Second)}
}
