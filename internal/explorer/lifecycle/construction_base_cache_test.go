package lifecycle

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
)

func configureSnapshotConstructionCompilation(service *Service, store *fakeStore, snapshot capability.Snapshot, scope authscope.ReadScope, calls *int) {
	service.config.Capability.ForCompilation = func(context.Context, string, string) (AuthorizedCapability, error) {
		return AuthorizedCapability{Snapshot: snapshot, Scope: scope.Clone()}, nil
	}
	service.config.CompileReceipt = func(_ context.Context, request CompileReceiptRequest) (*explorer.CompilationReceipt, error) {
		*calls++
		receipt := lifecycleConstructionReceipt(snapshot, request.Workspace)
		receipt.ConstructionStages = map[string][]explorer.ReceiptConstructionStage{
			"patients": testConstructionStageDescriptors(request.Workspace, "patients"),
		}
		var err error
		receipt.CompilationKey, err = explorer.CompilationKey(*receipt)
		if err != nil {
			return nil, err
		}
		receipt.ID, err = explorer.ReceiptID(*receipt)
		if err != nil {
			return nil, err
		}
		store.receipt = receipt
		return receipt, nil
	}
}

func constructionCapabilitiesRequestFor(owner *explorer.Explorer, snapshotToken, outputID string) ConstructionCapabilitiesRequest {
	return ConstructionCapabilitiesRequest{
		Project: owner.Project, ExplorerID: owner.ExplorerID, SnapshotToken: snapshotToken,
		ExpectedDraftVersion: owner.DraftVersion, ExpectedDraftDigest: owner.DraftDigest,
		OutputID: outputID, StageID: recipe.ConstructionSourceProjectionID,
	}
}

func TestConstructionBaseCacheSkipsCompileForIdenticalRequest(t *testing.T) {
	service, store, snapshot := constructionProposalService(t)
	calls := 0
	configureSnapshotConstructionCompilation(service, store, snapshot, authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}, &calls)
	request := constructionCapabilitiesRequestFor(store.created, snapshot.Token, "patients")
	forCompilation := service.config.Capability.ForCompilation
	authorizationChecks := 0
	service.config.Capability.ForCompilation = func(ctx context.Context, project, token string) (AuthorizedCapability, error) {
		authorizationChecks++
		return forCompilation(ctx, project, token)
	}

	if _, err := service.GetConstructionCapabilities(context.Background(), request); err != nil {
		t.Fatalf("first capabilities request: %v", err)
	}
	authorizationChecks = 0
	if _, err := service.GetConstructionCapabilities(context.Background(), request); err != nil {
		t.Fatalf("second capabilities request: %v", err)
	}
	if calls != 1 {
		t.Fatalf("compile calls = %d, want 1", calls)
	}
	if authorizationChecks != 1 {
		t.Fatalf("cache-hit authorization checks = %d, want 1", authorizationChecks)
	}
}

func TestConstructionBaseCacheSeparatesDraftVersionAndDigest(t *testing.T) {
	service, store, snapshot := constructionProposalService(t)
	calls := 0
	configureSnapshotConstructionCompilation(service, store, snapshot, authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}, &calls)

	if _, err := service.GetConstructionCapabilities(context.Background(), constructionCapabilitiesRequestFor(store.created, snapshot.Token, "patients")); err != nil {
		t.Fatalf("base capabilities request: %v", err)
	}
	store.created.DraftVersion++
	if _, err := service.GetConstructionCapabilities(context.Background(), constructionCapabilitiesRequestFor(store.created, snapshot.Token, "patients")); err != nil {
		t.Fatalf("new draft version request: %v", err)
	}
	if calls != 2 {
		t.Fatalf("compile calls after draft version change = %d, want 2", calls)
	}

	workspace, err := authoringv2.DecodeWorkspace(store.created.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	workspace.Explorer.Title = "Changed digest"
	raw, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	store.created.DraftConfig, store.created.DraftDigest = raw, digest
	if _, err := service.GetConstructionCapabilities(context.Background(), constructionCapabilitiesRequestFor(store.created, snapshot.Token, "patients")); err != nil {
		t.Fatalf("new draft digest request: %v", err)
	}
	if calls != 3 {
		t.Fatalf("compile calls after draft digest change = %d, want 3", calls)
	}
}

func TestConstructionBaseCacheSeparatesSnapshotTokenAndScopeDigest(t *testing.T) {
	service, store, snapshot := constructionProposalService(t)
	calls := 0
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	configureSnapshotConstructionCompilation(service, store, snapshot, scope, &calls)

	if _, err := service.GetConstructionCapabilities(context.Background(), constructionCapabilitiesRequestFor(store.created, snapshot.Token, "patients")); err != nil {
		t.Fatalf("base capabilities request: %v", err)
	}

	otherToken := readySnapshot("project-a", "generation-a", "snapshot-other", scope)
	configureSnapshotConstructionCompilation(service, store, otherToken, scope, &calls)
	if _, err := service.GetConstructionCapabilities(context.Background(), constructionCapabilitiesRequestFor(store.created, otherToken.Token, "patients")); err != nil {
		t.Fatalf("other token request: %v", err)
	}
	if calls != 2 {
		t.Fatalf("compile calls after snapshot token change = %d, want 2", calls)
	}

	service, store, snapshot = constructionProposalService(t)
	calls = 0
	configureSnapshotConstructionCompilation(service, store, snapshot, scope, &calls)
	if _, err := service.GetConstructionCapabilities(context.Background(), constructionCapabilitiesRequestFor(store.created, snapshot.Token, "patients")); err != nil {
		t.Fatalf("base scope capabilities request: %v", err)
	}
	otherScope := authscope.ReadScope{Mode: authscope.ReadScopeRestricted, AuthResourcePaths: []string{"/x"}}
	scopedSnapshot := readySnapshot("project-a", "generation-a", snapshot.Token, otherScope)
	configureSnapshotConstructionCompilation(service, store, scopedSnapshot, otherScope, &calls)
	if _, err := service.GetConstructionCapabilities(context.Background(), constructionCapabilitiesRequestFor(store.created, scopedSnapshot.Token, "patients")); err != nil {
		t.Fatalf("other scope request: %v", err)
	}
	if calls != 2 {
		t.Fatalf("compile calls after scope digest change = %d, want 2", calls)
	}
}

func TestConstructionBaseCacheSeparatesOutputID(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	cache := newConstructionBaseCache(time.Hour, 16, func() time.Time { return now })
	base := constructionBaseCacheKey{
		Format: constructionBaseCacheFormatVersion, Project: "project", ExplorerID: "explorer",
		DraftVersion: 1, DraftDigest: "digest", SnapshotToken: "token", AuthorizationScopeDigest: "scope",
	}
	patientsKey := base
	patientsKey.OutputID = "patients"
	siblingKey := base
	siblingKey.OutputID = "sibling"
	cache.Put(patientsKey, constructionBaseCacheValue{})
	if _, ok := cache.Get(siblingKey); ok {
		t.Fatal("cache reused an entry for a different output ID")
	}
	if _, ok := cache.Get(patientsKey); !ok {
		t.Fatal("cache missed the original output ID")
	}
}

func TestConstructionBaseCacheHitStillRejectsStaleDraft(t *testing.T) {
	service, store, snapshot := constructionProposalService(t)
	calls := 0
	configureSnapshotConstructionCompilation(service, store, snapshot, authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}, &calls)
	request := constructionCapabilitiesRequestFor(store.created, snapshot.Token, "patients")
	if _, err := service.GetConstructionCapabilities(context.Background(), request); err != nil {
		t.Fatalf("first capabilities request: %v", err)
	}
	store.created.DraftDigest = "stale-digest"
	_, err := service.GetConstructionCapabilities(context.Background(), request)
	if lifecycleErrorCode(err) != "DRAFT_CONFLICT" {
		t.Fatalf("stale cache-hit error = %v, want DRAFT_CONFLICT", err)
	}
	if calls != 1 {
		t.Fatalf("compile calls = %d, want 1", calls)
	}
}

func TestConstructionBaseCacheDoesNotCacheErrors(t *testing.T) {
	service, store, snapshot := constructionProposalService(t)
	calls := 0
	configureSnapshotConstructionCompilation(service, store, snapshot, authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}, &calls)
	success := service.config.CompileReceipt
	service.config.CompileReceipt = func(context.Context, CompileReceiptRequest) (*explorer.CompilationReceipt, error) {
		return nil, errors.New("temporary compile failure")
	}
	request := constructionCapabilitiesRequestFor(store.created, snapshot.Token, "patients")
	if _, err := service.GetConstructionCapabilities(context.Background(), request); err == nil {
		t.Fatal("expected compile failure")
	}
	service.config.CompileReceipt = success
	if _, err := service.GetConstructionCapabilities(context.Background(), request); err != nil {
		t.Fatalf("retry capabilities request: %v", err)
	}
	if calls != 1 {
		t.Fatalf("successful compile calls = %d, want 1", calls)
	}
}

func TestConstructionBaseCacheReturnsIndependentCopies(t *testing.T) {
	service, store, snapshot := constructionProposalService(t)
	calls := 0
	configureSnapshotConstructionCompilation(service, store, snapshot, authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}, &calls)
	request := constructionCapabilitiesRequestFor(store.created, snapshot.Token, "patients")
	first, err := service.GetConstructionCapabilities(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Stages) == 0 || len(first.Stages[0].Columns) == 0 {
		t.Fatal("expected stage columns")
	}
	originalColumn := first.Stages[0].Columns[0].Name
	first.Stages[0].Columns[0].Name = "corrupted"
	first.Stages[0].Capabilities = nil
	if len(first.BaseConstruction.Steps) > 0 {
		first.BaseConstruction.Steps[0].ID = "corrupted"
	}
	second, err := service.GetConstructionCapabilities(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if second.Stages[0].Columns[0].Name != originalColumn {
		t.Fatalf("cached stage column = %q, want %q", second.Stages[0].Columns[0].Name, originalColumn)
	}
	if len(second.Stages[0].Capabilities) == 0 {
		t.Fatal("cached stage capabilities were corrupted")
	}
	if len(second.BaseConstruction.Steps) > 0 && second.BaseConstruction.Steps[0].ID == "corrupted" {
		t.Fatal("cached construction was corrupted")
	}
	if calls != 1 {
		t.Fatalf("compile calls = %d, want 1", calls)
	}
}

func TestConstructionBaseCacheExpiresEntries(t *testing.T) {
	service, store, snapshot := constructionProposalService(t)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	service.constructionBaseCache = newConstructionBaseCache(time.Minute, 16, func() time.Time { return now })
	calls := 0
	configureSnapshotConstructionCompilation(service, store, snapshot, authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}, &calls)
	request := constructionCapabilitiesRequestFor(store.created, snapshot.Token, "patients")
	if _, err := service.GetConstructionCapabilities(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Minute)
	if _, err := service.GetConstructionCapabilities(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("compile calls after expiry = %d, want 2", calls)
	}
}

func TestConstructionBaseCacheEvictsOldestEntry(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	cache := newConstructionBaseCache(time.Hour, 2, func() time.Time { return now })
	key := func(outputID string) constructionBaseCacheKey {
		return constructionBaseCacheKey{Format: constructionBaseCacheFormatVersion, Project: "project", ExplorerID: "explorer", DraftVersion: 1, DraftDigest: "digest", SnapshotToken: "token", OutputID: outputID}
	}
	cache.Put(key("a"), constructionBaseCacheValue{})
	cache.Put(key("b"), constructionBaseCacheValue{})
	cache.Put(key("c"), constructionBaseCacheValue{})
	if _, ok := cache.Get(key("a")); ok {
		t.Fatal("oldest cache entry was not evicted")
	}
	if _, ok := cache.Get(key("b")); !ok {
		t.Fatal("cache entry b should remain")
	}
	if _, ok := cache.Get(key("c")); !ok {
		t.Fatal("cache entry c should remain")
	}
}

func TestTTLCacheSamplesPutClockWhileLocked(t *testing.T) {
	now := time.Unix(100, 0)
	var cache *ttlCache[string, string]
	sampledWhileLocked := false
	cache = newTTLCache[string, string](time.Minute, 2, func() time.Time {
		if cache.mu.TryLock() {
			cache.mu.Unlock()
		} else {
			sampledWhileLocked = true
		}
		return now
	}, time.Minute, 2)

	cache.Put("key", "value")
	if !sampledWhileLocked {
		t.Fatal("generic Put sampled its clock before acquiring the cache lock")
	}
}

func TestConstructionBaseCacheSamplesClockBeforeLock(t *testing.T) {
	now := time.Unix(100, 0)
	var cache *constructionBaseCache
	sampledUnlocked := false
	cache = newConstructionBaseCache(time.Minute, 2, func() time.Time {
		if cache.entries.mu.TryLock() {
			sampledUnlocked = true
			cache.entries.mu.Unlock()
		}
		return now
	})

	cache.Put(constructionBaseCacheKey{OutputID: "out"}, constructionBaseCacheValue{})
	if !sampledUnlocked {
		t.Fatal("construction base cache sampled its clock after acquiring the cache lock")
	}
}
