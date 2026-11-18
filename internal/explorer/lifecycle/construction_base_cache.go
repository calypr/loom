package lifecycle

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
)

const (
	constructionBaseCacheFormatVersion = 1

	defaultConstructionBaseCacheTTL     = 5 * time.Minute
	defaultConstructionBaseCacheEntries = 16
)

type constructionBaseCacheKey struct {
	Format                   int
	Project                  string
	ExplorerID               string
	DraftVersion             int64
	DraftDigest              string
	SnapshotToken            string
	Generation               string
	SchemaDigest             string
	ShapeDigest              string
	ResourceInventoryDigest  string
	RelationshipDigest       string
	FieldDigest              string
	ProtocolVersion          string
	CompilerVersion          string
	TraversalPolicyVersion   string
	ProjectionPolicyVersion  string
	CacheVersion             string
	OutputID                 string
	AuthorizationScopeDigest string
}

type constructionBaseCacheValue struct {
	receipt *explorer.CompilationReceipt
	stages  []explorer.ReceiptConstructionStage
}

type ttlCacheEntry[V any] struct {
	value     V
	expiresAt time.Time
}

type ttlCache[K comparable, V any] struct {
	mu      sync.Mutex
	entries map[K]ttlCacheEntry[V]
	order   []K
	max     int
	ttl     time.Duration
	now     func() time.Time
}

func newTTLCache[K comparable, V any](ttl time.Duration, maxEntries int, now func() time.Time, defaultTTL time.Duration, defaultMaxEntries int) *ttlCache[K, V] {
	if ttl <= 0 {
		ttl = defaultTTL
	}
	if maxEntries <= 0 {
		maxEntries = defaultMaxEntries
	}
	if now == nil {
		now = time.Now
	}
	return &ttlCache[K, V]{
		entries: make(map[K]ttlCacheEntry[V], maxEntries),
		max:     maxEntries,
		ttl:     ttl,
		now:     now,
	}
}

func (c *ttlCache[K, V]) Get(key K) (V, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok {
		var zero V
		return zero, false
	}
	if !c.now().Before(entry.expiresAt) {
		c.deleteLocked(key)
		var zero V
		return zero, false
	}
	return entry.value, true
}

func (c *ttlCache[K, V]) Put(key K, value V) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.putLocked(key, value, c.now().Add(c.ttl))
}

func (c *ttlCache[K, V]) putExpiringAt(key K, value V, expiresAt time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.putLocked(key, value, expiresAt)
}

func (c *ttlCache[K, V]) putLocked(key K, value V, expiresAt time.Time) {
	if _, exists := c.entries[key]; !exists {
		for len(c.entries) >= c.max && len(c.order) > 0 {
			oldest := c.order[0]
			c.order = c.order[1:]
			delete(c.entries, oldest)
		}
		c.order = append(c.order, key)
	}
	c.entries[key] = ttlCacheEntry[V]{value: value, expiresAt: expiresAt}
}

func (c *ttlCache[K, V]) Delete(key K) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.deleteLocked(key)
}

func (c *ttlCache[K, V]) deleteLocked(key K) {
	delete(c.entries, key)
	for index := range c.order {
		if c.order[index] != key {
			continue
		}
		c.order = append(c.order[:index], c.order[index+1:]...)
		return
	}
}

type constructionBaseCache struct {
	entries *ttlCache[constructionBaseCacheKey, constructionBaseCacheValue]
}

func newConstructionBaseCache(ttl time.Duration, maxEntries int, now func() time.Time) *constructionBaseCache {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &constructionBaseCache{entries: newTTLCache[constructionBaseCacheKey, constructionBaseCacheValue](ttl, maxEntries, now, defaultConstructionBaseCacheTTL, defaultConstructionBaseCacheEntries)}
}

func (c *constructionBaseCache) Get(key constructionBaseCacheKey) (constructionBaseCacheValue, bool) {
	value, ok := c.entries.Get(key)
	if !ok {
		return constructionBaseCacheValue{}, false
	}
	receipt := cloneCachedCompilationReceipt(value.receipt)
	if value.receipt != nil && receipt == nil {
		c.entries.Delete(key)
		return constructionBaseCacheValue{}, false
	}
	return constructionBaseCacheValue{
		receipt: receipt,
		stages:  cloneCachedReceiptConstructionStages(value.stages),
	}, true
}

func (c *constructionBaseCache) Put(key constructionBaseCacheKey, value constructionBaseCacheValue) {
	receipt := cloneCachedCompilationReceipt(value.receipt)
	if value.receipt != nil && receipt == nil {
		return
	}
	c.entries.putExpiringAt(key, constructionBaseCacheValue{
		receipt: receipt,
		stages:  cloneCachedReceiptConstructionStages(value.stages),
	}, c.entries.now().Add(c.entries.ttl))
}

func cloneCachedCompilationReceipt(in *explorer.CompilationReceipt) *explorer.CompilationReceipt {
	if in == nil {
		return nil
	}
	encoded, err := json.Marshal(in)
	if err != nil {
		return nil
	}
	out := new(explorer.CompilationReceipt)
	if err := json.Unmarshal(encoded, out); err != nil {
		return nil
	}
	return out
}

func cloneCachedReceiptConstructionStages(in []explorer.ReceiptConstructionStage) []explorer.ReceiptConstructionStage {
	if in == nil {
		return nil
	}
	out := make([]explorer.ReceiptConstructionStage, len(in))
	for index := range in {
		out[index] = in[index]
		out[index].Columns = append([]explorer.ReceiptConstructionStageColumn(nil), in[index].Columns...)
		out[index].Capabilities = append([]explorer.ReceiptConstructionOperationChoice(nil), in[index].Capabilities...)
		out[index].RelatedExpandAnchors = append([]explorer.ReceiptConstructionRelatedExpandAnchor(nil), in[index].RelatedExpandAnchors...)
		out[index].CodedGroupChoices = append([]explorer.ReceiptConstructionCodedGroupChoice(nil), in[index].CodedGroupChoices...)
		if in[index].RelatedExpand != nil {
			related := *in[index].RelatedExpand
			related.Route = append([]recipe.ConstructionRelatedRouteStep(nil), in[index].RelatedExpand.Route...)
			out[index].RelatedExpand = &related
		}
		if in[index].ActiveRelatedRecord != nil {
			active := *in[index].ActiveRelatedRecord
			out[index].ActiveRelatedRecord = &active
		}
		if context := in[index].GroupFieldSourceContext; context != nil {
			cloned := *context
			cloned.FieldOwners = append([]explorer.ReceiptConstructionGroupFieldOwner(nil), context.FieldOwners...)
			if context.SourceExpansion != nil {
				expansion := *context.SourceExpansion
				expansion.Route = append([]explorer.ReceiptConstructionGroupFieldSourceRouteStep(nil), context.SourceExpansion.Route...)
				expansion.Ancestors = append([]explorer.ReceiptConstructionGroupFieldSourceAncestor(nil), context.SourceExpansion.Ancestors...)
				cloned.SourceExpansion = &expansion
			}
			out[index].GroupFieldSourceContext = &cloned
		}
	}
	return out
}
