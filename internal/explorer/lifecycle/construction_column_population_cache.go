package lifecycle

import "time"

const (
	columnPopulationCacheTTL        = 2 * time.Minute
	columnPopulationCacheMaxEntries = 4096
)

type columnPopulationCacheKey struct {
	Project    string
	Generation string
	ReceiptID  string
	OutputID   string
	StageID    string
	ColumnID   string
	Scope      string
}

type columnPopulationCache struct {
	*ttlCache[columnPopulationCacheKey, string]
}

func newColumnPopulationCache(ttl time.Duration, maxEntries int, now func() time.Time) *columnPopulationCache {
	return &columnPopulationCache{ttlCache: newTTLCache[columnPopulationCacheKey, string](ttl, maxEntries, now, columnPopulationCacheTTL, columnPopulationCacheMaxEntries)}
}

func (c *columnPopulationCache) Get(key columnPopulationCacheKey) (string, bool) {
	if c == nil || key.ReceiptID == "" {
		return "", false
	}
	return c.ttlCache.Get(key)
}

func (c *columnPopulationCache) Put(key columnPopulationCacheKey, status string) {
	if c == nil || key.ReceiptID == "" || (status != "POPULATED" && status != "EMPTY") {
		return
	}
	c.ttlCache.Put(key, status)
}
