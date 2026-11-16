package lifecycle

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
)

const (
	// constructionBaseCacheFormatVersion invalidates entries when the cached
	// value shape or the meaning of a key field changes.
	constructionBaseCacheFormatVersion = 1

	defaultConstructionBaseCacheTTL     = 5 * time.Minute
	defaultConstructionBaseCacheEntries = 16
)

// constructionBaseCacheKey includes every request input that can change a
// compiled construction base. The authorization scope digest is included so a
// broader or narrower read scope cannot reuse another scope's compilation. The
// snapshot identity digests are included even though the token is content
// addressed, so a resolver that ever reuses a token across a generation/schema
// change cannot leak an older compilation into a newer snapshot.
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

// constructionBaseCacheValue holds only the expensive compile-derived state.
// The owner, workspace, document, construction, snapshot, authorization, and
// catalog are revalidated or rebuilt for every request.
type constructionBaseCacheValue struct {
	receipt *explorer.CompilationReceipt
	stages  []explorer.ReceiptConstructionStage
}

type constructionBaseCacheEntry struct {
	value     constructionBaseCacheValue
	expiresAt time.Time
}

type constructionBaseCache struct {
	mu      sync.Mutex
	entries map[constructionBaseCacheKey]constructionBaseCacheEntry
	order   []constructionBaseCacheKey
	max     int
	ttl     time.Duration
	now     func() time.Time
}

func newConstructionBaseCache(ttl time.Duration, maxEntries int, now func() time.Time) *constructionBaseCache {
	if ttl <= 0 {
		ttl = defaultConstructionBaseCacheTTL
	}
	if maxEntries <= 0 {
		maxEntries = defaultConstructionBaseCacheEntries
	}
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &constructionBaseCache{
		entries: make(map[constructionBaseCacheKey]constructionBaseCacheEntry, maxEntries),
		max:     maxEntries,
		ttl:     ttl,
		now:     now,
	}
}

func (c *constructionBaseCache) Get(key constructionBaseCacheKey) (constructionBaseCacheValue, bool) {
	c.mu.Lock()
	entry, ok := c.entries[key]
	if !ok {
		c.mu.Unlock()
		return constructionBaseCacheValue{}, false
	}
	if !c.now().Before(entry.expiresAt) {
		c.deleteLocked(key)
		c.mu.Unlock()
		return constructionBaseCacheValue{}, false
	}
	c.mu.Unlock()

	receipt := cloneCachedCompilationReceipt(entry.value.receipt)
	if entry.value.receipt != nil && receipt == nil {
		c.mu.Lock()
		c.deleteLocked(key)
		c.mu.Unlock()
		return constructionBaseCacheValue{}, false
	}
	return constructionBaseCacheValue{
		receipt: receipt,
		stages:  cloneCachedReceiptConstructionStages(entry.value.stages),
	}, true
}

func (c *constructionBaseCache) Put(key constructionBaseCacheKey, value constructionBaseCacheValue) {
	receipt := cloneCachedCompilationReceipt(value.receipt)
	if value.receipt != nil && receipt == nil {
		return
	}
	entry := constructionBaseCacheEntry{
		value: constructionBaseCacheValue{
			receipt: receipt,
			stages:  cloneCachedReceiptConstructionStages(value.stages),
		},
		expiresAt: c.now().Add(c.ttl),
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.entries[key]; !exists {
		for len(c.entries) >= c.max && len(c.order) > 0 {
			oldest := c.order[0]
			c.order = c.order[1:]
			delete(c.entries, oldest)
		}
		c.order = append(c.order, key)
	}
	c.entries[key] = entry
}

func (c *constructionBaseCache) deleteLocked(key constructionBaseCacheKey) {
	delete(c.entries, key)
	for index := range c.order {
		if c.order[index] != key {
			continue
		}
		c.order = append(c.order[:index], c.order[index+1:]...)
		return
	}
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
