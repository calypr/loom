package capabilitydb

import (
	"container/list"
	"sync"

	"github.com/calypr/loom/internal/explorer/capability"
)

const (
	capabilitySnapshotCacheMaxEntries = 8
	capabilitySnapshotCacheMaxBytes   = 64 << 20
)

type snapshotRevisionEntry struct {
	token    string
	revision string
	snapshot capability.Snapshot
	bytes    int
}

type snapshotRevisionCache struct {
	mu         sync.Mutex
	entries    map[string]*list.Element
	lru        *list.List
	bytes      int
	maxEntries int
	maxBytes   int
}

func newSnapshotRevisionCache(maxEntries, maxBytes int) *snapshotRevisionCache {
	return &snapshotRevisionCache{entries: make(map[string]*list.Element), lru: list.New(), maxEntries: maxEntries, maxBytes: maxBytes}
}

func (c *snapshotRevisionCache) get(token string) *snapshotRevisionEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	if element := c.entries[token]; element != nil {
		c.lru.MoveToFront(element)
		return element.Value.(*snapshotRevisionEntry)
	}
	return nil
}

func (c *snapshotRevisionCache) put(entry *snapshotRevisionEntry) {
	if entry.revision == "" || entry.bytes <= 0 || entry.bytes > c.maxBytes || c.maxEntries <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if previous := c.entries[entry.token]; previous != nil {
		c.remove(previous)
	}
	c.entries[entry.token] = c.lru.PushFront(entry)
	c.bytes += entry.bytes
	for len(c.entries) > c.maxEntries || c.bytes > c.maxBytes {
		c.remove(c.lru.Back())
	}
}

func (c *snapshotRevisionCache) remove(element *list.Element) {
	entry := element.Value.(*snapshotRevisionEntry)
	delete(c.entries, entry.token)
	c.bytes -= entry.bytes
	c.lru.Remove(element)
}
