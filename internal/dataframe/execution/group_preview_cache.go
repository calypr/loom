package execution

import (
	"container/list"
	"encoding/json"
	"sync"
)

const (
	groupPreviewCacheMaxBytes      = 8 << 20
	groupPreviewCacheMaxEntryBytes = 1 << 20
	groupPreviewCacheMaxEntries    = 64
	groupPreviewCacheEntryOverhead = 128
	groupPreviewCacheRowOverhead   = 24
)

// groupPreviewRowsKey identifies raw rows for one exact preview query and
// source collection version. It deliberately retains the full query and
// canonical bind JSON so a cache hit cannot depend on a digest collision.
type groupPreviewRowsKey struct {
	query      string
	bindVars   string
	collection string
	revision   string
	limit      int
}

func newGroupPreviewRowsKey(query string, bindVars map[string]any, collection, revision string, limit int) (groupPreviewRowsKey, int, bool) {
	if query == "" || collection == "" || revision == "" || limit <= 0 {
		return groupPreviewRowsKey{}, 0, false
	}
	canonicalBinds, err := json.Marshal(bindVars)
	if err != nil {
		return groupPreviewRowsKey{}, 0, false
	}
	key := groupPreviewRowsKey{
		query:      query,
		bindVars:   string(canonicalBinds),
		collection: collection,
		revision:   revision,
		limit:      limit,
	}
	weight := groupPreviewCacheEntryOverhead + len(key.query) + len(key.bindVars) + len(key.collection) + len(key.revision) + 16
	return key, weight, weight <= groupPreviewCacheMaxEntryBytes
}

type groupPreviewRowsEntry struct {
	key    groupPreviewRowsKey
	rows   [][]byte
	weight int
}

// groupPreviewRowsCache is a zero-value-usable LRU. Entries contain immutable
// JSON-encoded raw query rows; consumers decode fresh maps before applying the
// normal post-query checks and visitor.
type groupPreviewRowsCache struct {
	mu      sync.Mutex
	entries map[groupPreviewRowsKey]*list.Element
	lru     list.List
	bytes   int
}

func (c *groupPreviewRowsCache) get(key groupPreviewRowsKey) ([][]byte, bool) {
	if c == nil {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	element := c.entries[key]
	if element == nil {
		return nil, false
	}
	c.lru.MoveToFront(element)
	entry := element.Value.(groupPreviewRowsEntry)
	rows := make([][]byte, len(entry.rows))
	for index, row := range entry.rows {
		rows[index] = append([]byte(nil), row...)
	}
	return rows, true
}

func (c *groupPreviewRowsCache) delete(key groupPreviewRowsKey) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if element := c.entries[key]; element != nil {
		c.remove(element)
	}
}

func (c *groupPreviewRowsCache) put(key groupPreviewRowsKey, rows [][]byte) {
	if c == nil {
		return
	}
	weight := groupPreviewCacheEntryOverhead + len(key.query) + len(key.bindVars) + len(key.collection) + len(key.revision) + 16
	for _, row := range rows {
		weight += len(row) + groupPreviewCacheRowOverhead
		if weight > groupPreviewCacheMaxEntryBytes {
			return
		}
	}
	if weight > groupPreviewCacheMaxEntryBytes {
		return
	}
	ownedRows := make([][]byte, len(rows))
	for index, row := range rows {
		ownedRows[index] = append([]byte(nil), row...)
	}
	entry := groupPreviewRowsEntry{key: key, rows: ownedRows, weight: weight}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = make(map[groupPreviewRowsKey]*list.Element)
	}
	if existing := c.entries[key]; existing != nil {
		c.remove(existing)
	}
	element := c.lru.PushFront(entry)
	c.entries[key] = element
	c.bytes += weight
	for len(c.entries) > groupPreviewCacheMaxEntries || c.bytes > groupPreviewCacheMaxBytes {
		c.remove(c.lru.Back())
	}
}

func (c *groupPreviewRowsCache) remove(element *list.Element) {
	if element == nil {
		return
	}
	entry := element.Value.(groupPreviewRowsEntry)
	delete(c.entries, entry.key)
	c.bytes -= entry.weight
	c.lru.Remove(element)
}

type groupPreviewRowsCapture struct {
	keyWeight int
	weight    int
	rows      [][]byte
	cacheable bool
}

func newGroupPreviewRowsCapture(keyWeight int) groupPreviewRowsCapture {
	return groupPreviewRowsCapture{keyWeight: keyWeight, cacheable: keyWeight <= groupPreviewCacheMaxEntryBytes}
}

func (c *groupPreviewRowsCapture) encode(row map[string]any) []byte {
	if !c.cacheable {
		return nil
	}
	encoded, err := json.Marshal(row)
	if err != nil || c.keyWeight+c.weight+len(encoded)+groupPreviewCacheRowOverhead > groupPreviewCacheMaxEntryBytes {
		c.cacheable = false
		c.rows = nil
		c.weight = 0
		return nil
	}
	return encoded
}

func (c *groupPreviewRowsCapture) append(encoded []byte) {
	if !c.cacheable || encoded == nil {
		return
	}
	c.rows = append(c.rows, encoded)
	c.weight += len(encoded) + groupPreviewCacheRowOverhead
}

func decodeGroupPreviewRows(encodedRows [][]byte) ([]map[string]any, bool) {
	rows := make([]map[string]any, len(encodedRows))
	for index, encoded := range encodedRows {
		if err := json.Unmarshal(encoded, &rows[index]); err != nil || rows[index] == nil {
			return nil, false
		}
	}
	return rows, true
}
