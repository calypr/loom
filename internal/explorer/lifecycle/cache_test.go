package lifecycle

import (
	"context"
	"errors"
	"testing"
	"time"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/explorer/receiptmodel"

	"github.com/calypr/loom/internal/explorer/construction"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
)

type cacheTestAuthorization struct {
	snapshot capability.Snapshot
	scope    authscope.ReadScope
}

func configureSnapshotConstructionCompilation(config *Config, store *fakeStore, snapshot capability.Snapshot, scope authscope.ReadScope, calls *int) *cacheTestAuthorization {
	authorization := &cacheTestAuthorization{snapshot: snapshot, scope: scope}
	config.Capability.ForCompilation = func(context.Context, string, string) (ports.AuthorizedCapability, error) {
		return ports.AuthorizedCapability{Snapshot: authorization.snapshot, Scope: authorization.scope.Clone()}, nil
	}
	config.CompileReceipt = func(_ context.Context, request ports.CompileReceiptRequest) (*receiptmodel.CompilationReceipt, error) {
		*calls++
		receipt := lifecycleConstructionReceipt(authorization.snapshot, request.Workspace)
		receipt.ConstructionStages = map[string][]receiptmodel.ReceiptConstructionStage{
			"patients": testConstructionStageDescriptors(request.Workspace, "patients"),
		}
		var err error
		receipt.CompilationKey, err = receiptmodel.CompilationKey(*receipt)
		if err != nil {
			return nil, err
		}
		receipt.ID, err = receiptmodel.ReceiptID(*receipt)
		if err != nil {
			return nil, err
		}
		store.receipt = receipt
		return receipt, nil
	}
	return authorization
}

func constructionCapabilitiesRequestFor(owner *explorer.Explorer, snapshotToken, outputID string) construction.ConstructionCapabilitiesRequest {
	return construction.ConstructionCapabilitiesRequest{
		Project: owner.Project, ExplorerID: owner.ExplorerID, SnapshotToken: snapshotToken,
		ExpectedDraftVersion: owner.DraftVersion, ExpectedDraftDigest: owner.DraftDigest,
		OutputID: outputID, StageID: recipe.ConstructionSourceProjectionID,
	}
}

func TestConstructionBaseCacheSkipsCompileForIdenticalRequest(t *testing.T) {
	setup := constructionProposalSetup(t)
	store, snapshot := setup.Store, setup.Snapshot
	calls := 0
	configureSnapshotConstructionCompilation(setup.Config, store, snapshot, authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}, &calls)
	request := constructionCapabilitiesRequestFor(store.created, snapshot.Token, "patients")
	forCompilation := setup.Config.Capability.ForCompilation
	authorizationChecks := 0
	setup.Config.Capability.ForCompilation = func(ctx context.Context, project, token string) (ports.AuthorizedCapability, error) {
		authorizationChecks++
		return forCompilation(ctx, project, token)
	}
	service := newTestServiceFromSetup(t, setup)

	if _, err := service.OwnerServices().Construction.GetConstructionCapabilities(context.Background(), request); err != nil {
		t.Fatalf("first capabilities request: %v", err)
	}
	authorizationChecks = 0
	if _, err := service.OwnerServices().Construction.GetConstructionCapabilities(context.Background(), request); err != nil {
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
	setup := constructionProposalSetup(t)
	store, snapshot := setup.Store, setup.Snapshot
	calls := 0
	configureSnapshotConstructionCompilation(setup.Config, store, snapshot, authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}, &calls)
	service := newTestServiceFromSetup(t, setup)

	if _, err := service.OwnerServices().Construction.GetConstructionCapabilities(context.Background(), constructionCapabilitiesRequestFor(store.created, snapshot.Token, "patients")); err != nil {
		t.Fatalf("base capabilities request: %v", err)
	}
	store.created.DraftVersion++
	if _, err := service.OwnerServices().Construction.GetConstructionCapabilities(context.Background(), constructionCapabilitiesRequestFor(store.created, snapshot.Token, "patients")); err != nil {
		t.Fatalf("new draft version request: %v", err)
	}
	if calls != 2 {
		t.Fatalf("compile calls after draft version change = %d, want 2", calls)
	}

	workspace, err := ws.DecodeWorkspace(store.created.DraftConfig)
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
	if _, err := service.OwnerServices().Construction.GetConstructionCapabilities(context.Background(), constructionCapabilitiesRequestFor(store.created, snapshot.Token, "patients")); err != nil {
		t.Fatalf("new draft digest request: %v", err)
	}
	if calls != 3 {
		t.Fatalf("compile calls after draft digest change = %d, want 3", calls)
	}
}

func TestConstructionBaseCacheSeparatesSnapshotTokenAndScopeDigest(t *testing.T) {
	setup := constructionProposalSetup(t)
	store, snapshot := setup.Store, setup.Snapshot
	calls := 0
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	authorization := configureSnapshotConstructionCompilation(setup.Config, store, snapshot, scope, &calls)
	service := newTestServiceFromSetup(t, setup)

	if _, err := service.OwnerServices().Construction.GetConstructionCapabilities(context.Background(), constructionCapabilitiesRequestFor(store.created, snapshot.Token, "patients")); err != nil {
		t.Fatalf("base capabilities request: %v", err)
	}

	otherToken := readySnapshot("project-a", "generation-a", "snapshot-other", scope)
	authorization.snapshot = otherToken
	if _, err := service.OwnerServices().Construction.GetConstructionCapabilities(context.Background(), constructionCapabilitiesRequestFor(store.created, otherToken.Token, "patients")); err != nil {
		t.Fatalf("other token request: %v", err)
	}
	if calls != 2 {
		t.Fatalf("compile calls after snapshot token change = %d, want 2", calls)
	}

	setup = constructionProposalSetup(t)
	store, snapshot = setup.Store, setup.Snapshot
	calls = 0
	authorization = configureSnapshotConstructionCompilation(setup.Config, store, snapshot, scope, &calls)
	service = newTestServiceFromSetup(t, setup)
	if _, err := service.OwnerServices().Construction.GetConstructionCapabilities(context.Background(), constructionCapabilitiesRequestFor(store.created, snapshot.Token, "patients")); err != nil {
		t.Fatalf("base scope capabilities request: %v", err)
	}
	otherScope := authscope.ReadScope{Mode: authscope.ReadScopeRestricted, AuthResourcePaths: []string{"/x"}}
	scopedSnapshot := readySnapshot("project-a", "generation-a", snapshot.Token, otherScope)
	authorization.snapshot, authorization.scope = scopedSnapshot, otherScope
	if _, err := service.OwnerServices().Construction.GetConstructionCapabilities(context.Background(), constructionCapabilitiesRequestFor(store.created, scopedSnapshot.Token, "patients")); err != nil {
		t.Fatalf("other scope request: %v", err)
	}
	if calls != 2 {
		t.Fatalf("compile calls after scope digest change = %d, want 2", calls)
	}
}

func TestConstructionBaseCacheHitStillRejectsStaleDraft(t *testing.T) {
	setup := constructionProposalSetup(t)
	store, snapshot := setup.Store, setup.Snapshot
	calls := 0
	configureSnapshotConstructionCompilation(setup.Config, store, snapshot, authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}, &calls)
	service := newTestServiceFromSetup(t, setup)
	request := constructionCapabilitiesRequestFor(store.created, snapshot.Token, "patients")
	if _, err := service.OwnerServices().Construction.GetConstructionCapabilities(context.Background(), request); err != nil {
		t.Fatalf("first capabilities request: %v", err)
	}
	store.created.DraftDigest = "stale-digest"
	_, err := service.OwnerServices().Construction.GetConstructionCapabilities(context.Background(), request)
	if lifecycleErrorCode(err) != "DRAFT_CONFLICT" {
		t.Fatalf("stale cache-hit error = %v, want DRAFT_CONFLICT", err)
	}
	if calls != 1 {
		t.Fatalf("compile calls = %d, want 1", calls)
	}
}

func TestConstructionBaseCacheDoesNotCacheErrors(t *testing.T) {
	setup := constructionProposalSetup(t)
	store, snapshot := setup.Store, setup.Snapshot
	calls := 0
	configureSnapshotConstructionCompilation(setup.Config, store, snapshot, authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}, &calls)
	compile := setup.Config.CompileReceipt
	failCompile := true
	setup.Config.CompileReceipt = func(ctx context.Context, request ports.CompileReceiptRequest) (*receiptmodel.CompilationReceipt, error) {
		if failCompile {
			return nil, errors.New("temporary compile failure")
		}
		return compile(ctx, request)
	}
	service := newTestServiceFromSetup(t, setup)
	request := constructionCapabilitiesRequestFor(store.created, snapshot.Token, "patients")
	if _, err := service.OwnerServices().Construction.GetConstructionCapabilities(context.Background(), request); err == nil {
		t.Fatal("expected compile failure")
	}
	failCompile = false
	if _, err := service.OwnerServices().Construction.GetConstructionCapabilities(context.Background(), request); err != nil {
		t.Fatalf("retry capabilities request: %v", err)
	}
	if calls != 1 {
		t.Fatalf("successful compile calls = %d, want 1", calls)
	}
}

func TestConstructionBaseCacheReturnsIndependentCopies(t *testing.T) {
	setup := constructionProposalSetup(t)
	store, snapshot := setup.Store, setup.Snapshot
	calls := 0
	configureSnapshotConstructionCompilation(setup.Config, store, snapshot, authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}, &calls)
	service := newTestServiceFromSetup(t, setup)
	request := constructionCapabilitiesRequestFor(store.created, snapshot.Token, "patients")
	first, err := service.OwnerServices().Construction.GetConstructionCapabilities(context.Background(), request)
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
	second, err := service.OwnerServices().Construction.GetConstructionCapabilities(context.Background(), request)
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
	setup := constructionProposalSetup(t)
	store, snapshot := setup.Store, setup.Snapshot
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	setup.Config.ConstructionBaseCacheTTL = time.Minute
	setup.Config.Now = func() time.Time { return now }
	calls := 0
	configureSnapshotConstructionCompilation(setup.Config, store, snapshot, authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}, &calls)
	service := newTestServiceFromSetup(t, setup)
	request := constructionCapabilitiesRequestFor(store.created, snapshot.Token, "patients")
	if _, err := service.OwnerServices().Construction.GetConstructionCapabilities(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Minute)
	if _, err := service.OwnerServices().Construction.GetConstructionCapabilities(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("compile calls after expiry = %d, want 2", calls)
	}
}
