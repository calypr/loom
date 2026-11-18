package arango

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/calypr/loom/internal/explorer/capability"
	store "github.com/calypr/loom/internal/store/arango"
)

type revisionSnapshotClient struct {
	mu              sync.Mutex
	row             map[string]any
	err             error
	override        map[string]any
	queries         int
	full            int
	markers         int
	afterVisitError error
	afterRead       func(context.Context)
}

func (c *revisionSnapshotClient) WithTransaction(ctx context.Context, _ store.TransactionCollections, fn store.TransactionFunc) error {
	return fn(ctx, c)
}
func (c *revisionSnapshotClient) QueryRows(ctx context.Context, query string, _ int, binds map[string]any, visit store.RowVisitor) error {
	if query != capabilitySnapshotGetByTokenAQL {
		return fmt.Errorf("unexpected query")
	}
	if !strings.Contains(query, `d._rev == @revision`) || !strings.Contains(query, `_loomCapabilityUnchanged: true`) {
		return fmt.Errorf("query does not validate revision")
	}
	c.mu.Lock()
	c.queries++
	err := c.err
	row := c.row
	override := c.override
	hook := c.afterRead
	afterVisitError := c.afterVisitError
	if err != nil {
		c.mu.Unlock()
		return err
	}
	if override != nil {
		row = override
	} else if row != nil && (row["_key"] != binds["key"] || row["token"] != binds["token"]) {
		row = nil
	}
	if override == nil && row != nil {
		revision, _ := binds["revision"].(string)
		if revision != "" && row["_rev"] == revision {
			row = map[string]any{"_key": row["_key"], "token": row["token"], "_rev": row["_rev"], "_loomCapabilityUnchanged": true}
			c.markers++
		} else {
			c.full++
		}
	}
	c.mu.Unlock()
	if hook != nil {
		hook(ctx)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if row == nil {
		return nil
	}
	if err := visit(row); err != nil {
		return err
	}
	return afterVisitError
}
func (c *revisionSnapshotClient) replace(row map[string]any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.row = row
}
func revisionSnapshotDocument(t *testing.T, snapshot capability.Snapshot, revision string) map[string]any {
	t.Helper()
	row, err := canonicalSnapshotDocument(snapshot, capabilitySnapshotKey(snapshot.Token))
	if err != nil {
		t.Fatal(err)
	}
	if revision != "" {
		row["_rev"] = revision
	}
	return row
}
func revisionSnapshotStore(t *testing.T) (*CapabilitySnapshotStore, *revisionSnapshotClient, capability.Snapshot) {
	t.Helper()
	snapshot := testCapabilitySnapshot()
	c := &revisionSnapshotClient{row: revisionSnapshotDocument(t, snapshot, "r1")}
	s, err := NewCapabilitySnapshotStore(c)
	if err != nil {
		t.Fatal(err)
	}
	return s, c, snapshot
}

func TestCapabilitySnapshotRevisionReuseReturnsIndependentSnapshots(t *testing.T) {
	s, c, snapshot := revisionSnapshotStore(t)
	first, err := s.GetByToken(context.Background(), snapshot.Token)
	if err != nil {
		t.Fatal(err)
	}
	first.Diagnostics[0].Message = "caller changed"
	first.Identity.Project = "caller changed"
	for i := 0; i < 2; i++ {
		got, err := s.GetByToken(context.Background(), snapshot.Token)
		if err != nil {
			t.Fatal(err)
		}
		if got.Identity.Project != snapshot.Identity.Project || got.Diagnostics[0].Message != snapshot.Diagnostics[0].Message {
			t.Fatal("caller mutated cached snapshot")
		}
		got.Diagnostics[0].Message = "another mutation"
	}
	if c.queries != 3 || c.full != 1 || c.markers != 2 {
		t.Fatalf("queries=%d full=%d markers=%d", c.queries, c.full, c.markers)
	}
}

func TestCapabilitySnapshotRevisionChangesRequireFullValidation(t *testing.T) {
	s, c, snapshot := revisionSnapshotStore(t)
	if _, err := s.GetByToken(context.Background(), snapshot.Token); err != nil {
		t.Fatal(err)
	}
	tampered := revisionSnapshotDocument(t, snapshot, "r2")
	tampered["complete"] = true
	c.replace(tampered)
	if _, err := s.GetByToken(context.Background(), snapshot.Token); !errors.Is(err, capability.ErrInvalidSnapshot) {
		t.Fatalf("tampered revision error=%v", err)
	}
	c.replace(revisionSnapshotDocument(t, snapshot, "r3"))
	if _, err := s.GetByToken(context.Background(), snapshot.Token); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetByToken(context.Background(), snapshot.Token); err != nil {
		t.Fatal(err)
	}
	if c.full != 3 || c.markers != 1 {
		t.Fatalf("full=%d markers=%d", c.full, c.markers)
	}
}

func TestCapabilitySnapshotRevisionReuseFailsClosed(t *testing.T) {
	for _, kind := range []string{"deleted", "different-token", "different-scope", "database-error", "canceled", "canceled-after-read"} {
		t.Run(kind, func(t *testing.T) {
			s, c, snapshot := revisionSnapshotStore(t)
			if _, err := s.GetByToken(context.Background(), snapshot.Token); err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			token := snapshot.Token
			want := capability.ErrNotFound
			switch kind {
			case "deleted":
				c.replace(nil)
			case "different-token":
				token = "sha256:other"
			case "different-scope":
				identity := snapshot.Identity
				identity.AuthorizationScopeDigest = "other-scope"
				other := capability.NewSnapshot(identity, snapshot.Policy, snapshot.Status, snapshot.Complete, snapshot.Truncated, snapshot.Nodes, snapshot.Edges, snapshot.Candidates, snapshot.Diagnostics)
				c.replace(revisionSnapshotDocument(t, other, "r2"))
			case "database-error":
				want = errors.New("database unavailable")
				c.err = want
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
				want = context.Canceled
			case "canceled-after-read":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				c.afterRead = func(context.Context) { cancel() }
				want = context.Canceled
			}
			got, err := s.GetByToken(ctx, token)
			if got != nil || !errors.Is(err, want) {
				t.Fatalf("returned snapshot=%v error=%v want=%v", got != nil, err, want)
			}
		})
	}
}

func TestCapabilitySnapshotRevisionMarkerRequiresExactIdentity(t *testing.T) {
	for _, kind := range []string{"no-cache", "token", "key", "revision", "empty-revision", "marker-type", "extra-payload"} {
		t.Run(kind, func(t *testing.T) {
			s, c, snapshot := revisionSnapshotStore(t)
			if kind != "no-cache" {
				if _, err := s.GetByToken(context.Background(), snapshot.Token); err != nil {
					t.Fatal(err)
				}
			}
			marker := map[string]any{"_key": capabilitySnapshotKey(snapshot.Token), "token": snapshot.Token, "_rev": "r1", "_loomCapabilityUnchanged": true}
			switch kind {
			case "token":
				marker["token"] = "other"
			case "key":
				marker["_key"] = "other"
			case "revision":
				marker["_rev"] = "r2"
			case "empty-revision":
				marker["_rev"] = ""
			case "marker-type":
				marker["_loomCapabilityUnchanged"] = "true"
			case "extra-payload":
				marker["status"] = "READY"
			}
			c.override = marker
			got, err := s.GetByToken(context.Background(), snapshot.Token)
			if got != nil || !errors.Is(err, capability.ErrInvalidSnapshot) {
				t.Fatalf("invalid marker returned snapshot=%v error=%v", got != nil, err)
			}
		})
	}
}

func TestCapabilitySnapshotWithoutRevisionIsNeverReused(t *testing.T) {
	s, c, snapshot := revisionSnapshotStore(t)
	c.replace(revisionSnapshotDocument(t, snapshot, ""))
	for i := 0; i < 2; i++ {
		if _, err := s.GetByToken(context.Background(), snapshot.Token); err != nil {
			t.Fatal(err)
		}
	}
	if c.full != 2 || c.markers != 0 || s.snapshots.get(snapshot.Token) != nil {
		t.Fatalf("full=%d markers=%d", c.full, c.markers)
	}
}

func TestCapabilitySnapshotRevisionCacheBounds(t *testing.T) {
	snapshot := testCapabilitySnapshot()
	entry := func(token string, bytes int) *snapshotRevisionEntry {
		return &snapshotRevisionEntry{token: token, revision: "r1", snapshot: snapshot, bytes: bytes}
	}
	cache := newSnapshotRevisionCache(2, 10)
	cache.put(entry("a", 4))
	cache.put(entry("b", 4))
	cache.get("a")
	cache.put(entry("c", 4))
	if cache.get("b") != nil || cache.get("a") == nil || cache.get("c") == nil {
		t.Fatal("entry bound did not evict least recently used snapshot")
	}
	cache.put(entry("d", 8))
	if cache.bytes != 8 || len(cache.entries) != 1 || cache.get("d") == nil {
		t.Fatal("byte bound did not evict smaller snapshots")
	}
	cache.put(entry("oversized", 11))
	if cache.get("oversized") != nil || cache.bytes != 8 {
		t.Fatal("oversized snapshot was cached")
	}
	cache.put(entry("d", 3))
	if cache.bytes != 3 || len(cache.entries) != 1 {
		t.Fatal("replacement weight was not updated")
	}
}

func TestCapabilitySnapshotRevisionReuseConcurrentReaders(t *testing.T) {
	s, c, snapshot := revisionSnapshotStore(t)
	if _, err := s.GetByToken(context.Background(), snapshot.Token); err != nil {
		t.Fatal(err)
	}
	const readers = 24
	var wg sync.WaitGroup
	errs := make(chan error, readers)
	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := s.GetByToken(context.Background(), snapshot.Token)
			if err == nil {
				if got.Diagnostics[0].Message != snapshot.Diagnostics[0].Message {
					err = fmt.Errorf("shared mutable snapshot")
				}
				got.Diagnostics[0].Message = "private change"
			}
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if c.queries != readers+1 || c.markers != readers {
		t.Fatalf("queries=%d markers=%d", c.queries, c.markers)
	}
}

func TestCapabilitySnapshotRevisionRegressionStillChecksDatabase(t *testing.T) {
	s, c, snapshot := revisionSnapshotStore(t)
	if _, err := s.GetByToken(context.Background(), snapshot.Token); err != nil {
		t.Fatal(err)
	}
	older := s.snapshots.get(snapshot.Token)
	c.replace(revisionSnapshotDocument(t, snapshot, "r2"))
	if _, err := s.GetByToken(context.Background(), snapshot.Token); err != nil {
		t.Fatal(err)
	}

	s.snapshots.put(older)
	if _, err := s.GetByToken(context.Background(), snapshot.Token); err != nil {
		t.Fatal(err)
	}
	if c.full != 3 || s.snapshots.get(snapshot.Token).revision != "r2" {
		t.Fatalf("full=%d cached revision=%s", c.full, s.snapshots.get(snapshot.Token).revision)
	}
}

func TestCapabilitySnapshotCursorErrorDoesNotPublishCacheEntry(t *testing.T) {
	s, c, snapshot := revisionSnapshotStore(t)
	failure := errors.New("cursor failed after row")
	c.afterVisitError = failure
	got, err := s.GetByToken(context.Background(), snapshot.Token)
	if got != nil || !errors.Is(err, failure) || s.snapshots.get(snapshot.Token) != nil {
		t.Fatalf("cursor failure published a snapshot: got=%v err=%v", got != nil, err)
	}
}

func TestCapabilitySnapshotStoreDoesNotReuseOversizedDocument(t *testing.T) {
	s, c, snapshot := revisionSnapshotStore(t)
	s.snapshots = newSnapshotRevisionCache(2, 1)
	for i := 0; i < 2; i++ {
		if _, err := s.GetByToken(context.Background(), snapshot.Token); err != nil {
			t.Fatal(err)
		}
	}
	if c.full != 2 || c.markers != 0 {
		t.Fatalf("oversized document reused: full=%d markers=%d", c.full, c.markers)
	}
}
