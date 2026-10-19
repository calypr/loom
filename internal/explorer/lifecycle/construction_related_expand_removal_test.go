package lifecycle

import (
	"context"
	"encoding/json"
	"testing"

	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
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
			service.config.CompileReceipt = func(_ context.Context, request CompileReceiptRequest) (*explorer.CompilationReceipt, error) {
				if request.ConstructionProposal != nil {
					candidateCompileCalls++
					candidateBinding = cloneConstructionProposalBinding(request.ConstructionProposal)
				}
				receipt := lifecycleTableShapeReceipt(snapshot, request.Workspace)
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

			proposal, err := service.ProposeConstruction(context.Background(), ConstructionProposalRequest{
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
					Commands: []authoringv2.Command{{Type: authoringv2.CommandApplyConstructionProposal, OutputID: "patients", ProposalID: proposal.ProposalID}},
				}, "alice")
				if err != nil {
					t.Fatalf("apply cascade-bound proposal: %v", err)
				}
			}
		})
	}
}

func TestConstructionRemovalCascadeRemovesOwnerOfDependentRelatedField(t *testing.T) {
	_, store, _ := constructionProposalService(t)
	workspace, construction := seedRelatedExpandRemovalConstruction(t, store)
	document := workspace.Documents[0]
	construction.Steps = construction.Steps[:1]
	expansion := construction.Steps[0]
	statusColumnID := "observation_status"
	statusSource := authoringv2.ConstructionRelatedFieldSource{
		Kind: capability.ConstructionChoiceSourceField, CandidateID: "observation-status", NodeID: "observation-node",
		ResourceType: "Observation", Path: "status", Cardinality: "optional_one", LogicalType: "string",
	}
	relatedFieldOutputs := append(append([]authoringv2.StageColumn(nil), expansion.Outputs...), authoringv2.StageColumn{
		ID: statusColumnID, Name: statusColumnID, Label: "Observation status", Type: "string", Nullable: true,
	})
	relatedField := authoringv2.ConstructionStep{
		ID: "related_status_input", OwnerStepID: "group_status",
		Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputStepOutput, StepID: expansion.ID}},
		Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationRelatedField, RelatedField: &authoringv2.ConstructionRelatedField{
			ChoiceID: "saved-status-choice", Source: statusSource, OutputColumnID: statusColumnID,
		}},
		Outputs: relatedFieldOutputs,
	}
	patientIDColumnID := document.Columns[0].ColumnID
	groupOutputs := []authoringv2.StageColumn{
		{ID: patientIDColumnID, Name: document.Columns[0].Column, Label: document.Columns[0].Label, Type: document.Columns[0].LogicalType},
		{ID: "status_group", Name: "status_group", Label: "Observation status", Type: "string"},
		{ID: "row_count", Name: "row_count", Label: "Rows", Type: "integer"},
	}
	group := authoringv2.ConstructionStep{
		ID: "group_status", Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputStepOutput, StepID: relatedField.ID}},
		Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationGroup, Group: &authoringv2.ConstructionGroup{
			ConstructionID: "group_status", MissingKeyPolicy: authoringv2.ConstructionGroupMissingKeyGroup,
			Keys: []authoringv2.ConstructionGroupKey{
				{InputColumnID: patientIDColumnID, OutputColumnID: patientIDColumnID},
				{InputColumnID: statusColumnID, OutputColumnID: "status_group"},
			},
			Aggregates: []authoringv2.ConstructionGroupAggregate{{Operation: authoringv2.ConstructionGroupCountRows, OutputColumnID: "row_count"}},
		}},
		Outputs: groupOutputs,
	}
	filter := authoringv2.ConstructionStep{
		ID: "keep_root_filter", Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputStepOutput, StepID: group.ID}},
		Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationFilter, Filter: &authoringv2.ConstructionFilter{
			ColumnID: patientIDColumnID, Operator: authoringv2.ConstructionFilterExists,
		}},
		Outputs: append([]authoringv2.StageColumn(nil), groupOutputs...),
	}
	construction.Steps = append(construction.Steps, relatedField, group, filter)
	document.Construction = &construction
	workspace.Documents[0] = document
	if err := workspace.Validate(); err != nil {
		t.Fatalf("validate owned related-field construction: %v", err)
	}
	baseStages := relatedExpandRemovalTestStages(t, workspace, "patients")
	removedDocument, impact, err := analyzeConstructionCandidateWithCascade(document, constructionWithoutStep(construction, expansion.ID), "", []string{expansion.ID}, baseStages)
	if err != nil {
		t.Fatalf("cascade owner of removed related-field source: %v", err)
	}
	wantRemoved := []string{expansion.ID, relatedField.ID, group.ID}
	if !equalStringSlices(impact.RemovedStepIDs, wantRemoved) {
		t.Fatalf("removed steps = %#v, want expansion, its owned field input, and owner %#v", impact.RemovedStepIDs, wantRemoved)
	}
	if !equalStringSlices(constructionStepIDs(removedDocument.Construction.Steps), []string{filter.ID}) || impact.HasMissingInputs() {
		t.Fatalf("cascade did not preserve independent filter after removing owner: steps=%#v impact=%#v", constructionStepIDs(removedDocument.Construction.Steps), impact)
	}
}

func TestConstructionRelatedExpandTargetEditCascadesDependentsButPolicyEditDoesNot(t *testing.T) {
	_, store, _ := constructionProposalService(t)
	workspace, construction := seedRelatedExpandRemovalConstruction(t, store)
	document := workspace.Documents[0]
	baseStages := relatedExpandRemovalTestStages(t, workspace, "patients")
	firstStepID := construction.Steps[0].ID

	t.Run("target resource change removes dependent expansions before schema rebuild", func(t *testing.T) {
		candidate := cloneLifecycleTestConstruction(t, construction)
		first := &candidate.Steps[0]
		first.Operation.RelatedExpand.TargetNodeID = "condition"
		first.Operation.RelatedExpand.TargetResourceType = "Condition"
		first.Operation.RelatedExpand.Route[0].ToNodeID = "condition"
		first.Operation.RelatedExpand.Route[0].ToResourceType = "Condition"
		for outputIndex := range first.Outputs {
			if first.Outputs[outputIndex].ID == first.Operation.RelatedExpand.RelatedRecordColumnID {
				first.Outputs[outputIndex].Name = "related_conditions"
				first.Outputs[outputIndex].Label = "related_conditions"
			}
		}
		updated, impact, err := analyzeConstructionCandidateWithCascade(document, candidate, firstStepID, nil, baseStages)
		if err != nil {
			t.Fatalf("analyze target edit with dependent cascade: %v", err)
		}
		if !equalStringSlices(impact.RemovedStepIDs, []string{"expand_conditions", "expand_medications"}) {
			t.Fatalf("target edit removed steps = %#v, want dependent expansion suffix", impact.RemovedStepIDs)
		}
		if !equalStringSlices(constructionStepIDs(updated.Construction.Steps), []string{firstStepID, "keep_root_filter"}) || impact.HasMissingInputs() {
			t.Fatalf("target edit candidate = %#v, impact=%#v", constructionStepIDs(updated.Construction.Steps), impact)
		}
	})

	t.Run("same target with changed route and empty policy keeps valid downstream operations", func(t *testing.T) {
		candidate := cloneLifecycleTestConstruction(t, construction)
		candidate.Steps[0].Operation.RelatedExpand.EmptyPolicy = authoringv2.ConstructionExpandEmptyError
		candidate.Steps[0].Operation.RelatedExpand.Route[0].EdgeID = "edited-observations-edge"
		candidate.Steps[0].Operation.RelatedExpand.Route[0].Relationship = "edited_observations"
		updated, impact, err := analyzeConstructionCandidateWithCascade(document, candidate, firstStepID, nil, baseStages)
		if err != nil {
			t.Fatalf("analyze policy edit: %v", err)
		}
		if len(impact.RemovedStepIDs) != 0 || !equalStringSlices(constructionStepIDs(updated.Construction.Steps), constructionStepIDs(construction.Steps)) {
			t.Fatalf("policy edit removed valid dependents: steps=%#v impact=%#v", constructionStepIDs(updated.Construction.Steps), impact)
		}
	})

	t.Run("middle target resource change removes only its dependent suffix", func(t *testing.T) {
		candidate := cloneLifecycleTestConstruction(t, construction)
		middle := &candidate.Steps[1]
		middle.Operation.RelatedExpand.TargetNodeID = "medication"
		middle.Operation.RelatedExpand.TargetResourceType = "Medication"
		middle.Operation.RelatedExpand.Route[0].EdgeID = "medications"
		middle.Operation.RelatedExpand.Route[0].ToNodeID = "medication"
		middle.Operation.RelatedExpand.Route[0].ToResourceType = "Medication"
		middle.Operation.RelatedExpand.Route[0].Relationship = "medications"
		for outputIndex := range middle.Outputs {
			if middle.Outputs[outputIndex].ID == middle.Operation.RelatedExpand.RelatedRecordColumnID {
				middle.Outputs[outputIndex].Name = "related_medications"
				middle.Outputs[outputIndex].Label = "related_medications"
			}
		}
		updated, impact, err := analyzeConstructionCandidateWithCascade(document, candidate, middle.ID, nil, baseStages)
		if err != nil {
			t.Fatalf("analyze middle target edit with dependent cascade: %v", err)
		}
		if !equalStringSlices(impact.RemovedStepIDs, []string{"expand_medications"}) ||
			!equalStringSlices(constructionStepIDs(updated.Construction.Steps), []string{"expand_observations", middle.ID, "keep_root_filter"}) || impact.HasMissingInputs() {
			t.Fatalf("middle target edit result = steps=%#v impact=%#v", constructionStepIDs(updated.Construction.Steps), impact)
		}
	})
}

func cloneLifecycleTestConstruction(t *testing.T, construction authoringv2.Construction) authoringv2.Construction {
	t.Helper()
	raw, err := json.Marshal(construction)
	if err != nil {
		t.Fatal(err)
	}
	var clone authoringv2.Construction
	if err := json.Unmarshal(raw, &clone); err != nil {
		t.Fatal(err)
	}
	return clone
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
	service.config.CompileReceipt = func(_ context.Context, request CompileReceiptRequest) (*explorer.CompilationReceipt, error) {
		if request.ConstructionProposal != nil {
			candidateCompileCalls++
			candidateBinding = cloneConstructionProposalBinding(request.ConstructionProposal)
		}
		receipt := lifecycleTableShapeReceipt(snapshot, request.Workspace)
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
	proposal, err := service.ProposeConstruction(context.Background(), ConstructionProposalRequest{
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
		Commands: []authoringv2.Command{{Type: authoringv2.CommandApplyConstructionProposal, OutputID: "patients", ProposalID: proposal.ProposalID}},
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

func TestConstructionEditCascadeKeepsChangedStepWhenItsOwnInputIsMissing(t *testing.T) {
	_, store, _ := constructionProposalService(t)
	workspace, construction := seedGroupKeyEditConstruction(t, store)
	document := workspace.Documents[0]
	candidate := cloneLifecycleTestConstruction(t, construction)
	candidate.Steps[0].Operation.Group.Keys[0].InputColumnID = "missing_source_column"
	updated, impact, err := analyzeConstructionCandidateWithCascade(document, candidate, "group-records", nil, nil)
	if err != nil {
		t.Fatalf("analyze invalid GROUP edit: %v", err)
	}
	if len(impact.RemovedStepIDs) != 0 || !equalStringSlices(constructionStepIDs(updated.Construction.Steps), constructionStepIDs(construction.Steps)) {
		t.Fatalf("edit cascade removed the changed step or its descendants: steps=%#v impact=%#v", constructionStepIDs(updated.Construction.Steps), impact)
	}
	if len(impact.MissingInputs) != 1 || impact.MissingInputs[0] != (authoringv2.ConstructionDependencyIssue{StepID: "group-records", ColumnID: "missing_source_column"}) {
		t.Fatalf("missing changed-step input = %#v, want one NEEDS_REPAIR issue", impact.MissingInputs)
	}
}

func seedGroupKeyEditConstruction(t *testing.T, store *fakeStore) (authoringv2.Workspace, authoringv2.Construction) {
	t.Helper()
	workspace, err := authoringv2.DecodeWorkspace(store.created.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	document, err := authoringv2.UpgradeDocumentToConstruction(workspace.Documents[0])
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

func constructionWithoutStep(construction authoringv2.Construction, stepID string) authoringv2.Construction {
	candidate := construction
	candidate.Steps = make([]authoringv2.ConstructionStep, 0, len(construction.Steps)-1)
	for _, step := range construction.Steps {
		if step.ID != stepID {
			candidate.Steps = append(candidate.Steps, step)
		}
	}
	return candidate
}

func constructionStepIDs(steps []authoringv2.ConstructionStep) []string {
	ids := make([]string, 0, len(steps))
	for _, step := range steps {
		ids = append(ids, step.ID)
	}
	return ids
}

func equalStringSlices(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func seedRelatedExpandRemovalConstruction(t *testing.T, store *fakeStore) (authoringv2.Workspace, authoringv2.Construction) {
	t.Helper()
	workspace, err := authoringv2.DecodeWorkspace(store.created.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	document, err := authoringv2.UpgradeDocumentToConstruction(workspace.Documents[0])
	if err != nil {
		t.Fatal(err)
	}
	resources := []string{"Patient", "Observation", "Condition", "Medication"}
	nodes := []string{"patient", "observation", "condition", "medication"}
	relationships := []string{"observations", "conditions", "medications"}
	outputs := make([]authoringv2.StageColumn, 0, len(document.Columns)+len(relationships))
	for _, column := range document.Columns {
		outputs = append(outputs, authoringv2.StageColumn{ID: column.ColumnID, Name: column.Column, Label: column.Label, Type: column.LogicalType})
	}
	steps := make([]authoringv2.ConstructionStep, 0, len(relationships))
	for index, relationship := range relationships {
		stepID := "expand_" + relationship
		anchorColumnID := "_key"
		input := authoringv2.ConstructionInputRef{Kind: authoringv2.ConstructionInputSourceProjection}
		if index > 0 {
			anchorColumnID = "test_terminal_identity_" + steps[index-1].ID
			input = authoringv2.ConstructionInputRef{Kind: authoringv2.ConstructionInputStepOutput, StepID: steps[index-1].ID}
		}
		relatedRecordColumnID := "related_" + relationship
		route := []capability.ConstructionRouteStep{{
			EdgeID: relationship, FromNodeID: nodes[index], ToNodeID: nodes[index+1],
			FromResourceType: resources[index], ToResourceType: resources[index+1],
			Relationship: relationship, StorageDirection: "INBOUND", MatchMode: "OPTIONAL",
		}}
		stepOutputs := append(append([]authoringv2.StageColumn(nil), outputs...), authoringv2.StageColumn{
			ID: relatedRecordColumnID, Name: relatedRecordColumnID, Label: relatedRecordColumnID, Type: "string", Nullable: false,
		})
		steps = append(steps, authoringv2.ConstructionStep{
			ID: stepID, Inputs: []authoringv2.ConstructionInputRef{input},
			Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationRelatedExpand, RelatedExpand: &authoringv2.ConstructionRelatedExpand{
				AnchorColumnID: anchorColumnID, ChoiceID: "choice_" + stepID,
				TargetNodeID: nodes[index+1], TargetResourceType: resources[index+1], Route: route,
				ContributorRule: authoringv2.ConstructionRelatedContributorRule{Policy: authoringv2.ConstructionRelatedAllMatches},
				EmptyPolicy:     authoringv2.ConstructionExpandEmptyExclude, RelatedRecordColumnID: relatedRecordColumnID,
			}},
			Outputs: stepOutputs,
		})
		outputs = stepOutputs
	}
	filterColumnID := document.Columns[0].ColumnID
	steps = append(steps, authoringv2.ConstructionStep{
		ID: "keep_root_filter", Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputStepOutput, StepID: steps[len(steps)-1].ID}},
		Operation: authoringv2.ConstructionOperation{
			Kind:   authoringv2.ConstructionOperationFilter,
			Filter: &authoringv2.ConstructionFilter{ColumnID: filterColumnID, Operator: authoringv2.ConstructionFilterExists},
		},
		Outputs: append([]authoringv2.StageColumn(nil), outputs...),
	})
	document.Construction = &authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: steps}
	workspace.Documents[0] = document
	if err := workspace.Validate(); err != nil {
		t.Fatalf("validate related expansion chain: %v", err)
	}
	draft, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	store.created.DraftConfig = draft
	store.created.DraftDigest = digest
	return workspace, *document.Construction
}

func relatedExpandRemovalTestStages(t *testing.T, workspace authoringv2.Workspace, outputID string) []explorer.ReceiptConstructionStage {
	t.Helper()
	stages := testConstructionStageDescriptors(workspace, outputID)
	index := constructionDocumentIndex(workspace, outputID)
	if index < 0 || workspace.Documents[index].Construction == nil {
		t.Fatal("related expansion test workspace has no construction")
	}
	rootAnchor := stages[0].RelatedExpandAnchors[0]
	var active *explorer.ReceiptConstructionActiveRelatedRecord
	for stepIndex, step := range workspace.Documents[index].Construction.Steps {
		related := step.Operation.RelatedExpand
		if related == nil {
			if step.Operation.Kind != authoringv2.ConstructionOperationFilter &&
				step.Operation.Kind != authoringv2.ConstructionOperationDerive &&
				step.Operation.Kind != authoringv2.ConstructionOperationRelatedSource &&
				step.Operation.Kind != authoringv2.ConstructionOperationRelatedField {
				active = nil
			}
			if active != nil {
				stage := &stages[stepIndex+1]
				stage.ActiveRelatedRecord = active
				stage.RelatedExpandAnchors = []explorer.ReceiptConstructionRelatedExpandAnchor{
					rootAnchor,
					{AnchorColumnID: active.TerminalIdentityColumn, Kind: "activeRelatedRecord", NodeID: active.TargetNodeID, ResourceType: active.TargetResourceType, Label: "Current related " + active.TargetResourceType},
				}
			}
			continue
		}
		stage := &stages[stepIndex+1]
		anchorKind, anchorNodeID, anchorResourceType := "root", related.Route[0].FromNodeID, related.Route[0].FromResourceType
		if related.AnchorColumnID != "_key" {
			if active == nil || active.TerminalIdentityColumn != related.AnchorColumnID {
				t.Fatalf("step %q anchor %q does not match its prior active identity", step.ID, related.AnchorColumnID)
			}
			anchorKind, anchorNodeID, anchorResourceType = "activeRelatedRecord", active.TargetNodeID, active.TargetResourceType
		}
		terminalColumnID := "test_terminal_identity_" + step.ID
		stage.RelatedExpand.AnchorColumnID = related.AnchorColumnID
		stage.RelatedExpand.AnchorColumn = related.AnchorColumnID
		stage.RelatedExpand.AnchorKind = anchorKind
		stage.RelatedExpand.AnchorNodeID = anchorNodeID
		stage.RelatedExpand.AnchorResourceType = anchorResourceType
		stage.RelatedExpand.TerminalIdentityColumn = terminalColumnID
		active = &explorer.ReceiptConstructionActiveRelatedRecord{
			TargetNodeID: related.TargetNodeID, TargetResourceType: related.TargetResourceType,
			TerminalIdentityColumn: terminalColumnID,
		}
		stage.ActiveRelatedRecord = active
		stage.RelatedExpandAnchors = []explorer.ReceiptConstructionRelatedExpandAnchor{
			rootAnchor,
			{AnchorColumnID: terminalColumnID, Kind: "activeRelatedRecord", NodeID: active.TargetNodeID, ResourceType: active.TargetResourceType, Label: "Current related " + active.TargetResourceType},
		}
	}
	return stages
}
