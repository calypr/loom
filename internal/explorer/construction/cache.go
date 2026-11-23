package construction

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
	Value     V
	ExpiresAt time.Time
}

type ttlCache[K comparable, V any] struct {
	Mu      sync.Mutex
	Entries map[K]ttlCacheEntry[V]
	Order   []K
	Max     int
	TTL     time.Duration
	now     func() time.Time
}

func newTTLCache[K comparable, V any](TTL time.Duration, maxEntries int, now func() time.Time, defaultTTL time.Duration, defaultMaxEntries int) *ttlCache[K, V] {
	if TTL <= 0 {
		TTL = defaultTTL
	}
	if maxEntries <= 0 {
		maxEntries = defaultMaxEntries
	}
	if now == nil {
		now = time.Now
	}
	return &ttlCache[K, V]{
		Entries: make(map[K]ttlCacheEntry[V], maxEntries),
		Max:     maxEntries,
		TTL:     TTL,
		now:     now,
	}
}

func (c *ttlCache[K, V]) Get(key K) (V, bool) {
	c.Mu.Lock()
	defer c.Mu.Unlock()
	entry, ok := c.Entries[key]
	if !ok {
		var zero V
		return zero, false
	}
	if !c.now().Before(entry.ExpiresAt) {
		c.deleteLocked(key)
		var zero V
		return zero, false
	}
	return entry.Value, true
}

func (c *ttlCache[K, V]) Put(key K, Value V) {
	c.Mu.Lock()
	defer c.Mu.Unlock()
	c.putLocked(key, Value, c.now().Add(c.TTL))
}

func (c *ttlCache[K, V]) putExpiringAt(key K, Value V, ExpiresAt time.Time) {
	c.Mu.Lock()
	defer c.Mu.Unlock()
	c.putLocked(key, Value, ExpiresAt)
}

func (c *ttlCache[K, V]) putLocked(key K, Value V, ExpiresAt time.Time) {
	if _, exists := c.Entries[key]; !exists {
		for len(c.Entries) >= c.Max && len(c.Order) > 0 {
			oldest := c.Order[0]
			c.Order = c.Order[1:]
			delete(c.Entries, oldest)
		}
		c.Order = append(c.Order, key)
	}
	c.Entries[key] = ttlCacheEntry[V]{Value: Value, ExpiresAt: ExpiresAt}
}

func (c *ttlCache[K, V]) Delete(key K) {
	c.Mu.Lock()
	defer c.Mu.Unlock()
	c.deleteLocked(key)
}

func (c *ttlCache[K, V]) deleteLocked(key K) {
	delete(c.Entries, key)
	for index := range c.Order {
		if c.Order[index] != key {
			continue
		}
		c.Order = append(c.Order[:index], c.Order[index+1:]...)
		return
	}
}

type constructionBaseCache struct {
	Entries *ttlCache[constructionBaseCacheKey, constructionBaseCacheValue]
}

func newConstructionBaseCache(TTL time.Duration, maxEntries int, now func() time.Time) *constructionBaseCache {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &constructionBaseCache{Entries: newTTLCache[constructionBaseCacheKey, constructionBaseCacheValue](TTL, maxEntries, now, defaultConstructionBaseCacheTTL, defaultConstructionBaseCacheEntries)}
}

func (c *constructionBaseCache) Get(key constructionBaseCacheKey) (constructionBaseCacheValue, bool) {
	Value, ok := c.Entries.Get(key)
	if !ok {
		return constructionBaseCacheValue{}, false
	}
	receipt := cloneCachedCompilationReceipt(Value.receipt)
	if Value.receipt != nil && receipt == nil {
		c.Entries.Delete(key)
		return constructionBaseCacheValue{}, false
	}
	return constructionBaseCacheValue{
		receipt: receipt,
		stages:  cloneCachedReceiptConstructionStages(Value.stages),
	}, true
}

func (c *constructionBaseCache) Put(key constructionBaseCacheKey, Value constructionBaseCacheValue) {
	receipt := cloneCachedCompilationReceipt(Value.receipt)
	if Value.receipt != nil && receipt == nil {
		return
	}
	c.Entries.putExpiringAt(key, constructionBaseCacheValue{
		receipt: receipt,
		stages:  cloneCachedReceiptConstructionStages(Value.stages),
	}, c.Entries.now().Add(c.Entries.TTL))
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
