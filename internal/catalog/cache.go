package catalog

import (
	"container/list"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
)

const (
	DefaultCacheMaxEntries = 1024
	DefaultCacheMaxBytes   = 64 << 20
)

type CacheOptions struct {
	MaxEntries int
	MaxBytes   int
}

type CacheStats struct {
	Entries   int
	Bytes     int
	Evictions uint64
}

type cacheEntryKind string

const (
	cacheFields     cacheEntryKind = "fields"
	cacheReferences cacheEntryKind = "references"
)

type cacheEntry struct {
	kind   cacheEntryKind
	key    string
	value  any
	weight int
}

type Cache struct {
	mu        sync.Mutex
	limits    CacheOptions
	entries   map[string]*list.Element
	lru       *list.List
	bytes     int
	evictions uint64
}

func NewCache() *Cache {
	return NewCacheWithOptions(CacheOptions{})
}

func NewCacheWithOptions(options CacheOptions) *Cache {
	if options.MaxEntries <= 0 {
		options.MaxEntries = DefaultCacheMaxEntries
	}
	if options.MaxBytes <= 0 {
		options.MaxBytes = DefaultCacheMaxBytes
	}
	return &Cache{
		limits:  options,
		entries: make(map[string]*list.Element),
		lru:     list.New(),
	}
}

func (c *Cache) DiscoverFields(fn func(context.Context, PopulatedFieldOptions) ([]PopulatedField, error)) func(context.Context, PopulatedFieldOptions) ([]PopulatedField, error) {
	return func(ctx context.Context, opts PopulatedFieldOptions) ([]PopulatedField, error) {
		key, err := fieldKey(opts)
		if err != nil {
			return nil, err
		}
		cached, ok := c.get(cacheFields, key)
		if ok {
			return cloneFields(cached.([]PopulatedField)), nil
		}
		results, err := fn(ctx, opts)
		if err != nil {
			return nil, err
		}
		cached = cloneFields(results)
		c.put(cacheFields, key, cached)
		return cloneFields(cached.([]PopulatedField)), nil
	}
}

func (c *Cache) DiscoverReferences(fn func(context.Context, PopulatedReferenceOptions) ([]PopulatedReference, error)) func(context.Context, PopulatedReferenceOptions) ([]PopulatedReference, error) {
	return func(ctx context.Context, opts PopulatedReferenceOptions) ([]PopulatedReference, error) {
		key, err := referenceKey(opts)
		if err != nil {
			return nil, err
		}
		cached, ok := c.get(cacheReferences, key)
		if ok {
			return cloneReferences(cached.([]PopulatedReference)), nil
		}
		results, err := fn(ctx, opts)
		if err != nil {
			return nil, err
		}
		cached = cloneReferences(results)
		c.put(cacheReferences, key, cached)
		return cloneReferences(cached.([]PopulatedReference)), nil
	}
}

func (c *Cache) InvalidateProject(project string) {
	project = strings.TrimSpace(project)
	if project == "" {
		c.InvalidateAll()
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for entryKey, element := range c.entries {
		entry := element.Value.(cacheEntry)
		if strings.HasPrefix(entry.key, project+"|") {
			c.removeLocked(entryKey, element)
		}
	}
}

func (c *Cache) InvalidateAll() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = make(map[string]*list.Element)
	c.lru.Init()
	c.bytes = 0
}

func (c *Cache) Stats() CacheStats {
	c.mu.Lock()
	defer c.mu.Unlock()
	return CacheStats{Entries: len(c.entries), Bytes: c.bytes, Evictions: c.evictions}
}

func (c *Cache) get(kind cacheEntryKind, key string) (any, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entryKey := string(kind) + "\x00" + key
	element, ok := c.entries[entryKey]
	if !ok {
		return nil, false
	}
	c.lru.MoveToFront(element)
	return element.Value.(cacheEntry).value, true
}

func (c *Cache) put(kind cacheEntryKind, key string, value any) {
	weight := cacheValueWeight(key, value)
	if weight > c.limits.MaxBytes {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entryKey := string(kind) + "\x00" + key
	if existing, ok := c.entries[entryKey]; ok {
		c.removeLocked(entryKey, existing)
	}
	for len(c.entries) >= c.limits.MaxEntries || c.bytes+weight > c.limits.MaxBytes {
		oldest := c.lru.Back()
		if oldest == nil {
			return
		}
		oldestKey := oldest.Value.(cacheEntry)
		c.removeLocked(string(oldestKey.kind)+"\x00"+oldestKey.key, oldest)
		c.evictions++
	}
	entry := cacheEntry{kind: kind, key: key, value: value, weight: weight}
	c.entries[entryKey] = c.lru.PushFront(entry)
	c.bytes += weight
}

func (c *Cache) removeLocked(key string, element *list.Element) {
	entry := element.Value.(cacheEntry)
	delete(c.entries, key)
	c.lru.Remove(element)
	c.bytes -= entry.weight
}

func cacheValueWeight(key string, value any) int {
	encoded, err := json.Marshal(value)
	if err != nil {
		return len(key)
	}
	return len(key) + len(encoded)
}

func fieldKey(opts PopulatedFieldOptions) (string, error) {
	scope, err := authScopeKey(opts.AuthResourcePaths, opts.AuthResourcePathsUnrestricted)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s|%s|%t|%s|%s", strings.TrimSpace(opts.Project), strings.TrimSpace(opts.ResourceType), opts.PivotOnly, scope, datasetGenerationKey(opts.DatasetGeneration)), nil
}

func referenceKey(opts PopulatedReferenceOptions) (string, error) {
	scope, err := authScopeKey(opts.AuthResourcePaths, opts.AuthResourcePathsUnrestricted)
	if err != nil {
		return "", err
	}
	mode := opts.Mode
	if mode == "" {
		mode = TraversalModeStorage
	}
	filter := opts.FromType
	if mode == TraversalModeBuilder {
		filter = opts.NodeType
	}
	return fmt.Sprintf("%s|%s|%s|%s|%s", strings.TrimSpace(opts.Project), strings.TrimSpace(filter), mode, scope, datasetGenerationKey(opts.DatasetGeneration)), nil
}

func datasetGenerationKey(generation string) string {
	generation = NormalizeDatasetGeneration(generation)
	if !HasDatasetGeneration(generation) {
		return "legacy"
	}
	encoded, _ := json.Marshal(generation)
	return "generation:" + string(encoded)
}

func authScopeKey(paths []string, explicitUnrestricted *bool) (string, error) {
	if EffectiveAuthResourcePathsUnrestricted(paths, explicitUnrestricted) {
		return "unrestricted", nil
	}
	normalized := append([]string(nil), paths...)
	sort.Strings(normalized)
	encoded, err := json.Marshal(normalized)
	if err != nil {
		return "", err
	}
	return "restricted:" + string(encoded), nil
}

func cloneFields(in []PopulatedField) []PopulatedField {
	if len(in) == 0 {
		return []PopulatedField{}
	}
	out := make([]PopulatedField, len(in))
	for i := range in {
		out[i] = in[i]
		if in[i].DistinctValues != nil {
			out[i].DistinctValues = append([]string(nil), in[i].DistinctValues...)
		}
		if in[i].PivotColumns != nil {
			out[i].PivotColumns = append([]string(nil), in[i].PivotColumns...)
		}
		if in[i].ExtensionValues != nil {
			out[i].ExtensionValues = append([]ExtensionValueObservation(nil), in[i].ExtensionValues...)
			for j := range out[i].ExtensionValues {
				out[i].ExtensionValues[j].URLPath = append([]string(nil), in[i].ExtensionValues[j].URLPath...)
			}
		}
	}
	return out
}

func cloneReferences(in []PopulatedReference) []PopulatedReference {
	if len(in) == 0 {
		return []PopulatedReference{}
	}
	out := make([]PopulatedReference, len(in))
	copy(out, in)
	return out
}
