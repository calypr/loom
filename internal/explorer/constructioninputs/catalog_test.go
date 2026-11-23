package constructioninputs

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/explorer/receiptmodel"
	"github.com/calypr/loom/internal/explorer/viewer"

	"github.com/calypr/loom/internal/testkit/constructionfixtures"

	reciperuntime "github.com/calypr/loom/internal/dataframe/recipe/runtime"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/publication"
	"github.com/calypr/loom/internal/dataframe/published"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/capability"
)

func TestConstructionInputsCatalogListsExactPinnedAuthorizedRevisions(t *testing.T) {
	service, reader, bundleCatalog, snapshot, digest := newConstructionInputsFixture(t, authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted})
	selector := dataset.DataframeSelector{Recipe: "lab_summary", TranslationVersion: "v1", Output: "observations"}
	old := constructionfixtures.Execution("revision-old", selector, "project-a", "generation-a", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	current := constructionfixtures.Execution("revision-current", selector, "project-a", "generation-a", time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC))
	otherProject := constructionfixtures.Execution("revision-other-project", selector, "project-b", "generation-a", time.Now())
	otherGeneration := constructionfixtures.Execution("revision-other-generation", selector, "project-a", "generation-old", time.Now())
	failed := constructionfixtures.Execution("revision-failed", selector, "project-a", "generation-a", time.Now())
	failed.State = publication.BundleFailed
	for index := range failed.Outputs {
		failed.Outputs[index].State = publication.BundleFailed
	}
	bundleCatalog.Executions = []publication.BundleExecution{old, current, otherProject, otherGeneration, failed}
	bundleCatalog.Pointers[current.PointerName()] = publication.BundlePointer{Name: current.PointerName(), ExecutionID: current.ID}
	reader.Values[old.ID+"/"+selector.Output] = constructionfixtures.Materialization(old, selector, authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted})
	reader.Values[current.ID+"/"+selector.Output] = constructionfixtures.Materialization(current, selector, authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted})

	request := Request{
		SnapshotToken: snapshot.Token, ExpectedDraftVersion: 1, ExpectedDraftDigest: digest, Limit: 1,
	}
	response, err := service.List(context.Background(), "project-a", "builder-a", request)
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
	if !reflect.DeepEqual(reader.Pins, []string{current.ID}) {
		t.Fatalf("exact revision pins = %#v", reader.Pins)
	}
	if !reflect.DeepEqual(reader.Calls, []string{current.ID + "/" + selector.Output}) {
		t.Fatalf("exact revision lookups = %#v", reader.Calls)
	}
	request.Cursor = response.NextCursor
	response, err = service.List(context.Background(), "project-a", "builder-a", request)
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Entries) != 1 || response.Entries[0].RevisionID != old.ID || response.Entries[0].IsCurrent || response.NextCursor != "" {
		t.Fatalf("historical exact revision page = %#v", response)
	}
	if !reflect.DeepEqual(reader.Pins, []string{old.ID}) {
		t.Fatalf("historical exact revision pins = %#v", reader.Pins)
	}
	if !reflect.DeepEqual(reader.Calls, []string{current.ID + "/" + selector.Output, old.ID + "/" + selector.Output}) {
		t.Fatalf("exact revision lookups = %#v", reader.Calls)
	}
}

func TestConstructionInputsCatalogUsesExactImmutablePublicationLabelsOncePerRevision(t *testing.T) {
	service, reader, bundleCatalog, snapshot, digest := newConstructionInputsFixture(t, authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted})
	selector := dataset.DataframeSelector{Recipe: "lab_summary", TranslationVersion: "v1", Output: "observations"}
	execution := constructionfixtures.Execution("source-revision", selector, "project-a", "generation-a", time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC))
	execution.ReceiptID = "receipt_source-revision"
	secondSelector := selector
	secondSelector.Output = "diagnoses"
	secondOutput := execution.Outputs[0]
	secondOutput.Name = secondSelector.Output
	secondOutput.Selector = secondSelector
	secondOutput.Columns = []publication.PhysicalColumn{{ID: "physical-diagnosis-id", Name: "col_opaque_diagnosis", ClickHouse: "String", LogicalType: "string"}}
	execution.Outputs[0].Columns = []publication.PhysicalColumn{{ID: "physical-observation-id", Name: "col_opaque_observation", ClickHouse: "String", LogicalType: "string"}}
	execution.Outputs = append(execution.Outputs, secondOutput)
	bundleCatalog.Executions = []publication.BundleExecution{execution}
	reader.Values[execution.ID+"/"+selector.Output] = constructionfixtures.Materialization(execution, selector, authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted})
	reader.Values[execution.ID+"/"+secondSelector.Output] = constructionfixtures.Materialization(execution, secondSelector, authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted})
	for key, name := range map[string]string{
		execution.ID + "/" + selector.Output:       "col_opaque_observation",
		execution.ID + "/" + secondSelector.Output: "col_opaque_diagnosis",
	} {
		materialization := reader.Values[key]
		materialization.ReceiptID = execution.ReceiptID
		materialization.Columns = []published.Column{{ID: "stable-" + name, Name: name, ClickHouse: "String", LogicalType: "string"}}
		reader.Values[key] = materialization
	}

	workspace, err := ws.DecodeWorkspace(constructionfixtures.BaselineExplorerWorkspace())
	if err != nil {
		t.Fatal(err)
	}
	workspace.Explorer.Title = "Published clinical tables"
	document := workspace.Documents[0]
	document.Output = ws.Output{ID: selector.Output, Title: "Observation measurements"}
	document.RootResourceType = "Observation"
	document.Route.ResourceType = "Observation"
	workspace.Documents = []ws.Document{document, document}
	workspace.Documents[1].Output = ws.Output{ID: secondSelector.Output, Title: "Diagnosis records"}
	workspace.Documents[1].Output.Title = ""
	workspace.Tabs = []ws.Tab{
		{ID: "observations", Title: "Observation measurements", OutputID: selector.Output, Order: 0, Visible: true},
		{ID: "diagnoses", Title: "Diagnosis records", OutputID: secondSelector.Output, Order: 1, Visible: true},
	}
	workspaceJSON, err := json.Marshal(workspace)
	if err != nil {
		t.Fatal(err)
	}
	emitted := []viewer.EmittedColumn{
		{OutputID: selector.Output, PublicColumn: "col_opaque_observation", Label: "Observed value", LogicalType: "string"},
		{OutputID: secondSelector.Output, PublicColumn: "col_opaque_diagnosis", Label: "Diagnosis code", LogicalType: "string"},
	}
	contracts := receiptmodel.PublicOutputContracts{Outputs: []receiptmodel.PublicOutputContract{
		{OutputID: selector.Output, RootResourceType: "Observation", RowGrain: "resource", Columns: []receiptmodel.PublicOutputColumn{{Column: "col_opaque_observation", Label: "Observed value", LogicalType: "string"}}},
		{OutputID: secondSelector.Output, RootResourceType: "Observation", RowGrain: "resource", Columns: []receiptmodel.PublicOutputColumn{{Column: "col_opaque_diagnosis", Label: "Diagnosis code", LogicalType: "string"}}},
	}}
	contractJSON, err := json.Marshal(contracts)
	if err != nil {
		t.Fatal(err)
	}
	service.revisions.(*constructionfixtures.Revisions).Values["authoring_source-revision"] = &explorer.Revision{
		ID: "authoring_source-revision", Project: "project-a", ExplorerID: "published-source",
		CompilationReceiptID: execution.ReceiptID, PublicOutputContract: contractJSON,
		AuthoringBundle: workspaceJSON,
		Recipe: recipe.Bundle{Outputs: []recipe.Output{
			{Name: selector.Output, RootResourceType: "Observation", RowGrain: "resource"},
			{Name: secondSelector.Output, RootResourceType: "Observation", RowGrain: "resource"},
		}},
		EmittedColumns: emitted, SourceGeneration: "generation-a",
		Publication: viewer.PublicationMetadata{State: string(explorer.RevisionReady), Generation: "generation-a", ExecutionID: execution.ID},
		Status:      explorer.RevisionReady,
	}
	if _, err := service.constructionInputPublicationMetadata(context.Background(), execution, "project-a"); err != nil {
		t.Fatal(err)
	}
	service.revisions.(*constructionfixtures.Revisions).Calls = nil

	request := Request{SnapshotToken: snapshot.Token, ExpectedDraftVersion: 1, ExpectedDraftDigest: digest, Limit: 2}
	response, err := service.List(context.Background(), "project-a", "builder-a", request)
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Entries) != 2 {
		t.Fatalf("pinned source entries = %#v", response.Entries)
	}
	entriesByOutput := make(map[string]inputEntry, len(response.Entries))
	for _, entry := range response.Entries {
		entriesByOutput[entry.OutputID] = entry
	}
	if got := entriesByOutput[selector.Output]; got.TableTitle != "Published clinical tables" || got.OutputTitle != "Observation measurements" || got.RevisionID != execution.ID || got.Columns[0].Name != "col_opaque_observation" || got.Columns[0].Label != "Observed value" {
		t.Fatalf("immutable observation output presentation = %#v", got)
	}
	if got := entriesByOutput[secondSelector.Output]; got.TableTitle != "Published clinical tables" || got.OutputTitle != secondSelector.Output || got.RevisionID != execution.ID || got.Columns[0].Name != "col_opaque_diagnosis" || got.Columns[0].Label != "Diagnosis code" {
		t.Fatalf("immutable diagnosis output presentation = %#v", got)
	}
	if calls := service.revisions.(*constructionfixtures.Revisions).Calls; !reflect.DeepEqual(calls, []string{"authoring_source-revision"}) {
		t.Fatalf("immutable source revision reads = %#v, want one read per execution receipt", calls)
	}

	validRevision := *service.revisions.(*constructionfixtures.Revisions).Values["authoring_source-revision"]
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
			service.revisions.(*constructionfixtures.Revisions).Values["authoring_source-revision"] = &revision

			listed, err := service.List(context.Background(), "project-a", "builder-a", request)
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
			service.revisions.(*constructionfixtures.Revisions).Values["authoring_source-revision"] = &mismatched
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
	execution := constructionfixtures.Execution("legacy-published-execution", selector, "project-a", "generation-a", time.Now())
	execution.ReceiptID = "receipt_legacy-published-execution"
	execution.Outputs[0].Columns = []publication.PhysicalColumn{{ID: "stable-opaque-id", Name: "col_opaque", ClickHouse: "String", LogicalType: "string"}}
	bundleCatalog.Executions = []publication.BundleExecution{execution}
	materialization := constructionfixtures.Materialization(execution, selector, authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted})
	materialization.ReceiptID = execution.ReceiptID
	materialization.Columns = []published.Column{{ID: "stable-opaque-id", Name: "col_opaque", ClickHouse: "String", LogicalType: "string"}}
	reader.Values[execution.ID+"/"+selector.Output] = materialization

	response, err := service.List(context.Background(), "project-a", "builder-a", Request{
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
	if calls := service.revisions.(*constructionfixtures.Revisions).Calls; !reflect.DeepEqual(calls, []string{"authoring_legacy-published-execution"}) {
		t.Fatalf("missing source revision lookup = %#v", calls)
	}
}

func TestConstructionInputsCatalogFiltersUnauthorizedScopeAndSupportsStablePaging(t *testing.T) {
	scope := authscope.ReadScope{Mode: authscope.ReadScopeRestricted, AuthResourcePaths: []string{"allowed-a"}}
	service, reader, bundleCatalog, snapshot, digest := newConstructionInputsFixture(t, scope)
	selectorA := dataset.DataframeSelector{Recipe: "labs", TranslationVersion: "v1", Output: "observations"}
	selectorB := dataset.DataframeSelector{Recipe: "visits", TranslationVersion: "v1", Output: "encounters"}
	selectorNext := dataset.DataframeSelector{Recipe: "tests", TranslationVersion: "v1", Output: "tests"}
	allowed := constructionfixtures.Execution("revision-allowed", selectorA, "project-a", "generation-a", time.Now())
	allowedNext := constructionfixtures.Execution("revision-allowed-next", selectorNext, "project-a", "generation-a", time.Now())
	denied := constructionfixtures.Execution("revision-denied", selectorB, "project-a", "generation-a", time.Now())
	constructionfixtures.SetExecutionScope(&allowed, scope)
	constructionfixtures.SetExecutionScope(&allowedNext, scope)
	constructionfixtures.SetExecutionScope(&denied, authscope.ReadScope{Mode: authscope.ReadScopeRestricted, AuthResourcePaths: []string{"allowed-b"}})
	bundleCatalog.Executions = []publication.BundleExecution{allowed, allowedNext, denied}
	reader.Values[allowed.ID+"/"+selectorA.Output] = constructionfixtures.Materialization(allowed, selectorA, scope)
	reader.Values[allowedNext.ID+"/"+selectorNext.Output] = constructionfixtures.Materialization(allowedNext, selectorNext, scope)
	reader.Values[denied.ID+"/"+selectorB.Output] = constructionfixtures.Materialization(denied, selectorB, authscope.ReadScope{Mode: authscope.ReadScopeRestricted, AuthResourcePaths: []string{"allowed-b"}})
	service.scopes = authscope.NewScopeResolver(authscope.ScopeResolverConfig{
		ResourceAccess: constructionInputsResourceAccess{"allowed-a"},
		ListExistingAuthResourcePaths: func(_ context.Context, options authscope.AuthResourcePathOptions) ([]string, error) {
			if options.Project != "project-a" || options.DatasetGeneration != "generation-a" {
				t.Fatalf("scope lookup used project/generation %q/%q", options.Project, options.DatasetGeneration)
			}
			return []string{"allowed-a", "allowed-b"}, nil
		},
	})
	ctx := authscope.ContextWithPrincipal(context.Background(), &authscope.Principal{AuthResourcePaths: []string{"allowed-a"}})
	allowedMaterialization := reader.Values[allowed.ID+"/"+selectorA.Output]
	persistedScope, err := reciperuntime.PersistedMaterializationScope(allowedMaterialization)
	if err != nil {
		t.Fatal(err)
	}
	effectiveScope, err := service.effectiveConstructionInputScope(ctx, persistedScope, allowedMaterialization)
	if err != nil || !reciperuntime.SameClickHouseReadScope(effectiveScope, scope) {
		t.Fatalf("allowed materialization scope = %#v, err=%v", effectiveScope, err)
	}
	request := Request{SnapshotToken: snapshot.Token, ExpectedDraftVersion: 1, ExpectedDraftDigest: digest, Limit: 1}
	first, err := service.List(ctx, "project-a", "builder-a", request)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Entries) != 1 || first.Entries[0].RevisionID != allowed.ID || first.NextCursor == "" {
		t.Fatalf("first authorized page = %#v", first)
	}
	request.Cursor = first.NextCursor
	second, err := service.List(ctx, "project-a", "builder-a", request)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Entries) != 1 || second.Entries[0].RevisionID != allowedNext.ID || second.NextCursor != "" {
		t.Fatalf("second authorized page = %#v", second)
	}
	request.Cursor = first.NextCursor
	request.Query = "different query"
	if _, err := service.List(ctx, "project-a", "builder-a", request); err == nil {
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
	request := Request{SnapshotToken: snapshot.Token, ExpectedDraftVersion: 2, ExpectedDraftDigest: digest}
	if _, err := service.List(context.Background(), "project-a", "builder-a", request); err == nil {
		t.Fatal("stale draft was accepted")
	}
	if bundleCatalog.ListCalls != 0 {
		t.Fatalf("published catalog was read before draft validation: %d calls", bundleCatalog.ListCalls)
	}
}

func TestConstructionInputsCatalogRejectsMalformedRequest(t *testing.T) {
	_, _, _, snapshot, digest := newConstructionInputsFixture(t, authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted})
	for _, request := range []Request{
		{SnapshotToken: snapshot.Token, ExpectedDraftVersion: 0, ExpectedDraftDigest: digest},
		{SnapshotToken: snapshot.Token, ExpectedDraftVersion: 1, ExpectedDraftDigest: digest, Limit: constructionInputsMaxLimit + 1},
		{SnapshotToken: snapshot.Token, ExpectedDraftVersion: 1, ExpectedDraftDigest: digest, Query: strings.Repeat("x", constructionInputsMaxQuery+1)},
	} {
		if _, _, _, err := validateConstructionInputsRequest(request); err == nil {
			t.Fatalf("malformed request accepted: %#v", request)
		}
	}
}

func TestConstructionInputsWithoutReaderIsUnavailable(t *testing.T) {
	service, _, _, _, _ := newConstructionInputsFixture(t, authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted})
	service.reader = nil
	_, err := service.List(context.Background(), "project-a", "builder-a", Request{
		SnapshotToken: "snapshot", ExpectedDraftVersion: 1, ExpectedDraftDigest: "digest",
	})
	var authoringErr *explorer.AuthoringError
	if !errors.As(err, &authoringErr) || authoringErr.Status != 503 ||
		authoringErr.Diagnostic.Code != "AUTHORING_UNAVAILABLE" ||
		authoringErr.Diagnostic.Message != "published construction inputs are not configured" {
		t.Fatalf("nil construction inputs reader = %#v, want 503 AUTHORING_UNAVAILABLE not configured", err)
	}
}

func newConstructionInputsFixture(t *testing.T, scope authscope.ReadScope) (Catalog, *constructionfixtures.Reader, *constructionfixtures.BundleCatalog, capability.Snapshot, string) {
	t.Helper()
	fixture, err := constructionfixtures.New(scope)
	if err != nil {
		t.Fatal(err)
	}
	service := Catalog{
		reader: fixture.Reader, catalog: fixture.Catalog, scopes: nil,
		revisions: fixture.Revisions, explorers: fixture.Explorers,
		capabilities: fixture.Capabilities,
	}
	return service, fixture.Reader, fixture.Catalog, fixture.Snapshot, fixture.Digest
}

type constructionInputsResourceAccess []string

func (a constructionInputsResourceAccess) GetAllowedResources(context.Context, string, string, string) ([]string, error) {
	return append([]string(nil), a...), nil
}
