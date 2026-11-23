package lifecycle

import (
	"context"
	"reflect"
	"testing"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/explorer/receiptmodel"

	"github.com/calypr/loom/internal/explorer/construction"

	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

func TestProposeConstructionPivotLabelOnlyKeepsSavedEmptyCategory(t *testing.T) {
	for _, semanticChange := range []bool{false, true} {
		t.Run(map[bool]string{false: "label only", true: "category changed"}[semanticChange], func(t *testing.T) {
			setup, _, _ := constructionProposalPivotSetup(t)
			store, snapshot := setup.Store, setup.Snapshot
			scans := 0
			setup.Config.ScanCategories = func(_ context.Context, receipt *receiptmodel.CompilationReceipt, _ recipe.RuntimeBindings, scan dataframeexecution.CategoryScanRequest) (dataframeexecution.CategoryScanResult, error) {
				scans++
				if !scan.RequireValue {
					t.Fatal("category proof must remain value-bearing")
				}
				var category receiptmodel.ReceiptConstructionStageColumn
				for _, stage := range receipt.ConstructionStages["patients"] {
					if stage.ID == scan.StageID {
						for _, column := range stage.Columns {
							if column.ID == scan.ColumnID {
								category = column
							}
						}
					}
				}
				proof := compilerCategoryScanProof(construction.ConstructionCategoryDiscoveryRequest{OutputID: scan.Output, StageID: scan.StageID, CategoryColumnID: scan.ColumnID, ValueColumnID: scan.ValueColumnID}, category)
				return dataframeexecution.CategoryScanResult{Values: []dataframeexecution.CategoryValue{{Present: true, Value: "other-observed-category"}}, Complete: true, Proof: proof}, nil
			}
			request := constructionProposalPivotRequest(store.created, snapshot, []ws.ConstructionPivotCategory{{Key: lifecycleTestStringScalar("final"), OutputColumnID: "pivot_final"}})
			request.CandidateConstruction.Steps[0].Outputs[1].Label = "Renamed empty category"
			if semanticChange {
				request.CandidateConstruction.Steps[0].Operation.Pivot.Categories[0].Key = lifecycleTestStringScalar("new-unobserved-category")
			}
			before := append([]byte(nil), store.created.DraftConfig...)
			service := newTestServiceFromSetup(t, setup)
			proposal, err := service.ProposeConstruction(context.Background(), request)
			if semanticChange {
				if lifecycleErrorCode(err) != "PIVOT_CATEGORY_HAS_NO_VALUES" || proposal.ProposalID != "" {
					t.Fatalf("semantic edit must reject: %#v %v", proposal, err)
				}
			} else {
				if err != nil || proposal.ProposalID == "" || proposal.PreviewStatus != "PREVIEW_PENDING" {
					t.Fatalf("saved category label edit rejected: %#v %v", proposal, err)
				}
				if len(proposal.Diagnostics) != 1 || proposal.Diagnostics[0].Outcome != "CATEGORY_HAS_NO_VALUES" {
					t.Fatalf("missing saved-category diagnostic: %#v", proposal.Diagnostics)
				}
				if proposal.CandidateConstruction.Steps[0].Outputs[1].Label != "Renamed empty category" {
					t.Fatal("label edit was lost")
				}
			}
			if scans != 1 || store.saveDraftCalls != 0 || !reflect.DeepEqual(before, []byte(store.created.DraftConfig)) {
				t.Fatalf("proposal scan/save/draft mutation: scans=%d saves=%d", scans, store.saveDraftCalls)
			}
		})
	}
}
