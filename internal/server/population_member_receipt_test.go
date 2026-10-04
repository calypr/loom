package server

import (
	"context"
	"reflect"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	explorercompilation "github.com/calypr/loom/internal/explorer/compilation"
	"github.com/calypr/loom/internal/explorer/lifecycle"
)

func TestCompileExplorerReceiptBindsPopulationRemovalWithGroupedRelatedSummary(t *testing.T) {
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	snapshotBase := testAuthoringV2CapabilitySnapshot()
	snapshot := capability.NewSnapshot(snapshotBase.Identity, snapshotBase.Policy, snapshotBase.Status, snapshotBase.Complete, snapshotBase.Truncated,
		append(snapshotBase.Nodes, capability.Node{ID: "n_observation", ResourceType: "Observation"}),
		[]capability.Edge{{
			ID: "e_patient_observation", FromNodeID: "n_patient", ToNodeID: "n_observation",
			SourceResourceType: "Patient", TargetResourceType: "Observation", Label: "subject_Patient", StorageDirection: "INBOUND",
		}},
		append(snapshotBase.Candidates, capability.Candidate{
			ID: "c_patient_active", NodeID: "n_patient", ResourceType: "Patient", FieldPath: "active", Label: "Active",
			LogicalType: "boolean", Cardinality: "REQUIRED_ONE", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar},
			SupportedOperations: []capability.Operation{capability.OperationSelect}, Observed: true, Populated: true,
		}, capability.Candidate{
			ID: "c_observation_status", NodeID: "n_observation", ResourceType: "Observation", FieldPath: "status", Label: "Observation status",
			LogicalType: "string", Cardinality: "OPTIONAL_ONE", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar},
			SupportedOperations: []capability.Operation{capability.OperationSelect}, Observed: true, Populated: true,
		}), nil)

	workspace, err := authoringv2.DecodeWorkspace(baselineExplorerWorkspaceV2())
	if err != nil {
		t.Fatal(err)
	}
	document, err := authoringv2.UpgradeDocumentToConstruction(workspace.Documents[0])
	if err != nil {
		t.Fatal(err)
	}
	document.Columns[0].ColumnID = "patient-id-column"
	document.Construction = &authoringv2.Construction{
		Version: authoringv2.ConstructionVersion,
		SourceProjections: []authoringv2.ConstructionSourceProjection{{
			ColumnID: "patient-active-source", OwnerStepID: "group-active", OccurrenceID: authoringv2.RootOccurrenceID,
			FieldPath: "active", FHIRType: "boolean", LogicalType: "boolean", Label: "Active",
		}},
		Steps: []authoringv2.ConstructionStep{
			{
				ID: "group-active", Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputSourceProjection}},
				Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationGroup, Group: &authoringv2.ConstructionGroup{
					ConstructionID: "group-active",
					Keys:           []authoringv2.ConstructionGroupKey{{InputColumnID: "patient-active-source", OutputColumnID: "active-group"}},
					Aggregates:     []authoringv2.ConstructionGroupAggregate{{Operation: authoringv2.ConstructionGroupCountRows, OutputColumnID: "patient-count"}},
				}},
				Outputs: []authoringv2.StageColumn{
					{ID: "active-group", Name: "active_group", Label: "Active", Type: "boolean"},
					{ID: "patient-count", Name: "patient_count", Label: "Patient count", Type: "integer"},
				},
			},
			{
				ID: "related-summary", Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputStepOutput, StepID: "group-active"}},
				Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationRelatedSource, RelatedSource: &authoringv2.ConstructionRelatedSource{
					AnchorColumnID: "__loom_row_id", ChoiceID: "", SourceOccurrenceID: "n_observation",
					Source: authoringv2.ConstructionRelatedFieldSource{
						Kind: capability.ConstructionChoiceSourceField, CandidateID: "c_observation_status", NodeID: "n_observation",
						ResourceType: "Observation", Path: "status", Cardinality: "optional_one", LogicalType: "string",
					},
					Route: []capability.ConstructionRouteStep{{
						EdgeID: "e_patient_observation", FromNodeID: "n_patient", ToNodeID: "n_observation",
						FromResourceType: "Patient", ToResourceType: "Observation", Relationship: "subject_Patient",
						StorageDirection: "INBOUND", MatchMode: "OPTIONAL",
					}},
					ContributorRule: authoringv2.ConstructionRelatedContributorRule{Policy: authoringv2.ConstructionRelatedAllMatches},
					Form:            capability.ConstructionChoiceCount, OutputColumnID: "observation-count",
				}},
				Outputs: []authoringv2.StageColumn{
					{ID: "active-group", Name: "active_group", Label: "Active", Type: "boolean"},
					{ID: "patient-count", Name: "patient_count", Label: "Patient count", Type: "integer"},
					{ID: "observation-count", Name: "observation_count", Label: "Observation count", Type: "integer"},
				},
			},
		},
	}
	workspace.Documents[0] = document
	workspace.Documents[0].Population = &authoringv2.Population{SelectionRevisionID: "selection-candidate", Route: []authoringv2.PopulationRouteStep{}}
	catalog := authoringV2Catalog(snapshot, "custom")
	choiceRoute := []capability.ConstructionRouteStep{{
		EdgeID: "e_patient_observation", FromNodeID: "n_patient", ToNodeID: "n_observation",
		FromResourceType: "Patient", ToResourceType: "Observation", Relationship: "subject_Patient",
		StorageDirection: "INBOUND", MatchMode: "OPTIONAL",
	}}
	choice, err := capability.NewFieldConstructionChoiceForRoute(snapshot.Token, choiceRoute, snapshot.Candidates[2])
	if err != nil {
		t.Fatal(err)
	}
	workspace.Documents[0].Construction.Steps[1].Operation.RelatedSource.ChoiceID = choice.ChoiceID
	workspace, err = authoringv2.MigrateLegacyContributors(workspace, catalog)
	if err != nil {
		t.Fatal(err)
	}
	workspace = authoringv2.MigrateLosslessDefaults(workspace, catalog).NormalizePresentationOrders()

	routeChoiceID, err := capability.NewPopulationRouteChoiceID(capability.PopulationRouteChoiceIdentity{
		Version: 1, SnapshotToken: snapshot.Token, OutputID: "patients", SelectionRevisionID: "selection-candidate",
		Route: []capability.ConstructionRouteStep{},
	})
	if err != nil {
		t.Fatal(err)
	}
	intentDigest, err := workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	binding := &explorer.PopulationMemberRemovalProposalBinding{
		DraftVersion: 7, DraftDigest: "sha256:base-draft", OutputID: "patients", BaseDocumentDigest: "sha256:base-document",
		BaseSelectionRevisionID: "selection-base", BaseMembershipDigest: "sha256:base-members", BaseMemberCount: 2,
		CandidateSelectionRevisionID: "selection-candidate", CandidateMembershipDigest: "sha256:candidate-members", CandidateMemberCount: 1,
		RemovedMember: explorer.ResourceRef{Project: "project-a", Generation: snapshot.Identity.Generation, ResourceType: "Patient", ID: "patient-2"},
		RouteChoiceID: routeChoiceID, CandidateWorkspaceDigest: intentDigest, SnapshotToken: snapshot.Token, PreviewLimit: 25,
	}
	readScope := scope
	engine, err := dataframeexecution.New(dataframeexecution.Config{
		Registry:  compilerTestRegistry{},
		QueryRows: func(context.Context, string, int, map[string]any, func(map[string]any) error) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	service, err := explorer.NewService(newTestExplorerStore())
	if err != nil {
		t.Fatal(err)
	}
	request := lifecycle.CompileReceiptRequest{
		Project: "project-a", ExplorerID: "custom", Workspace: workspace, SnapshotToken: snapshot.Token,
		SelectionMembersCollection: "loom_explorer_selection_members", PopulationMemberRemovalProposal: binding,
		ResolvedInputs: explorercompilation.ResolvedInputs{Populations: []explorercompilation.ResolvedPopulation{{
			OutputID: "patients", SelectionRevisionID: "selection-candidate", MembershipDigest: binding.CandidateMembershipDigest,
			MemberCount: binding.CandidateMemberCount, ResourceType: "Patient", Route: []authoringv2.PopulationRouteStep{},
		}}},
		Authorized: lifecycle.AuthorizedCapability{Snapshot: snapshot, Scope: readScope},
	}
	receipt, err := compileExplorerReceipt(context.Background(), request, nil, engine, service, nil)
	if err != nil {
		t.Fatalf("compile grouped related-summary population proposal receipt: %v", err)
	}
	if receipt.PopulationMemberRemovalProposal == nil || !reflect.DeepEqual(*receipt.PopulationMemberRemovalProposal, *binding) || receipt.IntentDigest != intentDigest {
		t.Fatalf("receipt did not bind the exact candidate workspace and population removal: binding=%#v receipt=%#v", binding, receipt.PopulationMemberRemovalProposal)
	}
	if len(receipt.Bundle.Outputs) != 1 || receipt.Bundle.Outputs[0].Construction == nil || len(receipt.Bundle.Outputs[0].Construction.Steps) != 2 {
		t.Fatalf("compiled receipt omitted Group → related summary operations: %#v", receipt.Bundle.Outputs)
	}
	operations := receipt.Bundle.Outputs[0].Construction.Steps
	if operations[0].Operation.Kind != recipe.ConstructionGroupOp || operations[1].Operation.Kind != recipe.ConstructionRelatedSourceOp {
		t.Fatalf("compiled operations = %q then %q, want GROUP then RELATED_SOURCE", operations[0].Operation.Kind, operations[1].Operation.Kind)
	}
	stages := receipt.ConstructionStages["patients"]
	var sawGroup, sawRelated bool
	for _, stage := range stages {
		if stage.ID == "group-active" && stage.Operation == "GROUP" {
			sawGroup = true
		}
		if stage.ID == "related-summary" && stage.Operation == "RELATED_SOURCE" {
			sawRelated = true
		}
	}
	if !sawGroup || !sawRelated {
		t.Fatalf("receipt construction stages lost Group/related source: %#v", stages)
	}
	bindings := recipe.RuntimeBindings{
		Project: "project-a", SelectionProject: "project-a", DatasetGeneration: snapshot.Identity.Generation,
		AuthScopeMode: authscope.ReadScopeUnrestricted, SelectionMembersCollection: request.SelectionMembersCollection,
	}
	if _, err := compileValidatedReceiptResolution(context.Background(), engine, receipt, bindings); err != nil {
		t.Fatalf("real Group/related population proposal receipt failed re-lowering: %v", err)
	}
}
