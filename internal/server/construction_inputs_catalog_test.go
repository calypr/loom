package server

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
	"github.com/calypr/loom/internal/dataframe/publication"
	"github.com/calypr/loom/internal/dataframe/published"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle"
)

type constructionInputsReaderFixture struct {
	values map[string]published.Materialization
	calls  []string
	pins   []string
}

func (r *constructionInputsReaderFixture) ExactExecutionMaterialization(_ context.Context, revision, output string) (published.Materialization, error) {
	key := revision + "/" + output
	r.calls = append(r.calls, key)
	value, ok := r.values[key]
	if !ok {
		return published.Materialization{}, fmt.Errorf("missing exact fixture %s", key)
	}
	return value, nil
}

func (r *constructionInputsReaderFixture) WithExecutionReadPins(ctx context.Context, ids []string, visit func(context.Context) error) error {
	r.pins = append([]string(nil), ids...)
	return visit(ctx)
}

type constructionInputsCatalogFixture struct {
	executions []publication.BundleExecution
	pointers   map[string]publication.BundlePointer
	listCalls  int
}

func (c *constructionInputsCatalogFixture) ListExecutions(context.Context, publication.BundleState, time.Time) ([]publication.BundleExecution, error) {
	c.listCalls++
	return append([]publication.BundleExecution(nil), c.executions...), nil
}

func (c *constructionInputsCatalogFixture) GetPointer(_ context.Context, name string) (publication.BundlePointer, error) {
	pointer, ok := c.pointers[name]
	if !ok {
		return publication.BundlePointer{}, publication.ErrBundleNotFound
	}
	return pointer, nil
}

type constructionInputsExplorerFixture struct{ value *explorer.Explorer }

func (f constructionInputsExplorerFixture) Get(context.Context, string, string) (*explorer.Explorer, error) {
	return f.value, nil
}

func TestConstructionInputsCatalogListsExactPinnedAuthorizedRevisions(t *testing.T) {
	service, reader, bundleCatalog, snapshot, digest := newConstructionInputsFixture(t, authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted})
	selector := dataset.DataframeSelector{Recipe: "lab_summary", TranslationVersion: "v1", Output: "observations"}
	old := constructionInputsExecution("revision-old", selector, "project-a", "generation-a", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	current := constructionInputsExecution("revision-current", selector, "project-a", "generation-a", time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC))
	otherProject := constructionInputsExecution("revision-other-project", selector, "project-b", "generation-a", time.Now())
	otherGeneration := constructionInputsExecution("revision-other-generation", selector, "project-a", "generation-old", time.Now())
	failed := constructionInputsExecution("revision-failed", selector, "project-a", "generation-a", time.Now())
	failed.State = publication.BundleFailed
	for index := range failed.Outputs {
		failed.Outputs[index].State = publication.BundleFailed
	}
	bundleCatalog.executions = []publication.BundleExecution{old, current, otherProject, otherGeneration, failed}
	bundleCatalog.pointers[current.PointerName()] = publication.BundlePointer{Name: current.PointerName(), ExecutionID: current.ID}
	reader.values[old.ID+"/"+selector.Output] = constructionInputsMaterialization(old, selector, authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted})
	reader.values[current.ID+"/"+selector.Output] = constructionInputsMaterialization(current, selector, authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted})

	request := constructionInputsRequest{
		SnapshotToken: snapshot.Token, ExpectedDraftVersion: 1, ExpectedDraftDigest: digest, Limit: 1,
	}
	response, err := service.list(context.Background(), "project-a", "builder-a", request)
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Entries) != 1 || response.NextCursor == "" {
		t.Fatalf("first exact revision page = %#v", response)
	}
	if response.Entries[0].RevisionID != current.ID || !response.Entries[0].IsCurrent {
		t.Fatalf("current exact revision marker = %#v", response.Entries)
	}
	if response.Entries[0].Kind != "TABLE_REVISION" || response.Entries[0].TableID != selector.Key() || response.Entries[0].OutputID != selector.Output {
		t.Fatalf("exact published identity = %#v", response.Entries[0])
	}
	if got := response.Entries[0].Columns[0]; got.ID != "stable-lab-value" || got.Name != "value" || got.Label != "value" || got.Type != "decimal" || got.ClickHouseType != "Nullable(Float64)" || !got.Nullable || got.Repeated {
		t.Fatalf("stable published column metadata = %#v", got)
	}
	if response.DatasetGeneration != "generation-a" || response.DraftVersion != 1 || response.DraftDigest != digest {
		t.Fatalf("snapshot/draft bindings = %#v", response)
	}
	if !reflect.DeepEqual(reader.pins, []string{current.ID}) {
		t.Fatalf("exact revision pins = %#v", reader.pins)
	}
	if !reflect.DeepEqual(reader.calls, []string{current.ID + "/" + selector.Output}) {
		t.Fatalf("exact revision lookups = %#v", reader.calls)
	}
	request.Cursor = response.NextCursor
	response, err = service.list(context.Background(), "project-a", "builder-a", request)
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Entries) != 1 || response.Entries[0].RevisionID != old.ID || response.Entries[0].IsCurrent || response.NextCursor != "" {
		t.Fatalf("historical exact revision page = %#v", response)
	}
	if !reflect.DeepEqual(reader.pins, []string{old.ID}) {
		t.Fatalf("historical exact revision pins = %#v", reader.pins)
	}
	if !reflect.DeepEqual(reader.calls, []string{current.ID + "/" + selector.Output, old.ID + "/" + selector.Output}) {
		t.Fatalf("exact revision lookups = %#v", reader.calls)
	}
}

func TestConstructionInputsCatalogFiltersUnauthorizedScopeAndSupportsStablePaging(t *testing.T) {
	scope := authscope.ReadScope{Mode: authscope.ReadScopeRestricted, AuthResourcePaths: []string{"allowed-a"}}
	service, reader, bundleCatalog, snapshot, digest := newConstructionInputsFixture(t, scope)
	selectorA := dataset.DataframeSelector{Recipe: "labs", TranslationVersion: "v1", Output: "observations"}
	selectorB := dataset.DataframeSelector{Recipe: "visits", TranslationVersion: "v1", Output: "encounters"}
	selectorNext := dataset.DataframeSelector{Recipe: "tests", TranslationVersion: "v1", Output: "tests"}
	allowed := constructionInputsExecution("revision-allowed", selectorA, "project-a", "generation-a", time.Now())
	allowedNext := constructionInputsExecution("revision-allowed-next", selectorNext, "project-a", "generation-a", time.Now())
	denied := constructionInputsExecution("revision-denied", selectorB, "project-a", "generation-a", time.Now())
	setConstructionInputsExecutionScope(&allowed, scope)
	setConstructionInputsExecutionScope(&allowedNext, scope)
	setConstructionInputsExecutionScope(&denied, authscope.ReadScope{Mode: authscope.ReadScopeRestricted, AuthResourcePaths: []string{"allowed-b"}})
	bundleCatalog.executions = []publication.BundleExecution{allowed, allowedNext, denied}
	reader.values[allowed.ID+"/"+selectorA.Output] = constructionInputsMaterialization(allowed, selectorA, scope)
	reader.values[allowedNext.ID+"/"+selectorNext.Output] = constructionInputsMaterialization(allowedNext, selectorNext, scope)
	reader.values[denied.ID+"/"+selectorB.Output] = constructionInputsMaterialization(denied, selectorB, authscope.ReadScope{Mode: authscope.ReadScopeRestricted, AuthResourcePaths: []string{"allowed-b"}})
	service.scopes = authscope.NewScopeResolver(authscope.ScopeResolverConfig{
		ResourceAccess: constructionInputsResourceAccess{"allowed-a"},
		ListExistingAuthResourcePaths: func(_ context.Context, options catalog.AuthResourcePathOptions) ([]string, error) {
			if options.Project != "project-a" || options.DatasetGeneration != "generation-a" {
				t.Fatalf("scope lookup used project/generation %q/%q", options.Project, options.DatasetGeneration)
			}
			return []string{"allowed-a", "allowed-b"}, nil
		},
	})
	ctx := authscope.ContextWithPrincipal(context.Background(), &authscope.Principal{AuthResourcePaths: []string{"allowed-a"}})
	allowedMaterialization := reader.values[allowed.ID+"/"+selectorA.Output]
	persistedScope, err := persistedMaterializationScope(allowedMaterialization)
	if err != nil {
		t.Fatal(err)
	}
	effectiveScope, err := service.effectiveConstructionInputScope(ctx, persistedScope, allowedMaterialization)
	if err != nil || !sameClickHouseReadScope(effectiveScope, scope) {
		t.Fatalf("allowed materialization scope = %#v, err=%v", effectiveScope, err)
	}
	request := constructionInputsRequest{SnapshotToken: snapshot.Token, ExpectedDraftVersion: 1, ExpectedDraftDigest: digest, Limit: 1}
	first, err := service.list(ctx, "project-a", "builder-a", request)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Entries) != 1 || first.Entries[0].RevisionID != allowed.ID || first.NextCursor == "" {
		t.Fatalf("first authorized page = %#v", first)
	}
	request.Cursor = first.NextCursor
	second, err := service.list(ctx, "project-a", "builder-a", request)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Entries) != 1 || second.Entries[0].RevisionID != allowedNext.ID || second.NextCursor != "" {
		t.Fatalf("second authorized page = %#v", second)
	}
	request.Cursor = first.NextCursor
	request.Query = "different query"
	if _, err := service.list(ctx, "project-a", "builder-a", request); err == nil {
		t.Fatal("cursor for a different query was accepted")
	}
	request.Query = ""
	request.ExpectedDraftVersion++
	if _, _, _, err := validateConstructionInputsRequest(request); err == nil {
		t.Fatal("cursor from a different draft version was accepted")
	}
}

func TestConstructionInputsCatalogRejectsStaleDraftBeforeListing(t *testing.T) {
	service, _, bundleCatalog, snapshot, digest := newConstructionInputsFixture(t, authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted})
	request := constructionInputsRequest{SnapshotToken: snapshot.Token, ExpectedDraftVersion: 2, ExpectedDraftDigest: digest}
	if _, err := service.list(context.Background(), "project-a", "builder-a", request); err == nil {
		t.Fatal("stale draft was accepted")
	}
	if bundleCatalog.listCalls != 0 {
		t.Fatalf("published catalog was read before draft validation: %d calls", bundleCatalog.listCalls)
	}
}

func TestConstructionInputsCatalogRejectsMalformedRequest(t *testing.T) {
	_, _, _, snapshot, digest := newConstructionInputsFixture(t, authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted})
	for _, request := range []constructionInputsRequest{
		{SnapshotToken: snapshot.Token, ExpectedDraftVersion: 0, ExpectedDraftDigest: digest},
		{SnapshotToken: snapshot.Token, ExpectedDraftVersion: 1, ExpectedDraftDigest: digest, Limit: constructionInputsMaxLimit + 1},
		{SnapshotToken: snapshot.Token, ExpectedDraftVersion: 1, ExpectedDraftDigest: digest, Query: strings.Repeat("x", constructionInputsMaxQuery+1)},
	} {
		if _, _, _, err := validateConstructionInputsRequest(request); err == nil {
			t.Fatalf("malformed request accepted: %#v", request)
		}
	}
}

func TestConstructionInputsDirectHandlerChecksProjectReadBeforeCatalog(t *testing.T) {
	handler := &explorerHTTPHandlers{
		authorizeRead: func(context.Context, *authscope.Principal, string) error { return errors.New("denied") },
	}
	_, err := handler.getConstructionInputsDirect(context.Background(), "project-a", "builder-a", &constructionInputsRequest{
		SnapshotToken: "snapshot", ExpectedDraftVersion: 1, ExpectedDraftDigest: "digest",
	})
	var authoringErr *explorer.AuthoringError
	if !errors.As(err, &authoringErr) || authoringErr.Status != 403 {
		t.Fatalf("project read denial = %v, want 403 authoring error", err)
	}
}

func newConstructionInputsFixture(t *testing.T, scope authscope.ReadScope) (constructionInputsCatalog, *constructionInputsReaderFixture, *constructionInputsCatalogFixture, capability.Snapshot, string) {
	t.Helper()
	snapshot := testAuthorizedCapabilitySnapshot(t, "generation-a", scope)
	workspace, err := authoringv2.DecodeWorkspace(baselineExplorerWorkspaceV2())
	if err != nil {
		t.Fatal(err)
	}
	draft, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	reader := &constructionInputsReaderFixture{values: map[string]published.Materialization{}}
	catalogReader := &constructionInputsCatalogFixture{pointers: map[string]publication.BundlePointer{}}
	service := constructionInputsCatalog{
		reader: reader, catalog: catalogReader, scopes: nil,
		explorers: constructionInputsExplorerFixture{value: &explorer.Explorer{Project: "project-a", ExplorerID: "builder-a", DraftVersion: 1, DraftDigest: digest, DraftConfig: draft}},
		capabilities: lifecycle.CapabilityResolver{ForCompilation: func(context.Context, string, string) (lifecycle.AuthorizedCapability, error) {
			return lifecycle.AuthorizedCapability{Snapshot: snapshot, Scope: scope}, nil
		}},
	}
	return service, reader, catalogReader, snapshot, digest
}

type constructionInputsResourceAccess []string

func (a constructionInputsResourceAccess) GetAllowedResources(context.Context, string, string, string) ([]string, error) {
	return append([]string(nil), a...), nil
}

func constructionInputsExecution(id string, selector dataset.DataframeSelector, project, generation string, createdAt time.Time) publication.BundleExecution {
	verifiedAt := createdAt
	execution := publication.BundleExecution{
		ID: id,
		BundleIdentity: publication.BundleIdentity{
			Name: selector.Recipe, TranslationVersion: selector.TranslationVersion, Project: project, DatasetGeneration: generation,
			RecipeDigest: "recipe-" + id, SchemaDigest: "schema-" + id, EngineVersion: "engine-v1",
		},
		State: publication.BundlePublished, CreatedAt: createdAt,
		Outputs: []publication.BundleOutputRecord{{
			Name: selector.Output, PhysicalTable: "physical_" + id,
			Selector: selector, State: publication.BundlePublished, VerifiedAt: &verifiedAt,
			Columns: []publication.PhysicalColumn{{ID: "stable-lab-value", Name: "value", ClickHouse: "Nullable(Float64)", LogicalType: "decimal", Nullable: true}},
		}},
	}
	setConstructionInputsExecutionScope(&execution, authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted})
	return execution
}

func setConstructionInputsExecutionScope(execution *publication.BundleExecution, scope authscope.ReadScope) {
	bindings := recipe.RuntimeBindings{
		Project: execution.Project, DatasetGeneration: execution.DatasetGeneration,
		AuthScopeMode: scope.Mode, AuthResourcePaths: append([]string(nil), scope.AuthResourcePaths...),
	}
	execution.ReceiptID = "receipt-" + execution.ID
	execution.AuthScopeMode = string(scope.Mode)
	execution.AuthResourcePaths = append([]string(nil), scope.AuthResourcePaths...)
	execution.ScopeDigest = recipeScopeDigest(bindings)
}

func constructionInputsMaterialization(execution publication.BundleExecution, selector dataset.DataframeSelector, scope authscope.ReadScope) published.Materialization {
	bindings := recipe.RuntimeBindings{
		Project: execution.Project, DatasetGeneration: execution.DatasetGeneration,
		AuthScopeMode: scope.Mode, AuthResourcePaths: append([]string(nil), scope.AuthResourcePaths...),
	}
	return published.Materialization{
		ID: execution.ID + ":" + selector.Output, Name: "Observation", Revision: execution.ID,
		ReceiptID: "receipt-" + execution.ID, SchemaDigest: "schema-" + execution.ID, ScopeDigest: recipeScopeDigest(bindings),
		AuthScopeMode: string(scope.Mode), Project: execution.Project, DatasetGeneration: execution.DatasetGeneration,
		State: published.StateReady, ScopeUnrestricted: scope.Mode == authscope.ReadScopeUnrestricted,
		AuthResourcePaths: append([]string(nil), scope.AuthResourcePaths...), PhysicalTable: "physical_" + execution.ID,
		CreatedAt: execution.CreatedAt, Selector: selector,
		SourceRow: &publication.SourceRowMetadata{ResourceType: "Observation", IDColumn: "observation_id"},
		Columns:   []published.Column{{ID: "stable-lab-value", Name: "value", ClickHouse: "Nullable(Float64)", LogicalType: "decimal", Nullable: true}},
	}
}
