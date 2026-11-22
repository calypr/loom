package authscope

import "time"

const defaultFenceAccessCacheEntries = 1024

type cachedResourceAccess struct {
	snapshot  map[string][]resourceAccessRecord
	expiresAt time.Time

	seq uint64
}

func (c *FenceUserAccessClient) currentTime() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

func (c *FenceUserAccessClient) storeResourceAccessLocked(key string, snapshot map[string][]resourceAccessRecord, now time.Time) {
	c.cache[key] = cachedResourceAccess{snapshot: cloneResourceAccess(snapshot), expiresAt: now.Add(c.cacheTTL), seq: c.nextAccessSeqLocked()}
	for token, entry := range c.cache {
		if !now.Before(entry.expiresAt) {
			delete(c.cache, token)
		}
	}
	for len(c.cache) > c.maxCacheEntries {
		victim, ok := c.leastRecentlyUsedCacheKeyLocked()
		if !ok {
			return
		}
		delete(c.cache, victim)
	}
}

func (c *FenceUserAccessClient) nextAccessSeqLocked() uint64 {
	c.accessSeq++
	return c.accessSeq
}

func (c *FenceUserAccessClient) leastRecentlyUsedCacheKeyLocked() (string, bool) {
	var victim string
	var oldest uint64
	found := false
	for token, entry := range c.cache {
		if !found || entry.seq < oldest {
			victim = token
			oldest = entry.seq
			found = true
		}
	}
	return victim, found
}

func cloneResourceAccess(in map[string][]resourceAccessRecord) map[string][]resourceAccessRecord {
	out := make(map[string][]resourceAccessRecord, len(in))
	for resource, records := range in {
		out[resource] = append([]resourceAccessRecord(nil), records...)
	}
	return out
}
