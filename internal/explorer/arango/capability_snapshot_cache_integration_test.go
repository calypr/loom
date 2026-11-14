package arango

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/calypr/loom/internal/explorer/capability"
	store "github.com/calypr/loom/internal/store/arango"
	"github.com/google/uuid"
)

const capabilityCacheDatabasePrefix = "loom_verify_capcache_"

type capabilitySnapshotQueryObserver struct {
	*store.Client
	mu             sync.Mutex
	queryCount     int
	serializedRows []int
	fail           error
}

func (c *capabilitySnapshotQueryObserver) QueryRows(ctx context.Context, query string, batch int, binds map[string]any, visit store.RowVisitor) error {
	c.mu.Lock()
	c.queryCount++
	fail := c.fail
	c.mu.Unlock()
	if fail != nil {
		return fail
	}
	return c.Client.QueryRows(ctx, query, batch, binds, func(row map[string]any) error {
		encoded, err := json.Marshal(row)
		if err != nil {
			return err
		}
		c.mu.Lock()
		c.serializedRows = append(c.serializedRows, len(encoded))
		c.mu.Unlock()
		return visit(row)
	})
}

func (c *capabilitySnapshotQueryObserver) reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.queryCount = 0
	c.serializedRows = nil
}

func (c *capabilitySnapshotQueryObserver) stats() (int, []int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.queryCount, append([]int(nil), c.serializedRows...)
}

func (c *capabilitySnapshotQueryObserver) setFailure(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.fail = err
}

func TestCapabilitySnapshotStoreCacheAgainstArango(t *testing.T) {
	endpoint := os.Getenv("LOOM_TEST_ARANGO_URL")
	if endpoint == "" {
		t.Skip("set LOOM_TEST_ARANGO_URL to run capability snapshot cache against a disposable database")
	}

	database := capabilityCacheDatabasePrefix + strings.ReplaceAll(uuid.NewString(), "-", "")
	if !strings.HasPrefix(database, capabilityCacheDatabasePrefix) {
		t.Fatalf("verification database %q escapes the owned prefix", database)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	client, err := store.Open(ctx, endpoint, database)
	if err != nil {
		t.Fatalf("open private capability cache database: %v", err)
	}
	t.Logf("CAPABILITY_CACHE_DATABASE=%s", database)
	t.Cleanup(func() {
		_ = client.Close(context.Background())
		cleanupCtx, stop := context.WithTimeout(context.Background(), 15*time.Second)
		defer stop()
		request, err := http.NewRequestWithContext(cleanupCtx, http.MethodDelete, strings.TrimRight(endpoint, "/")+"/_api/database/"+url.PathEscape(database), nil)
		if err != nil {
			t.Errorf("build private database cleanup request: %v", err)
			return
		}
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Errorf("remove private database %s: %v", database, err)
			return
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Errorf("remove private database %s: status %d", database, response.StatusCode)
			return
		}
		t.Logf("CAPABILITY_CACHE_DATABASE_REMOVED=%s", database)
	})
	if err := client.Bootstrap(ctx, store.BootstrapSpec{Collections: []store.CollectionSpec{CapabilitySnapshotCollectionSpec()}}); err != nil {
		t.Fatalf("bootstrap only capability snapshot collection: %v", err)
	}

	observer := &capabilitySnapshotQueryObserver{Client: client}
	snapshots, err := NewCapabilitySnapshotStore(observer)
	if err != nil {
		t.Fatal(err)
	}
	original := largeCapabilityCacheSnapshot("generation-a")
	if _, err := snapshots.Put(ctx, original); err != nil {
		t.Fatalf("put synthetic snapshot: %v", err)
	}

	observer.reset()
	cold, err := snapshots.GetByToken(ctx, original.Token)
	if err != nil {
		t.Fatalf("cold get by token: %v", err)
	}
	assertCapabilityCacheRead(t, observer, "cold", true)
	if !sameCapabilitySnapshot(cold, original) {
		t.Fatal("cold get did not return the complete canonical snapshot")
	}

	observer.reset()
	warm, err := snapshots.GetByToken(ctx, original.Token)
	if err != nil {
		t.Fatalf("warm get by token: %v", err)
	}
	assertCapabilityCacheRead(t, observer, "warm", false)
	if !sameCapabilitySnapshot(warm, original) {
		t.Fatal("warm get did not return the complete canonical snapshot")
	}

	cold.Nodes[0].ResourceType = "MutatedByCaller"
	cold.Diagnostics[0].Details["marker"] = "mutated"
	observer.reset()
	cloneCheck, err := snapshots.GetByToken(ctx, original.Token)
	if err != nil {
		t.Fatalf("get after mutating returned clone: %v", err)
	}
	assertCapabilityCacheRead(t, observer, "clone-isolation warm", false)
	if !sameCapabilitySnapshot(cloneCheck, original) {
		t.Fatal("mutating a returned snapshot changed the cached or persisted value")
	}

	wrongToken := capability.NewSnapshot(cacheSnapshotIdentity("generation-wrong-token"), capability.Policy{}, capability.StatusReady, true, false, nil, nil, nil, nil)
	observer.reset()
	if _, err := snapshots.GetByToken(ctx, wrongToken.Token); !errors.Is(err, capability.ErrNotFound) {
		t.Fatalf("unknown exact token lookup = %v, want not found", err)
	}
	assertCapabilityCacheQueryCount(t, observer, "unknown-token")

	other := largeCapabilityCacheSnapshot("generation-b")
	if _, err := snapshots.Put(ctx, other); err != nil {
		t.Fatalf("put second valid snapshot: %v", err)
	}
	observer.reset()
	gotOther, err := snapshots.GetByToken(ctx, other.Token)
	if err != nil {
		t.Fatalf("get second exact token: %v", err)
	}
	assertCapabilityCacheRead(t, observer, "second-token cold", true)
	if !sameCapabilitySnapshot(gotOther, other) || gotOther.Token == original.Token {
		t.Fatal("second token lookup returned the wrong snapshot")
	}
	observer.reset()
	gotOriginal, err := snapshots.GetByToken(ctx, original.Token)
	if err != nil || !sameCapabilitySnapshot(gotOriginal, original) {
		t.Fatalf("original token lookup after caching second token = %#v, err=%v", gotOriginal, err)
	}
	assertCapabilityCacheRead(t, observer, "original token after second token", false)

	key := capabilitySnapshotKey(original.Token)
	if err := client.ExecuteAQL(ctx, `
FOR d IN @@c
  FILTER d._key == @key
  UPDATE d WITH {status: "FAILED"} IN @@c
  RETURN NEW
`, map[string]any{"@c": CapabilitySnapshotCollection, "key": key}); err != nil {
		t.Fatalf("tamper private snapshot document: %v", err)
	}
	observer.reset()
	if _, err := snapshots.GetByToken(ctx, original.Token); !errors.Is(err, capability.ErrInvalidSnapshot) {
		t.Fatalf("tampered document read = %v, want invalid snapshot", err)
	}
	assertCapabilityCacheRead(t, observer, "tampered revision", true)

	doc, err := canonicalSnapshotDocument(original, key)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.ExecuteAQL(ctx, `
FOR d IN @@c
  FILTER d._key == @key
  REPLACE @doc IN @@c
  RETURN NEW
`, map[string]any{"@c": CapabilitySnapshotCollection, "key": key, "doc": doc}); err != nil {
		t.Fatalf("restore private snapshot document: %v", err)
	}
	observer.reset()
	restored, err := snapshots.GetByToken(ctx, original.Token)
	if err != nil || !sameCapabilitySnapshot(restored, original) {
		t.Fatalf("get restored snapshot = %#v, err=%v", restored, err)
	}
	assertCapabilityCacheRead(t, observer, "restored revision", true)

	if err := client.ExecuteAQL(ctx, `
FOR d IN @@c
  FILTER d._key == @key
  UPDATE d WITH {token: @wrongToken} IN @@c
  RETURN NEW
`, map[string]any{"@c": CapabilitySnapshotCollection, "key": key, "wrongToken": other.Token}); err != nil {
		t.Fatalf("tamper private snapshot token: %v", err)
	}
	observer.reset()
	if _, err := snapshots.GetByToken(ctx, original.Token); !errors.Is(err, capability.ErrNotFound) {
		t.Fatalf("wrong-token document lookup = %v, want not found", err)
	}
	assertCapabilityCacheQueryCount(t, observer, "wrong-token document")
	observer.reset()
	stillOther, err := snapshots.GetByToken(ctx, other.Token)
	if err != nil || !sameCapabilitySnapshot(stillOther, other) {
		t.Fatalf("other exact token after token tamper = %#v, err=%v", stillOther, err)
	}
	assertCapabilityCacheRead(t, observer, "other exact token after token tamper", false)
	if err := client.ExecuteAQL(ctx, `
FOR d IN @@c
  FILTER d._key == @key
  REPLACE @doc IN @@c
  RETURN NEW
`, map[string]any{"@c": CapabilitySnapshotCollection, "key": key, "doc": doc}); err != nil {
		t.Fatalf("restore private snapshot token: %v", err)
	}
	observer.reset()
	restored, err = snapshots.GetByToken(ctx, original.Token)
	if err != nil || !sameCapabilitySnapshot(restored, original) {
		t.Fatalf("get snapshot after token restoration = %#v, err=%v", restored, err)
	}
	assertCapabilityCacheRead(t, observer, "restored token revision", true)

	injected := errors.New("injected Arango query failure")
	observer.setFailure(injected)
	observer.reset()
	if got, err := snapshots.GetByToken(ctx, original.Token); !errors.Is(err, injected) || got != nil {
		t.Fatalf("cached get on database failure = %#v, %v; want nil and injected error", got, err)
	}
	assertCapabilityCacheQueryCount(t, observer, "database failure")
	observer.setFailure(nil)

	canceled, cancelRequest := context.WithCancel(ctx)
	cancelRequest()
	observer.reset()
	if got, err := snapshots.GetByToken(canceled, original.Token); !errors.Is(err, context.Canceled) || got != nil {
		t.Fatalf("cached get on canceled request = %#v, %v; want nil and context canceled", got, err)
	}
	if queries, _ := observer.stats(); queries != 0 {
		t.Fatalf("canceled request issued %d database queries, want cancellation before query", queries)
	}

	if err := client.ExecuteAQL(ctx, `
FOR d IN @@c
  FILTER d._key == @key
  REMOVE d IN @@c
  RETURN OLD
`, map[string]any{"@c": CapabilitySnapshotCollection, "key": key}); err != nil {
		t.Fatalf("delete private snapshot document: %v", err)
	}
	observer.reset()
	if _, err := snapshots.GetByToken(ctx, original.Token); !errors.Is(err, capability.ErrNotFound) {
		t.Fatalf("deleted document lookup = %v, want not found despite warm cache", err)
	}
	assertCapabilityCacheQueryCount(t, observer, "deleted document")
}

func sameCapabilitySnapshot(got *capability.Snapshot, want capability.Snapshot) bool {
	return got != nil && got.Token == want.Token && bytes.Equal(got.CanonicalPayload(), want.CanonicalPayload())
}

func assertCapabilityCacheQueryCount(t *testing.T, observer *capabilitySnapshotQueryObserver, label string) {
	t.Helper()
	queries, _ := observer.stats()
	if queries != 1 {
		t.Fatalf("%s cache read issued %d database queries, want one", label, queries)
	}
}

func assertCapabilityCacheRead(t *testing.T, observer *capabilitySnapshotQueryObserver, label string, wantFullDocument bool) {
	t.Helper()
	queries, rows := observer.stats()
	if queries != 1 {
		t.Fatalf("%s cache read issued %d database queries, want one", label, queries)
	}
	if wantFullDocument {
		if len(rows) != 1 || rows[0] <= 1024 {
			t.Fatalf("%s database response row sizes=%v, want one full row >1KiB", label, rows)
		}
		return
	}
	if len(rows) != 1 || rows[0] >= 1024 {
		t.Fatalf("%s database response row sizes=%v, want one compact marker <1KiB", label, rows)
	}
}

func largeCapabilityCacheSnapshot(generation string) capability.Snapshot {
	nodes := make([]capability.Node, 160)
	for index := range nodes {
		nodes[index] = capability.Node{
			ID: fmt.Sprintf("node-%04d", index), ResourceType: fmt.Sprintf("SyntheticResourceType%04d", index),
			RowRootEligible: true, Populated: true, DocumentCount: int64(index + 1),
			SupportedOperations: []capability.Operation{capability.OperationSelect, capability.OperationFilter},
			CapabilityVersion:   strings.Repeat("v", 48),
		}
	}
	return capability.NewSnapshot(
		cacheSnapshotIdentity(generation), capability.Policy{}, capability.StatusReady, true, false,
		nodes, nil, nil,
		[]capability.Diagnostic{{Code: "synthetic", Message: "synthetic immutable snapshot", Details: map[string]any{"marker": "unchanged"}}},
	)
}

func cacheSnapshotIdentity(generation string) capability.SnapshotIdentity {
	return capability.SnapshotIdentity{
		Project: "cache-test-project", Generation: generation, AuthorizationScopeDigest: "cache-test-scope",
		SchemaDigest: "cache-test-schema", ResourceInventoryDigest: "cache-test-inventory",
		RelationshipDigest: "cache-test-relationships", FieldDigest: "cache-test-fields",
		ProtocolVersion: "cache-test-protocol", CompilerVersion: "cache-test-compiler",
		TraversalPolicyVersion: "cache-test-traversal", ProjectionPolicyVersion: "cache-test-projection",
	}
}
