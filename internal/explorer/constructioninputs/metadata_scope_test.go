package constructioninputs

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/explorer/receiptmodel"
	"github.com/calypr/loom/internal/explorer/viewer"

	"github.com/calypr/loom/internal/testkit/constructionfixtures"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/publication"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/explorer"
)

func TestConstructionInputsCatalogDoesNotReadUnauthorizedRevisionMetadata(t *testing.T) {
	scope := authscope.ReadScope{Mode: authscope.ReadScopeRestricted, AuthResourcePaths: []string{"allowed-a"}}
	service, reader, bundleCatalog, snapshot, digest := newConstructionInputsFixture(t, scope)
	selector := dataset.DataframeSelector{Recipe: "labs", TranslationVersion: "v1", Output: "observations"}
	createdAt := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	active := constructionfixtures.Execution("revision-active", selector, "project-a", "generation-a", createdAt)
	superseded := constructionfixtures.Execution("revision-superseded", selector, "project-a", "generation-a", createdAt.Add(time.Minute))
	denied := constructionfixtures.Execution("revision-denied", selector, "project-a", "generation-a", createdAt.Add(2*time.Minute))
	for _, execution := range []*publication.BundleExecution{&active, &superseded} {
		constructionfixtures.SetExecutionScope(execution, scope)
		execution.ReceiptID = "receipt_" + execution.ID
	}
	constructionfixtures.SetExecutionScope(&denied, authscope.ReadScope{Mode: authscope.ReadScopeRestricted, AuthResourcePaths: []string{"allowed-b"}})
	denied.ReceiptID = "receipt_" + denied.ID
	bundleCatalog.Executions = []publication.BundleExecution{active, denied, superseded}
	bundleCatalog.Pointers[active.PointerName()] = publication.BundlePointer{Name: active.PointerName(), ExecutionID: active.ID}
	for _, execution := range []publication.BundleExecution{active, superseded} {
		materialization := constructionfixtures.Materialization(execution, selector, scope)
		materialization.ReceiptID = execution.ReceiptID
		reader.Values[execution.ID+"/"+selector.Output] = materialization
	}
	deniedScope := authscope.ReadScope{Mode: authscope.ReadScopeRestricted, AuthResourcePaths: []string{"allowed-b"}}
	deniedMaterialization := constructionfixtures.Materialization(denied, selector, deniedScope)
	deniedMaterialization.ReceiptID = denied.ReceiptID
	reader.Values[denied.ID+"/"+selector.Output] = deniedMaterialization

	revisions := service.revisions.(*constructionfixtures.Revisions)
	revisions.Values["authoring_"+active.ID] = constructionInputsImmutablePresentationRevision(t, active, "Allowed active table", "Active observations", "Allowed active value", explorer.RevisionActive)
	revisions.Values["authoring_"+superseded.ID] = constructionInputsImmutablePresentationRevision(t, superseded, "Allowed historical table", "Historical observations", "Allowed historical value", explorer.RevisionSuperseded)
	revisions.Values["authoring_"+denied.ID] = constructionInputsImmutablePresentationRevision(t, denied, "DENIED_SECRET_TABLE", "DENIED_SECRET_OUTPUT", "DENIED_SECRET_COLUMN", explorer.RevisionActive)

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
	response, err := service.List(ctx, "project-a", "builder-a", Request{
		SnapshotToken: snapshot.Token, ExpectedDraftVersion: 1, ExpectedDraftDigest: digest,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Entries) != 2 {
		t.Fatalf("authorized immutable revisions = %#v, want active and superseded entries only", response.Entries)
	}
	entries := make(map[string]inputEntry, len(response.Entries))
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
	if !reflect.DeepEqual(revisions.Calls, []string{"authoring_" + active.ID, "authoring_" + superseded.ID}) {
		t.Fatalf("immutable revision lookups = %#v, want only authorized active and superseded revisions", revisions.Calls)
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
	workspace, err := ws.DecodeWorkspace(constructionfixtures.BaselineExplorerWorkspace())
	if err != nil {
		t.Fatal(err)
	}
	workspace.Explorer.Title = tableTitle
	document := workspace.Documents[0]
	document.Output = ws.Output{ID: execution.Outputs[0].Name, Title: outputTitle}
	document.RootResourceType = "Observation"
	document.Route.ResourceType = "Observation"
	workspace.Documents = []ws.Document{document}
	workspaceJSON, err := json.Marshal(workspace)
	if err != nil {
		t.Fatal(err)
	}
	contracts, err := json.Marshal(receiptmodel.PublicOutputContracts{Outputs: []receiptmodel.PublicOutputContract{{
		OutputID: execution.Outputs[0].Name, RootResourceType: "Observation", RowGrain: "resource",
		Columns: []receiptmodel.PublicOutputColumn{{Column: "value", Label: columnLabel, LogicalType: "decimal"}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	return &explorer.Revision{
		ID: "authoring_" + strings.TrimPrefix(execution.ReceiptID, "receipt_"), Project: execution.Project,
		CompilationReceiptID: execution.ReceiptID, PublicOutputContract: contracts, AuthoringBundle: workspaceJSON,
		Recipe:           recipe.Bundle{Outputs: []recipe.Output{{Name: execution.Outputs[0].Name, RootResourceType: "Observation", RowGrain: "resource"}}},
		EmittedColumns:   []viewer.EmittedColumn{{OutputID: execution.Outputs[0].Name, PublicColumn: "value", Label: columnLabel, LogicalType: "decimal"}},
		SourceGeneration: execution.DatasetGeneration,
		Publication:      viewer.PublicationMetadata{State: string(explorer.RevisionActive), Generation: execution.DatasetGeneration, ExecutionID: execution.ID},
		Status:           status,
	}
}
