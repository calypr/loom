package server

import (
	"context"
	"encoding/json"
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

type constructionInputsRevisionFixture struct {
	values map[string]*explorer.Revision
	calls  []string
}

func (r *constructionInputsRevisionFixture) GetRevision(_ context.Context, id string) (*explorer.Revision, error) {
	r.calls = append(r.calls, id)
	revision, ok := r.values[id]
	if !ok {
		return nil, explorer.ErrNotFound
	}
	return revision, nil
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

func TestConstructionInputsCatalogUsesExactImmutablePublicationLabelsOncePerRevision(t *testing.T) {
	service, reader, bundleCatalog, snapshot, digest := newConstructionInputsFixture(t, authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted})
	selector := dataset.DataframeSelector{Recipe: "lab_summary", TranslationVersion: "v1", Output: "observations"}
	execution := constructionInputsExecution("source-revision", selector, "project-a", "generation-a", time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC))
	execution.ReceiptID = "receipt_source-revision"
	secondSelector := selector
	secondSelector.Output = "diagnoses"
	secondOutput := execution.Outputs[0]
	secondOutput.Name = secondSelector.Output
	secondOutput.Selector = secondSelector
	secondOutput.Columns = []publication.PhysicalColumn{{ID: "physical-diagnosis-id", Name: "col_opaque_diagnosis", ClickHouse: "String", LogicalType: "string"}}
	execution.Outputs[0].Columns = []publication.PhysicalColumn{{ID: "physical-observation-id", Name: "col_opaque_observation", ClickHouse: "String", LogicalType: "string"}}
	execution.Outputs = append(execution.Outputs, secondOutput)
	bundleCatalog.executions = []publication.BundleExecution{execution}
	reader.values[execution.ID+"/"+selector.Output] = constructionInputsMaterialization(execution, selector, authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted})
	reader.values[execution.ID+"/"+secondSelector.Output] = constructionInputsMaterialization(execution, secondSelector, authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted})
	for key, name := range map[string]string{
		execution.ID + "/" + selector.Output:       "col_opaque_observation",
		execution.ID + "/" + secondSelector.Output: "col_opaque_diagnosis",
	} {
		materialization := reader.values[key]
		materialization.ReceiptID = execution.ReceiptID
		materialization.Columns = []published.Column{{ID: "stable-" + name, Name: name, ClickHouse: "String", LogicalType: "string"}}
		reader.values[key] = materialization
	}

	workspace, err := authoringv2.DecodeWorkspace(baselineExplorerWorkspaceV2())
	if err != nil {
		t.Fatal(err)
	}
	workspace.Explorer.Title = "Published clinical tables"
	document := workspace.Documents[0]
	document.Output = authoringv2.Output{ID: selector.Output, Title: "Observation measurements"}
	document.RootResourceType = "Observation"
	document.Route.ResourceType = "Observation"
	workspace.Documents = []authoringv2.Document{document, document}
	workspace.Documents[1].Output = authoringv2.Output{ID: secondSelector.Output, Title: "Diagnosis records"}
	workspace.Documents[1].Output.Title = ""
	workspace.Tabs = []authoringv2.Tab{
		{ID: "observations", Title: "Observation measurements", OutputID: selector.Output, Order: 0, Visible: true},
		{ID: "diagnoses", Title: "Diagnosis records", OutputID: secondSelector.Output, Order: 1, Visible: true},
	}
	workspaceJSON, err := json.Marshal(workspace)
	if err != nil {
		t.Fatal(err)
	}
	emitted := []explorer.EmittedColumn{
		{OutputID: selector.Output, PublicColumn: "col_opaque_observation", Label: "Observed value", LogicalType: "string"},
		{OutputID: secondSelector.Output, PublicColumn: "col_opaque_diagnosis", Label: "Diagnosis code", LogicalType: "string"},
	}
	contracts := explorer.PublicOutputContracts{Outputs: []explorer.PublicOutputContract{
		{OutputID: selector.Output, RootResourceType: "Observation", RowGrain: "resource", Columns: []explorer.PublicOutputColumn{{Column: "col_opaque_observation", Label: "Observed value", LogicalType: "string"}}},
		{OutputID: secondSelector.Output, RootResourceType: "Observation", RowGrain: "resource", Columns: []explorer.PublicOutputColumn{{Column: "col_opaque_diagnosis", Label: "Diagnosis code", LogicalType: "string"}}},
	}}
	contractJSON, err := json.Marshal(contracts)
	if err != nil {
		t.Fatal(err)
	}
	service.revisions.(*constructionInputsRevisionFixture).values["authoring_source-revision"] = &explorer.Revision{
		ID: "authoring_source-revision", Project: "project-a", ExplorerID: "published-source",
		CompilationReceiptID: execution.ReceiptID, PublicOutputContract: contractJSON,
		AuthoringBundle: workspaceJSON,
		Recipe: recipe.Bundle{Outputs: []recipe.Output{
			{Name: selector.Output, RootResourceType: "Observation", RowGrain: "resource"},
			{Name: secondSelector.Output, RootResourceType: "Observation", RowGrain: "resource"},
		}},
		EmittedColumns: emitted, SourceGeneration: "generation-a",
		Publication: explorer.PublicationMetadata{State: string(explorer.RevisionReady), Generation: "generation-a", ExecutionID: execution.ID},
		Status:      explorer.RevisionReady,
	}
	if _, err := service.constructionInputPublicationMetadata(context.Background(), execution, "project-a"); err != nil {
		t.Fatal(err)
	}
	service.revisions.(*constructionInputsRevisionFixture).calls = nil

	request := constructionInputsRequest{SnapshotToken: snapshot.Token, ExpectedDraftVersion: 1, ExpectedDraftDigest: digest, Limit: 2}
	response, err := service.list(context.Background(), "project-a", "builder-a", request)
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Entries) != 2 {
		t.Fatalf("pinned source entries = %#v", response.Entries)
	}
	entriesByOutput := make(map[string]constructionInputEntry, len(response.Entries))
	for _, entry := range response.Entries {
		entriesByOutput[entry.OutputID] = entry
	}
	if got := entriesByOutput[selector.Output]; got.TableTitle != "Published clinical tables" || got.OutputTitle != "Observation measurements" || got.RevisionID != execution.ID || got.Columns[0].Name != "col_opaque_observation" || got.Columns[0].Label != "Observed value" {
		t.Fatalf("immutable observation output presentation = %#v", got)
	}
	if got := entriesByOutput[secondSelector.Output]; got.TableTitle != "Published clinical tables" || got.OutputTitle != secondSelector.Output || got.RevisionID != execution.ID || got.Columns[0].Name != "col_opaque_diagnosis" || got.Columns[0].Label != "Diagnosis code" {
		t.Fatalf("immutable diagnosis output presentation = %#v", got)
	}
	if calls := service.revisions.(*constructionInputsRevisionFixture).calls; !reflect.DeepEqual(calls, []string{"authoring_source-revision"}) {
		t.Fatalf("immutable source revision reads = %#v, want one read per execution receipt", calls)
	}

	validRevision := *service.revisions.(*constructionInputsRevisionFixture).values["authoring_source-revision"]
	for _, test := range []struct {
		name             string
		status           explorer.RevisionStatus
		publicationState string
		wantEntries      bool
	}{
		{name: "ready before activation", status: explorer.RevisionReady, publicationState: string(explorer.RevisionReady), wantEntries: true},
		{name: "active current", status: explorer.RevisionActive, publicationState: string(explorer.RevisionActive), wantEntries: true},
		{name: "superseded historical", status: explorer.RevisionSuperseded, publicationState: string(explorer.RevisionActive), wantEntries: true},
		{name: "failed", status: explorer.RevisionFailed, publicationState: string(explorer.RevisionFailed)},
		{name: "unknown status", status: explorer.RevisionStatus("UNKNOWN"), publicationState: string(explorer.RevisionActive)},
	} {
		t.Run("catalog listing accepts only successful immutable revision states/"+test.name, func(t *testing.T) {
			revision := validRevision
			revision.Status = test.status
			revision.Publication.State = test.publicationState
			service.revisions.(*constructionInputsRevisionFixture).values["authoring_source-revision"] = &revision

			listed, err := service.list(context.Background(), "project-a", "builder-a", request)
			if test.wantEntries {
				if err != nil || len(listed.Entries) != 2 {
					t.Fatalf("published revision status %q entries = %#v, err=%v", test.status, listed.Entries, err)
				}
				for _, entry := range listed.Entries {
					if entry.Columns[0].Label == "" || entry.RevisionID != execution.ID {
						t.Fatalf("published revision status %q lost pinned source metadata: %#v", test.status, entry)
					}
				}
				return
			}
			var authoringErr *explorer.AuthoringError
			if err == nil || !errors.As(err, &authoringErr) || authoringErr.Status != 503 || authoringErr.Diagnostic.Code != "AUTHORING_UNAVAILABLE" {
				t.Fatalf("non-successful revision status %q error = %#v, want unavailable", test.status, err)
			}
		})
	}
	for _, test := range []struct {
		name   string
		mutate func(*explorer.Revision)
	}{
		{name: "project", mutate: func(revision *explorer.Revision) { revision.Project = "project-b" }},
		{name: "generation", mutate: func(revision *explorer.Revision) { revision.SourceGeneration = "generation-b" }},
		{name: "receipt", mutate: func(revision *explorer.Revision) { revision.CompilationReceiptID = "receipt-other" }},
		{name: "execution", mutate: func(revision *explorer.Revision) { revision.Publication.ExecutionID = "execution-other" }},
	} {
		t.Run("reject mismatched immutable presentation "+test.name, func(t *testing.T) {
			mismatched := validRevision
			test.mutate(&mismatched)
			service.revisions.(*constructionInputsRevisionFixture).values["authoring_source-revision"] = &mismatched
			metadata, err := service.constructionInputPublicationMetadata(context.Background(), execution, "project-a")
			if err == nil || metadata != nil {
				t.Fatalf("mismatched immutable presentation exposed labels: metadata=%#v err=%v", metadata, err)
			}
		})
	}
}

func TestConstructionInputColumnsFallsBackToTheExactPublishedNameWithoutALabel(t *testing.T) {
	columns := constructionInputColumns([]published.Column{{ID: "stable-id", Name: "col_opaque", ClickHouse: "String", LogicalType: "string"}}, map[string]string{"col_opaque": " "})
	if len(columns) != 1 || columns[0].Name != "col_opaque" || columns[0].Label != "col_opaque" {
		t.Fatalf("blank display label should retain the exact published name: %#v", columns)
	}
}

func TestConstructionInputsCatalogMissingImmutableRevisionKeepsOnlyPublishedNames(t *testing.T) {
	service, reader, bundleCatalog, snapshot, digest := newConstructionInputsFixture(t, authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted})
	selector := dataset.DataframeSelector{Recipe: "opaque_recipe", TranslationVersion: "v1", Output: "opaque_output"}
	execution := constructionInputsExecution("legacy-published-execution", selector, "project-a", "generation-a", time.Now())
	execution.ReceiptID = "receipt_legacy-published-execution"
	execution.Outputs[0].Columns = []publication.PhysicalColumn{{ID: "stable-opaque-id", Name: "col_opaque", ClickHouse: "String", LogicalType: "string"}}
	bundleCatalog.executions = []publication.BundleExecution{execution}
	materialization := constructionInputsMaterialization(execution, selector, authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted})
	materialization.ReceiptID = execution.ReceiptID
	materialization.Columns = []published.Column{{ID: "stable-opaque-id", Name: "col_opaque", ClickHouse: "String", LogicalType: "string"}}
	reader.values[execution.ID+"/"+selector.Output] = materialization

	response, err := service.list(context.Background(), "project-a", "builder-a", constructionInputsRequest{
		SnapshotToken: snapshot.Token, ExpectedDraftVersion: 1, ExpectedDraftDigest: digest,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Entries) != 1 {
		t.Fatalf("legacy entry count = %d", len(response.Entries))
	}
	got := response.Entries[0]
	if got.TableTitle != "opaque_recipe" || got.OutputTitle != "opaque_output" || got.Columns[0].Name != "col_opaque" || got.Columns[0].Label != "col_opaque" {
		t.Fatalf("missing immutable metadata invented a label: %#v", got)
	}
	if calls := service.revisions.(*constructionInputsRevisionFixture).calls; !reflect.DeepEqual(calls, []string{"authoring_legacy-published-execution"}) {
		t.Fatalf("missing source revision lookup = %#v", calls)
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
	revisionReader := &constructionInputsRevisionFixture{values: map[string]*explorer.Revision{}}
	service := constructionInputsCatalog{
		reader: reader, catalog: catalogReader, scopes: nil,
		revisions: revisionReader,
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

func TestConstructionInputsCatalogDoesNotReadUnauthorizedRevisionMetadata(t *testing.T) {
	scope := authscope.ReadScope{Mode: authscope.ReadScopeRestricted, AuthResourcePaths: []string{"allowed-a"}}
	service, reader, bundleCatalog, snapshot, digest := newConstructionInputsFixture(t, scope)
	selector := dataset.DataframeSelector{Recipe: "labs", TranslationVersion: "v1", Output: "observations"}
	createdAt := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	active := constructionInputsExecution("revision-active", selector, "project-a", "generation-a", createdAt)
	superseded := constructionInputsExecution("revision-superseded", selector, "project-a", "generation-a", createdAt.Add(time.Minute))
	denied := constructionInputsExecution("revision-denied", selector, "project-a", "generation-a", createdAt.Add(2*time.Minute))
	for _, execution := range []*publication.BundleExecution{&active, &superseded} {
		setConstructionInputsExecutionScope(execution, scope)
		execution.ReceiptID = "receipt_" + execution.ID
	}
	setConstructionInputsExecutionScope(&denied, authscope.ReadScope{Mode: authscope.ReadScopeRestricted, AuthResourcePaths: []string{"allowed-b"}})
	denied.ReceiptID = "receipt_" + denied.ID
	bundleCatalog.executions = []publication.BundleExecution{active, denied, superseded}
	bundleCatalog.pointers[active.PointerName()] = publication.BundlePointer{Name: active.PointerName(), ExecutionID: active.ID}
	for _, execution := range []publication.BundleExecution{active, superseded} {
		materialization := constructionInputsMaterialization(execution, selector, scope)
		materialization.ReceiptID = execution.ReceiptID
		reader.values[execution.ID+"/"+selector.Output] = materialization
	}
	deniedScope := authscope.ReadScope{Mode: authscope.ReadScopeRestricted, AuthResourcePaths: []string{"allowed-b"}}
	deniedMaterialization := constructionInputsMaterialization(denied, selector, deniedScope)
	deniedMaterialization.ReceiptID = denied.ReceiptID
	reader.values[denied.ID+"/"+selector.Output] = deniedMaterialization

	revisions := service.revisions.(*constructionInputsRevisionFixture)
	revisions.values["authoring_"+active.ID] = constructionInputsImmutablePresentationRevision(t, active, "Allowed active table", "Active observations", "Allowed active value", explorer.RevisionActive)
	revisions.values["authoring_"+superseded.ID] = constructionInputsImmutablePresentationRevision(t, superseded, "Allowed historical table", "Historical observations", "Allowed historical value", explorer.RevisionSuperseded)
	revisions.values["authoring_"+denied.ID] = constructionInputsImmutablePresentationRevision(t, denied, "DENIED_SECRET_TABLE", "DENIED_SECRET_OUTPUT", "DENIED_SECRET_COLUMN", explorer.RevisionActive)

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
	response, err := service.list(ctx, "project-a", "builder-a", constructionInputsRequest{
		SnapshotToken: snapshot.Token, ExpectedDraftVersion: 1, ExpectedDraftDigest: digest,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Entries) != 2 {
		t.Fatalf("authorized immutable revisions = %#v, want active and superseded entries only", response.Entries)
	}
	entries := make(map[string]constructionInputEntry, len(response.Entries))
	for _, entry := range response.Entries {
		entries[entry.RevisionID] = entry
	}
	for revisionID, want := range map[string]struct{ tableTitle, outputTitle, columnLabel string }{
		active.ID:     {tableTitle: "Allowed active table", outputTitle: "Active observations", columnLabel: "Allowed active value"},
		superseded.ID: {tableTitle: "Allowed historical table", outputTitle: "Historical observations", columnLabel: "Allowed historical value"},
	} {
		entry, ok := entries[revisionID]
		if !ok || entry.TableTitle != want.tableTitle || entry.OutputTitle != want.outputTitle || len(entry.Columns) != 1 || entry.Columns[0].Label != want.columnLabel {
			t.Errorf("authorized revision %q presentation = %#v, want %#v", revisionID, entry, want)
		}
	}
	if _, ok := entries[denied.ID]; ok {
		t.Fatalf("unauthorized revision was returned: %#v", entries[denied.ID])
	}
	if !reflect.DeepEqual(revisions.calls, []string{"authoring_" + active.ID, "authoring_" + superseded.ID}) {
		t.Fatalf("immutable revision lookups = %#v, want only authorized active and superseded revisions", revisions.calls)
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"DENIED_SECRET_TABLE", "DENIED_SECRET_OUTPUT", "DENIED_SECRET_COLUMN"} {
		if strings.Contains(string(encoded), secret) {
			t.Errorf("unauthorized immutable metadata %q leaked in catalog response %s", secret, encoded)
		}
	}
}

func constructionInputsImmutablePresentationRevision(t *testing.T, execution publication.BundleExecution, tableTitle, outputTitle, columnLabel string, status explorer.RevisionStatus) *explorer.Revision {
	t.Helper()
	workspace, err := authoringv2.DecodeWorkspace(baselineExplorerWorkspaceV2())
	if err != nil {
		t.Fatal(err)
	}
	workspace.Explorer.Title = tableTitle
	document := workspace.Documents[0]
	document.Output = authoringv2.Output{ID: execution.Outputs[0].Name, Title: outputTitle}
	document.RootResourceType = "Observation"
	document.Route.ResourceType = "Observation"
	workspace.Documents = []authoringv2.Document{document}
	workspaceJSON, err := json.Marshal(workspace)
	if err != nil {
		t.Fatal(err)
	}
	contracts, err := json.Marshal(explorer.PublicOutputContracts{Outputs: []explorer.PublicOutputContract{{
		OutputID: execution.Outputs[0].Name, RootResourceType: "Observation", RowGrain: "resource",
		Columns: []explorer.PublicOutputColumn{{Column: "value", Label: columnLabel, LogicalType: "decimal"}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	return &explorer.Revision{
		ID: "authoring_" + strings.TrimPrefix(execution.ReceiptID, "receipt_"), Project: execution.Project,
		CompilationReceiptID: execution.ReceiptID, PublicOutputContract: contracts, AuthoringBundle: workspaceJSON,
		Recipe:           recipe.Bundle{Outputs: []recipe.Output{{Name: execution.Outputs[0].Name, RootResourceType: "Observation", RowGrain: "resource"}}},
		EmittedColumns:   []explorer.EmittedColumn{{OutputID: execution.Outputs[0].Name, PublicColumn: "value", Label: columnLabel, LogicalType: "decimal"}},
		SourceGeneration: execution.DatasetGeneration,
		Publication:      explorer.PublicationMetadata{State: string(explorer.RevisionActive), Generation: execution.DatasetGeneration, ExecutionID: execution.ID},
		Status:           status,
	}
}
