package lifecycle

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
)

func TestProposeConstructionPivotLabelOnlyKeepsSavedEmptyCategory(t *testing.T) {
	for _, semanticChange := range []bool{false, true} {
		t.Run(map[bool]string{false: "label only", true: "category changed"}[semanticChange], func(t *testing.T) {
			service, store, snapshot, _, _ := constructionProposalPivotFixture(t)
			scans := 0
			service.config.ScanCategories = func(_ context.Context, receipt *explorer.CompilationReceipt, _ recipe.RuntimeBindings, scan dataframeexecution.CategoryScanRequest) (dataframeexecution.CategoryScanResult, error) {
				scans++
				if !scan.RequireValue {
					t.Fatal("category proof must remain value-bearing")
				}
				var category explorer.ReceiptConstructionStageColumn
				for _, stage := range receipt.ConstructionStages["patients"] {
					if stage.ID == scan.StageID {
						for _, column := range stage.Columns {
							if column.ID == scan.ColumnID {
								category = column
							}
						}
					}
				}
				proof := compilerCategoryScanProof(ConstructionCategoryDiscoveryRequest{OutputID: scan.Output, StageID: scan.StageID, CategoryColumnID: scan.ColumnID, ValueColumnID: scan.ValueColumnID}, category)
				return dataframeexecution.CategoryScanResult{Values: []dataframeexecution.CategoryValue{{Present: true, Value: "other-observed-category"}}, Complete: true, Proof: proof}, nil
			}
			request := constructionProposalPivotRequest(store.created, snapshot, []authoringv2.ConstructionPivotCategory{{Key: lifecycleTestStringScalar("final"), OutputColumnID: "pivot_final"}})
			request.CandidateConstruction.Steps[0].Outputs[1].Label = "Renamed empty category"
			if semanticChange {
				request.CandidateConstruction.Steps[0].Operation.Pivot.Categories[0].Key = lifecycleTestStringScalar("new-unobserved-category")
			}
			before := append([]byte(nil), store.created.DraftConfig...)
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
func TestPivotLabelOnlyEditRejectsChangedInputAncestry(t *testing.T) {
	service, store, snapshot, _, _ := constructionProposalPivotFixture(t)
	_ = service
	request := constructionProposalPivotRequest(store.created, snapshot, []authoringv2.ConstructionPivotCategory{{Key: lifecycleTestStringScalar("final"), OutputColumnID: "pivot_final"}})
	base := request.CandidateConstruction
	prefix := constructionPivotFilterStep()
	prefix.ID = "upstream"
	prefix.Inputs = []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputSourceProjection}}
	base.Steps = append([]authoringv2.ConstructionStep{prefix}, base.Steps...)
	base.Steps[1].Inputs = []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputStepOutput, StepID: "upstream"}}
	clone := func(c authoringv2.Construction) authoringv2.Construction {
		raw, err := json.Marshal(c)
		if err != nil {
			t.Fatal(err)
		}
		var result authoringv2.Construction
		if err = json.Unmarshal(raw, &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	candidate := clone(base)
	candidate.Steps[1].Outputs[1].Label = "Renamed"
	if !constructionPivotLabelOnlyEdit(base, candidate, "pivot_status") {
		t.Fatal("unchanged ancestry label edit refused")
	}

	for _, tc := range []struct {
		name   string
		mutate func(*authoringv2.Construction)
	}{
		{"upstream membership", func(c *authoringv2.Construction) {
			c.Steps[0].Operation.Filter.Operator = authoringv2.ConstructionFilterMissing
		}},
		{"output name", func(c *authoringv2.Construction) { c.Steps[1].Outputs[1].Name = "different_name" }},
		{"output ID", func(c *authoringv2.Construction) { c.Steps[1].Outputs[1].ID = "different_id" }},
		{"output type", func(c *authoringv2.Construction) { c.Steps[1].Outputs[1].Type = "integer" }},
		{"output nullability", func(c *authoringv2.Construction) { c.Steps[1].Outputs[1].Nullable = !c.Steps[1].Outputs[1].Nullable }},
		{"input stage", func(c *authoringv2.Construction) {
			c.Steps[1].Inputs[0] = authoringv2.ConstructionInputRef{Kind: authoringv2.ConstructionInputSourceProjection}
		}},
		{"source projection", func(c *authoringv2.Construction) {
			c.SourceProjections = append(c.SourceProjections, authoringv2.ConstructionSourceProjection{ColumnID: "different_source"})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed := clone(candidate)
			tc.mutate(&changed)
			if constructionPivotLabelOnlyEdit(base, changed, "pivot_status") {
				t.Fatal("semantic change classified as label-only")
			}
		})
	}

}
