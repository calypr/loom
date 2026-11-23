package lifecycle

import (
	"context"
	"testing"

	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	constructionpkg "github.com/calypr/loom/internal/explorer/construction"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
)

func TestProposeConstructionCascadesRemovedRelatedExpandDependencies(t *testing.T) {
	for _, test := range []struct {
		name         string
		removeStepID string
		wantRemoved  []string
		wantKept     []string
	}{
		{
			name:         "first expansion removes dependent chain but keeps independent later filter",
			removeStepID: "expand_observations",
			wantRemoved:  []string{"expand_observations", "expand_conditions", "expand_medications"},
			wantKept:     []string{"keep_root_filter"},
		},
		{
			name:         "middle expansion removes its dependent suffix but keeps independent later filter",
			removeStepID: "expand_conditions",
			wantRemoved:  []string{"expand_conditions", "expand_medications"},
			wantKept:     []string{"expand_observations", "keep_root_filter"},
		},
		{
			name:         "leaf expansion removes only itself",
			removeStepID: "expand_medications",
			wantRemoved:  []string{"expand_medications"},
			wantKept:     []string{"expand_observations", "expand_conditions", "keep_root_filter"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			service, store, snapshot := constructionProposalService(t)
			workspace, construction := seedRelatedExpandRemovalConstruction(t, store)
			candidate := constructionWithoutStep(construction, test.removeStepID)
			candidateCompileCalls := 0
			var candidateBinding *explorer.ConstructionProposalBinding
			service.config.CompileReceipt = func(_ context.Context, request ports.CompileReceiptRequest) (*explorer.CompilationReceipt, error) {
				if request.ConstructionProposal != nil {
					candidateCompileCalls++
					candidateBinding = cloneConstructionProposalBinding(request.ConstructionProposal)
				}
				receipt := lifecycleConstructionReceipt(snapshot, request.Workspace)
				receipt.ConstructionProposal = cloneConstructionProposalBinding(request.ConstructionProposal)
				receipt.ConstructionStages = map[string][]explorer.ReceiptConstructionStage{
					"patients": relatedExpandRemovalTestStages(t, request.Workspace, "patients"),
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

			proposal, err := service.ProposeConstruction(context.Background(), constructionpkg.ConstructionProposalRequest{
				Project: store.created.Project, ExplorerID: store.created.ExplorerID, SnapshotToken: snapshot.Token,
				ExpectedDraftVersion: store.created.DraftVersion, ExpectedDraftDigest: store.created.DraftDigest,
				OutputID: "patients", RemoveStepIDs: []string{test.removeStepID}, CandidateConstruction: candidate,
			})
			if err != nil {
				t.Fatalf("propose removal: %v", err)
			}
			if proposal.PreviewStatus != "PREVIEW_PENDING" || proposal.ProposalID == "" {
				t.Fatalf("removal proposal = status %q, proposal ID %q; want a preview proposal", proposal.PreviewStatus, proposal.ProposalID)
			}
			if candidateCompileCalls != 1 || candidateBinding == nil {
				t.Fatalf("candidate compilation calls = %d, binding = %#v; want one bound candidate", candidateCompileCalls, candidateBinding)
			}
			if len(proposal.DependencyImpact.MissingInputs) != 0 {
				t.Fatalf("removal still requires repair: %#v", proposal.DependencyImpact.MissingInputs)
			}
			if !equalStringSlices(proposal.DependencyImpact.RemovedStepIDs, test.wantRemoved) {
				t.Fatalf("removed steps = %#v, want %#v", proposal.DependencyImpact.RemovedStepIDs, test.wantRemoved)
			}
			if !equalStringSlices(constructionStepIDs(proposal.CandidateConstruction.Steps), test.wantKept) {
				t.Fatalf("candidate steps = %#v, want %#v", constructionStepIDs(proposal.CandidateConstruction.Steps), test.wantKept)
			}
			if !equalStringSlices(candidateBinding.RemoveStepIDs, test.wantRemoved) {
				t.Fatalf("receipt binding removals = %#v, want %#v", candidateBinding.RemoveStepIDs, test.wantRemoved)
			}
			if len(workspace.Documents) != 1 || len(store.created.DraftConfig) == 0 {
				t.Fatal("proposal mutated or lost the seeded workspace")
			}

			if test.removeStepID == "expand_observations" {
				service.config.PreviewReceipt = func(_ context.Context, _ *explorer.CompilationReceipt, _ recipe.RuntimeBindings, visit func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
					if err := visit(map[string]any{"patient_id": "p1"}); err != nil {
						return dataframeexecution.PreviewSummary{}, err
					}
					return dataframeexecution.PreviewSummary{Output: "patients", Columns: []string{"patient_id"}, RowCount: 1, Complete: true}, nil
				}
				version, digest := store.created.DraftVersion, store.created.DraftDigest
				_, err := service.ApplyCommands(context.Background(), "project-a", "patients", authoringv2.ApplyCommandsRequest{
					CommandID: "apply-cascading-removal", SemanticsVersion: authoringv2.CurrentSemanticsVersion,
					SnapshotToken: snapshot.Token, ExpectedDraftVersion: version, ExpectedDraftDigest: digest,
					Commands: []authoringv2.Command{{Type: authoringv2.CommandApplyConstructionProposal, OutputID: "patients", Payload: &authoringv2.ProposalPayload{ProposalID: proposal.ProposalID}}},
				}, "alice")
				if err != nil {
					t.Fatalf("apply cascade-bound proposal: %v", err)
				}
			}
		})
	}
}

func TestProposeConstructionGroupEditCascadesMissingKeyFilterAndApplyBindsRemoval(t *testing.T) {
	service, store, snapshot := constructionProposalService(t)
	workspace, construction := seedGroupKeyEditConstruction(t, store)
	candidate := construction
	candidate.Steps = append([]authoringv2.ConstructionStep(nil), construction.Steps...)
	candidate.Steps[0].Operation.Group.Keys = nil
	candidate.Steps[0].Outputs = []authoringv2.StageColumn{{ID: "row_count", Name: "row_count", Label: "Rows", Type: "integer"}}
	candidateCompileCalls := 0
	var candidateBinding *explorer.ConstructionProposalBinding
	service.config.CompileReceipt = func(_ context.Context, request ports.CompileReceiptRequest) (*explorer.CompilationReceipt, error) {
		if request.ConstructionProposal != nil {
			candidateCompileCalls++
			candidateBinding = cloneConstructionProposalBinding(request.ConstructionProposal)
		}
		receipt := lifecycleConstructionReceipt(snapshot, request.Workspace)
		receipt.ConstructionProposal = cloneConstructionProposalBinding(request.ConstructionProposal)
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
	proposal, err := service.ProposeConstruction(context.Background(), constructionpkg.ConstructionProposalRequest{
		Project: store.created.Project, ExplorerID: store.created.ExplorerID, SnapshotToken: snapshot.Token,
		ExpectedDraftVersion: store.created.DraftVersion, ExpectedDraftDigest: store.created.DraftDigest,
		OutputID: "patients", ChangedStepID: "group-records", CandidateConstruction: candidate,
	})
	if err != nil {
		t.Fatalf("propose GROUP edit with dependent filter: %v", err)
	}
	wantSteps := []string{"group-records", "independent-count-filter"}
	if proposal.PreviewStatus != "PREVIEW_PENDING" || !equalStringSlices(proposal.DependencyImpact.RemovedStepIDs, []string{"dependent-id-filter"}) ||
		!equalStringSlices(constructionStepIDs(proposal.CandidateConstruction.Steps), wantSteps) {
		t.Fatalf("GROUP edit proposal did not cascade only the missing-key filter: status=%s impact=%#v steps=%#v", proposal.PreviewStatus, proposal.DependencyImpact, constructionStepIDs(proposal.CandidateConstruction.Steps))
	}
	if candidateCompileCalls != 1 || candidateBinding == nil || !equalStringSlices(candidateBinding.RemoveStepIDs, []string{"dependent-id-filter"}) {
		t.Fatalf("candidate binding = %#v, compile calls=%d; want cascade removal bound for Apply", candidateBinding, candidateCompileCalls)
	}
	service.config.PreviewReceipt = func(_ context.Context, _ *explorer.CompilationReceipt, _ recipe.RuntimeBindings, visit func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
		if err := visit(map[string]any{"row_count": int64(1)}); err != nil {
			return dataframeexecution.PreviewSummary{}, err
		}
		return dataframeexecution.PreviewSummary{Output: "patients", Columns: []string{"row_count"}, RowCount: 1, Complete: true}, nil
	}
	version, digest := store.created.DraftVersion, store.created.DraftDigest
	response, err := service.ApplyCommands(context.Background(), "project-a", "patients", authoringv2.ApplyCommandsRequest{
		CommandID: "apply-group-cascade", SemanticsVersion: authoringv2.CurrentSemanticsVersion,
		SnapshotToken: snapshot.Token, ExpectedDraftVersion: version, ExpectedDraftDigest: digest,
		Commands: []authoringv2.Command{{Type: authoringv2.CommandApplyConstructionProposal, OutputID: "patients", Payload: &authoringv2.ProposalPayload{ProposalID: proposal.ProposalID}}},
	}, "alice")
	if err != nil {
		t.Fatalf("apply GROUP edit cascade: %v", err)
	}
	if response.Workspace.Documents[0].Construction == nil || !equalStringSlices(constructionStepIDs(response.Workspace.Documents[0].Construction.Steps), wantSteps) {
		t.Fatalf("applied construction = %#v, want GROUP and independent count filter", response.Workspace.Documents[0].Construction)
	}
	if len(workspace.Documents) != 1 {
		t.Fatal("GROUP edit proposal changed the saved workspace before Apply")
	}
}

func seedGroupKeyEditConstruction(t *testing.T, store *fakeStore) (authoringv2.Workspace, authoringv2.Construction) {
	t.Helper()
	workspace, err := authoringv2.DecodeWorkspace(store.created.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	document, err := authoringv2.StartDocumentConstruction(workspace.Documents[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(document.Columns) == 0 {
		t.Fatal("group-key test document has no source columns")
	}
	inputID := document.Columns[0].ColumnID
	groupOutputs := []authoringv2.StageColumn{
		{ID: "group_id", Name: "group_id", Label: "Group ID", Type: "string"},
		{ID: "row_count", Name: "row_count", Label: "Rows", Type: "integer"},
	}
	construction := authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{
		{
			ID: "group-records", Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputSourceProjection}},
			Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationGroup, Group: &authoringv2.ConstructionGroup{
				ConstructionID: "group-records", MissingKeyPolicy: authoringv2.ConstructionGroupMissingKeyGroup,
				Keys:       []authoringv2.ConstructionGroupKey{{InputColumnID: inputID, OutputColumnID: "group_id"}},
				Aggregates: []authoringv2.ConstructionGroupAggregate{{Operation: authoringv2.ConstructionGroupCountRows, OutputColumnID: "row_count"}},
			}},
			Outputs: groupOutputs,
		},
		{
			ID: "dependent-id-filter", Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputStepOutput, StepID: "group-records"}},
			Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationFilter, Filter: &authoringv2.ConstructionFilter{
				ColumnID: "group_id", Operator: authoringv2.ConstructionFilterExists,
			}},
			Outputs: append([]authoringv2.StageColumn(nil), groupOutputs...),
		},
		{
			ID: "independent-count-filter", Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputStepOutput, StepID: "dependent-id-filter"}},
			Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationFilter, Filter: &authoringv2.ConstructionFilter{
				ColumnID: "row_count", Operator: authoringv2.ConstructionFilterExists,
			}},
			Outputs: append([]authoringv2.StageColumn(nil), groupOutputs...),
		},
	}}
	document.Construction = &construction
	workspace.Documents[0] = document
	if err := workspace.Validate(); err != nil {
		t.Fatalf("validate GROUP key dependency fixture: %v", err)
	}
	encoded, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	store.created.DraftConfig = encoded
	store.created.DraftDigest = digest
	return workspace, construction
}
