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
	query         string
	bindVars      string
	collection    string
	revision      string
	indexIdentity string
	limit         int
	decision      bool
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
	weight := groupPreviewCacheKeyWeight(key)
	return key, weight, weight <= groupPreviewCacheMaxEntryBytes
}

func newGroupPreviewProjectionDecisionKey(query string, bindVars map[string]any, collection, revision, indexIdentity string) (groupPreviewRowsKey, int, bool) {
	if query == "" || collection == "" || revision == "" || indexIdentity == "" {
		return groupPreviewRowsKey{}, 0, false
	}
	canonicalBinds, err := json.Marshal(bindVars)
	if err != nil {
		return groupPreviewRowsKey{}, 0, false
	}
	key := groupPreviewRowsKey{
		query: query, bindVars: string(canonicalBinds), collection: collection, revision: revision,
		indexIdentity: indexIdentity, decision: true,
	}
	weight := groupPreviewCacheKeyWeight(key) + groupPreviewDecisionWeight
	return key, weight, weight <= groupPreviewCacheMaxEntryBytes
}

const groupPreviewDecisionWeight = 32

func groupPreviewCacheKeyWeight(key groupPreviewRowsKey) int {
	return groupPreviewCacheEntryOverhead + len(key.query) + len(key.bindVars) + len(key.collection) +
		len(key.revision) + len(key.indexIdentity) + 16
}

type groupPreviewProjectionDecision struct {
	Factorized               bool
	SampleRows               int64
	DistinctProjections      int64
	SerializedCharacters     int64
	EstimatedDictionaryBytes uint64
}

type groupPreviewRowsEntry struct {
	key      groupPreviewRowsKey
	rows     [][]byte
	decision *groupPreviewProjectionDecision
	weight   int
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
	if element == nil || key.decision {
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

func (c *groupPreviewRowsCache) getDecision(key groupPreviewRowsKey) (groupPreviewProjectionDecision, bool) {
	if c == nil || !key.decision {
		return groupPreviewProjectionDecision{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	element := c.entries[key]
	if element == nil {
		return groupPreviewProjectionDecision{}, false
	}
	c.lru.MoveToFront(element)
	entry := element.Value.(groupPreviewRowsEntry)
	if entry.decision == nil {
		return groupPreviewProjectionDecision{}, false
	}
	return *entry.decision, true
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
	if c == nil || key.decision {
		return
	}
	weight := groupPreviewCacheKeyWeight(key)
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
	c.putEntry(entry)
}

func (c *groupPreviewRowsCache) putDecision(key groupPreviewRowsKey, decision groupPreviewProjectionDecision) {
	if c == nil || !key.decision {
		return
	}
	weight := groupPreviewCacheKeyWeight(key) + groupPreviewDecisionWeight
	if weight > groupPreviewCacheMaxEntryBytes {
		return
	}
	ownedDecision := decision
	entry := groupPreviewRowsEntry{key: key, decision: &ownedDecision, weight: weight}
	c.putEntry(entry)
}

func (c *groupPreviewRowsCache) putEntry(entry groupPreviewRowsEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = make(map[groupPreviewRowsKey]*list.Element)
	}
	if existing := c.entries[entry.key]; existing != nil {
		c.remove(existing)
	}
	element := c.lru.PushFront(entry)
	c.entries[entry.key] = element
	c.bytes += entry.weight
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
