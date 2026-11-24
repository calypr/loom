package construction

import (
	"testing"
	"time"
)

func TestConstructionBaseCacheSeparatesOutputID(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	cache := newConstructionBaseCache(time.Hour, 16, func() time.Time { return now })
	base := constructionBaseCacheKey{
		Format: constructionBaseCacheFormatVersion, Project: "project", ExplorerID: "explorer",
		DraftVersion: 1, DraftDigest: "digest", SnapshotToken: "token", AuthorizationScopeDigest: "scope",
	}
	patientsKey := base
	patientsKey.OutputID = "patients"
	siblingKey := base
	siblingKey.OutputID = "sibling"
	cache.Put(patientsKey, constructionBaseCacheValue{})
	if _, ok := cache.Get(siblingKey); ok {
		t.Fatal("cache reused an entry for a different output ID")
	}
	if _, ok := cache.Get(patientsKey); !ok {
		t.Fatal("cache missed the original output ID")
	}
}

func TestConstructionBaseCacheEvictsOldestEntry(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	cache := newConstructionBaseCache(time.Hour, 2, func() time.Time { return now })
	key := func(outputID string) constructionBaseCacheKey {
		return constructionBaseCacheKey{Format: constructionBaseCacheFormatVersion, Project: "project", ExplorerID: "explorer", DraftVersion: 1, DraftDigest: "digest", SnapshotToken: "token", OutputID: outputID}
	}
	cache.Put(key("a"), constructionBaseCacheValue{})
	cache.Put(key("b"), constructionBaseCacheValue{})
	cache.Put(key("c"), constructionBaseCacheValue{})
	if _, ok := cache.Get(key("a")); ok {
		t.Fatal("oldest cache entry was not evicted")
	}
	if _, ok := cache.Get(key("b")); !ok {
		t.Fatal("cache entry b should remain")
	}
	if _, ok := cache.Get(key("c")); !ok {
		t.Fatal("cache entry c should remain")
	}
}

func TestTTLCacheSamplesPutClockWhileLocked(t *testing.T) {
	now := time.Unix(100, 0)
	var cache *ttlCache[string, string]
	sampledWhileLocked := false
	cache = newTTLCache[string, string](time.Minute, 2, func() time.Time {
		if cache.Mu.TryLock() {
			cache.Mu.Unlock()
		} else {
			sampledWhileLocked = true
		}
		return now
	}, time.Minute, 2)

	cache.Put("key", "value")
	if !sampledWhileLocked {
		t.Fatal("generic Put sampled its clock before acquiring the cache lock")
	}
}

func TestConstructionBaseCacheSamplesClockBeforeLock(t *testing.T) {
	now := time.Unix(100, 0)
	var cache *constructionBaseCache
	sampledUnlocked := false
	cache = newConstructionBaseCache(time.Minute, 2, func() time.Time {
		if cache.Entries.Mu.TryLock() {
			sampledUnlocked = true
			cache.Entries.Mu.Unlock()
		}
		return now
	})

	cache.Put(constructionBaseCacheKey{OutputID: "out"}, constructionBaseCacheValue{})
	if !sampledUnlocked {
		t.Fatal("construction base cache sampled its clock after acquiring the cache lock")
	}
}
