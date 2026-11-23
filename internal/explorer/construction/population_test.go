package construction

import (
	"testing"
	"time"
)

func TestSharedColumnPopulationCacheExpiresBoundsAndScopesEntries(t *testing.T) {
	now := time.Unix(100, 0)
	cache := newColumnPopulationCache(time.Second, 2, func() time.Time { return now })
	key := columnPopulationCacheKey{Project: "p", Generation: "g1", ReceiptID: "r", OutputID: "o", StageID: "s", ColumnID: "c", Scope: "scope-a"}
	cache.Put(key, "EMPTY")
	if status, ok := cache.Get(key); !ok || status != "EMPTY" {
		t.Fatalf("cached status = %q, %v", status, ok)
	}
	cache.Put(key, "UNKNOWN")
	if status, ok := cache.Get(key); !ok || status != "EMPTY" {
		t.Fatalf("UNKNOWN replaced conclusive cached status: %q, %v", status, ok)
	}
	otherScope := key
	otherScope.Scope = "scope-b"
	if _, ok := cache.Get(otherScope); ok {
		t.Fatal("cache entry crossed authorization scope")
	}
	now = now.Add(2 * time.Second)
	if _, ok := cache.Get(key); ok || len(cache.Order) != 0 {
		t.Fatalf("expired cache entry or order retained: %#v", cache.Order)
	}
	cache.Put(key, "POPULATED")
	if len(cache.Order) != 1 {
		t.Fatalf("expiry/reinsert duplicated cache order: %#v", cache.Order)
	}
	cache.Put(otherScope, "EMPTY")
	third := key
	third.ColumnID = "third"
	cache.Put(third, "POPULATED")
	if len(cache.Entries) != 2 || len(cache.Order) != 2 {
		t.Fatalf("cache exceeded its bound: entries=%d order=%d", len(cache.Entries), len(cache.Order))
	}
	cache.Put(columnPopulationCacheKey{ColumnID: "uncacheable"}, "EMPTY")
	if len(cache.Entries) != 2 {
		t.Fatal("cached result without an exact receipt identity")
	}
}

func TestColumnPopulationCacheExpiresBoundsAndScopesEntries(t *testing.T) {
	now := time.Unix(100, 0)
	cache := newColumnPopulationCache(time.Second, 2, func() time.Time { return now })
	key := columnPopulationCacheKey{Project: "p", Generation: "g1", ReceiptID: "r", OutputID: "o", StageID: "s", ColumnID: "c", Scope: "scope-a"}
	cache.Put(key, "EMPTY")
	if status, ok := cache.Get(key); !ok || status != "EMPTY" {
		t.Fatalf("cached status = %q, %v", status, ok)
	}
	cache.Put(key, "UNKNOWN")
	if status, ok := cache.Get(key); !ok || status != "EMPTY" {
		t.Fatalf("UNKNOWN replaced conclusive cached status: %q, %v", status, ok)
	}
	otherScope := key
	otherScope.Scope = "scope-b"
	if _, ok := cache.Get(otherScope); ok {
		t.Fatal("cache entry crossed authorization scope")
	}
	now = now.Add(2 * time.Second)
	if _, ok := cache.Get(key); ok || len(cache.Order) != 0 {
		t.Fatalf("expired cache entry or order retained: %#v", cache.Order)
	}
	cache.Put(key, "POPULATED")
	if len(cache.Order) != 1 {
		t.Fatalf("expiry/reinsert duplicated cache order: %#v", cache.Order)
	}
	cache.Put(otherScope, "EMPTY")
	third := key
	third.ColumnID = "third"
	cache.Put(third, "POPULATED")
	if len(cache.Entries) != 2 || len(cache.Order) != 2 {
		t.Fatalf("cache exceeded its bound: entries=%d order=%d", len(cache.Entries), len(cache.Order))
	}
	cache.Put(columnPopulationCacheKey{ColumnID: "uncacheable"}, "EMPTY")
	if len(cache.Entries) != 2 {
		t.Fatal("cached result without an exact receipt identity")
	}
}
