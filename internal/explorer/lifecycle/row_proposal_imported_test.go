package lifecycle

import (
	"context"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
)

func TestImportedSourceRowProposalRejectsProjectedRowsWithoutCompilingOrSaving(t *testing.T) {
	for _, selection := range []RowDefinitionSelection{
		{Kind: RowDefinitionSelectionRecords},
		{Kind: RowDefinitionSelectionExpanded, Expanded: &ExpandedSelection{RowChoiceID: "opaque-choice-1", EmptyCollectionPolicy: authoringv2.EmptyCollectionPreserveParent}},
		{Kind: RowDefinitionSelectionExplicitGroup, ExplicitGroup: &ExplicitGroupSelection{RevisionID: "group-v1", UnassignedMemberPolicy: authoringv2.UnassignedMemberExclude}},
	} {
		t.Run(string(selection.Kind), func(t *testing.T) {
			service, store, snapshot, workspace := rowProposalService(t)
			workspace.Documents[0].ImportedRecipe = &authoringv2.ImportedRecipeRef{Name: "default", TranslationVersion: "v1", Digest: strings.Repeat("a", 64), Output: "patients"}
			for i := range workspace.Documents[0].Columns {
				column := &workspace.Documents[0].Columns[i]
				column.Source = authoringv2.ColumnSource{Kind: authoringv2.SourceImported, Imported: &authoringv2.ImportedSource{OutputColumn: column.Column}}
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
			service.config.CompileReceipt = func(context.Context, CompileReceiptRequest) (*explorer.CompilationReceipt, error) {
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
