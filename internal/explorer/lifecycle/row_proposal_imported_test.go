package lifecycle

import (
	"context"
	"strings"
	"testing"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/explorer/receiptmodel"

	"github.com/calypr/loom/internal/explorer/rowdef"

	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
)

func TestImportedSourceRowProposalRejectsProjectedRowsWithoutCompilingOrSaving(t *testing.T) {
	for _, selection := range []rowdef.RowDefinitionSelection{
		{Kind: rowdef.RowDefinitionSelectionRecords},
		{Kind: rowdef.RowDefinitionSelectionExpanded, Expanded: &rowdef.ExpandedSelection{RowChoiceID: "opaque-choice-1", EmptyCollectionPolicy: ws.EmptyCollectionPreserveParent}},
		{Kind: rowdef.RowDefinitionSelectionExplicitGroup, ExplicitGroup: &rowdef.ExplicitGroupSelection{RevisionID: "group-v1", UnassignedMemberPolicy: ws.UnassignedMemberExclude}},
	} {
		t.Run(string(selection.Kind), func(t *testing.T) {
			service, store, snapshot, workspace := rowProposalService(t)
			workspace.Documents[0].ImportedRecipe = &ws.ImportedRecipeRef{Name: "default", TranslationVersion: "v1", Digest: strings.Repeat("a", 64), Output: "patients"}
			for i := range workspace.Documents[0].Columns {
				column := &workspace.Documents[0].Columns[i]
				column.Source = ws.ColumnSource{Kind: ws.SourceImported, Imported: &ws.ImportedSource{OutputColumn: column.Column}}
			}
			raw, err := workspace.CanonicalJSON()
			if err != nil {
				t.Fatal(err)
			}
			digest, err := workspace.Digest()
			if err != nil {
				t.Fatal(err)
			}
			store.created.DraftConfig = raw
			store.created.DraftDigest = digest
			original := string(raw)
			version := store.created.DraftVersion
			compileCalls := 0
			service.config.CompileReceipt = func(context.Context, ports.CompileReceiptRequest) (*receiptmodel.CompilationReceipt, error) {
				compileCalls++
				t.Error("rejected projected source row edit reached compiler")
				return nil, nil
			}
			request := rowProposalRequest(store.created, snapshot)
			request.Selection = selection
			_, err = service.ProposeRowDefinition(context.Background(), request)
			if lifecycleErrorCode(err) != "IMPORTED_SOURCE_ROW_OVERLAY_REQUIRED" {
				t.Fatalf("got %v; want precise source ownership rejection", err)
			}
			if compileCalls != 0 || store.saveDraftCalls != 0 || store.created.DraftVersion != version || store.created.DraftDigest != digest || string(store.created.DraftConfig) != original {
				t.Fatal("rejected source rows edit changed saved state")
			}
		})
	}
}
