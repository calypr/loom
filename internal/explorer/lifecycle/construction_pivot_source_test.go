package lifecycle

import (
	"strings"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
)

func TestRebuiltPivotSourceHelperDoesNotReusePresentationForChangedBinding(t *testing.T) {
	_, construction := pivotSourcePresentationFixture()
	changedSource := construction.Steps[0].Operation.RelatedField.Source
	changedSource.Path = "valueQuantity.unit"
	output := authoringv2.StageColumn{
		ID: "pivot-input", Name: constructionPivotSourceOutputName("pivot-input"),
		Label: "Observation.valueQuantity.unit", Type: "string", Nullable: true,
	}
	got := preserveExistingPivotSourceTable(construction, "pivot", construction.Steps[0].ID, changedSource, output)
	if got.Table != nil {
		t.Fatalf("changed source binding inherited stale presentation: %#v", got.Table)
	}
}

func TestConstructionCandidateWithPivotSourcesPreservesPrefixForValueLabelEdit(t *testing.T) {
	document, construction := pivotSourcePresentationFixture()
	snapshot := readySnapshot("project-a", "generation-a", "pivot-source-snapshot", authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted})
	candidateSource := capability.Candidate{
		ID: "observation-status", NodeID: "observation", ResourceType: "Observation", FieldPath: "status",
		Label: "Status", LogicalType: "string", Cardinality: "optional_one",
		ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar}, Populated: true,
	}
	snapshot.Nodes = []capability.Node{
		{ID: "patient", ResourceType: "Patient", RowRootEligible: true},
		{ID: "observation", ResourceType: "Observation"},
	}
	snapshot.Candidates = []capability.Candidate{candidateSource}
	source := authoringv2.ConstructionRelatedFieldSource{
		Kind: capability.ConstructionChoiceSourceField, CandidateID: candidateSource.ID, NodeID: candidateSource.NodeID,
		ResourceType: candidateSource.ResourceType, Path: candidateSource.FieldPath,
		Cardinality: candidateSource.Cardinality, LogicalType: candidateSource.LogicalType,
	}
	helperID := construction.Steps[0].ID
	anchorChoice, err := capability.NewConstructionRelatedFieldChoice(snapshot.Token, recipe.ConstructionSourceProjectionID, candidateSource)
	if err != nil {
		t.Fatal(err)
	}
	construction.Steps[0].Operation.RelatedField.ChoiceID = anchorChoice.ChoiceID
	construction.Steps[0].Operation.RelatedField.Source = source
	construction.Steps[0].Outputs[len(construction.Steps[0].Outputs)-1].Label = "Observation.status"
	construction.Steps[0].Outputs[len(construction.Steps[0].Outputs)-1].Table = &authoringv2.TablePresentation{Visible: pivotPresentationBool(true), Order: pivotPresentationInt(4)}
	construction.Steps[1].Outputs[1].Label = "Observation.status"
	document.Construction = &construction

	active := &explorer.ReceiptConstructionActiveRelatedRecord{
		TargetNodeID: "observation", TargetResourceType: "Observation", TerminalIdentityColumn: "observation_id",
	}
	sourceStageColumns := []explorer.ReceiptConstructionStageColumn{
		{ID: "specimen", Name: "specimen_id", Label: "Specimen", Type: "string"},
		{ID: "category", Name: "category", Label: "Category", Type: "string"},
		{ID: "value", Name: "value", Label: "Value", Type: "string"},
	}
	stages := []explorer.ReceiptConstructionStage{
		{ID: recipe.ConstructionSourceProjectionID, Columns: sourceStageColumns, ActiveRelatedRecord: active},
		{ID: helperID, Columns: []explorer.ReceiptConstructionStageColumn{
			{ID: "specimen", Name: "specimen_id", Label: "Specimen", Type: "string"},
			{ID: "category", Name: "category", Label: "Category", Type: "string"},
			{ID: "value", Name: "value", Label: "Value", Type: "string"},
			{ID: "pivot-input", Name: constructionPivotSourceOutputName("pivot-input"), Label: "Observation.status", Type: "string"},
		}, ActiveRelatedRecord: active},
	}
	base := constructionBase{
		document: document, construction: construction, snapshot: snapshot,
		authorized: AuthorizedCapability{Snapshot: snapshot, Scope: authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}},
		stages:     stages,
	}
	selection, err := capability.NewConstructionRelatedFieldChoice(snapshot.Token, helperID, candidateSource)
	if err != nil {
		t.Fatal(err)
	}
	candidate := construction
	candidate.Steps = append([]authoringv2.ConstructionStep(nil), construction.Steps...)
	candidate.Steps[1].Outputs = append([]authoringv2.StageColumn(nil), construction.Steps[1].Outputs...)
	candidate.Steps[1].Outputs[len(candidate.Steps[1].Outputs)-1].Label = "Observed quantity d"
	request := ConstructionProposalRequest{
		Project: "project-a", ExplorerID: "patients", OutputID: "patients", ChangedStepID: "pivot",
		PivotSources: []ConstructionPivotSourceSelection{{ChoiceID: selection.ChoiceID, ColumnID: "pivot-input"}},
	}
	service := &Service{}
	prepared, _, err := service.constructionCandidateWithPivotSources(t.Context(), base, request, candidate)
	if err != nil {
		t.Fatalf("prepare related Pivot sources: %v", err)
	}
	gotOutput := prepared.Steps[0].Outputs[len(prepared.Steps[0].Outputs)-1]
	savedOutput := construction.Steps[0].Outputs[len(construction.Steps[0].Outputs)-1]
	if gotOutput.Table == nil || gotOutput.Table.Order == nil || *gotOutput.Table.Order != 4 {
		t.Fatalf("prepared helper output lost saved table presentation: %#v", gotOutput)
	}
	if gotOutput.Table == savedOutput.Table || gotOutput.Table.Visible == savedOutput.Table.Visible || gotOutput.Table.Order == savedOutput.Table.Order {
		t.Fatal("prepared helper table presentation aliases the accepted helper's pointers")
	}
	*gotOutput.Table.Order = 9
	if *savedOutput.Table.Order != 4 {
		t.Fatalf("mutating prepared presentation changed accepted prefix order to %d", *savedOutput.Table.Order)
	}
	*gotOutput.Table.Order = 4
	if _, _, err := document.AnalyzeConstructionCandidate(prepared, "pivot", nil); err != nil {
		t.Fatalf("analyze prepared Pivot label edit after source-helper regeneration: %v", err)
	}
}

func pivotSourcePresentationFixture() (authoringv2.Document, authoringv2.Construction) {
	visible := true
	order := 4
	source := authoringv2.ConstructionRelatedFieldSource{
		Kind: capability.ConstructionChoiceSourceField, CandidateID: "obs-value", NodeID: "observation",
		ResourceType: "Observation", Path: "valueQuantity.value", Cardinality: "optional_one", LogicalType: "string",
	}
	output := authoringv2.StageColumn{
		ID: "pivot-input", Name: constructionPivotSourceOutputName("pivot-input"), Label: "Observation.valueQuantity.value",
		Type: "string", Nullable: true, Table: &authoringv2.TablePresentation{Visible: &visible, Order: &order},
	}
	helperID := constructionPivotSourceHelperID("pivot", output.ID)
	construction := authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{
		{
			ID: helperID, OwnerStepID: "pivot", Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputSourceProjection}},
			Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationRelatedField, RelatedField: &authoringv2.ConstructionRelatedField{
				ChoiceID: "saved-choice", Source: source, OutputColumnID: output.ID,
			}},
			Outputs: []authoringv2.StageColumn{
				{ID: "specimen", Name: "specimen_id", Label: "Specimen", Type: "string"},
				{ID: "category", Name: "category", Label: "Category", Type: "string"},
				{ID: "value", Name: "value", Label: "Value", Type: "string"},
				output,
			},
		},
		{
			ID: "pivot", Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputStepOutput, StepID: helperID}},
			Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationPivot, Pivot: &authoringv2.ConstructionPivot{
				ConstructionID: "pivot", GroupKeyIDs: []string{"specimen", "pivot-input"},
				CategoryColumnID: "category", ValueColumnID: "value",
				Categories:             []authoringv2.ConstructionPivotCategory{{Key: lifecycleTestStringScalar("A"), OutputColumnID: "d"}},
				DuplicatePolicy:        authoringv2.ConstructionPivotDuplicateError,
				MissingCellPolicy:      authoringv2.ConstructionPivotMissingNull,
				UnlistedCategoryPolicy: authoringv2.ConstructionPivotUnlistedError,
			}},
			Outputs: []authoringv2.StageColumn{
				{ID: "specimen", Name: "specimen_id", Label: "Specimen", Type: "string"},
				output,
				{ID: "d", Name: "d", Label: "d", Type: "string"},
			},
		},
	}}
	document := authoringv2.Document{
		Kind: authoringv2.Kind, Output: authoringv2.Output{ID: "patients", Title: "Patients"},
		RootResourceType: "Patient", Route: authoringv2.RouteNode{OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "Patient"},
		Rows: authoringv2.RecordsRowDefinition(),
		Columns: []authoringv2.Column{
			{ColumnID: "specimen", Column: "specimen_id", Label: "Specimen", LogicalType: "string", OccurrenceID: authoringv2.RootOccurrenceID, Source: authoringv2.ColumnSource{Kind: authoringv2.SourceProjectID}},
			{ColumnID: "category", Column: "category", Label: "Category", LogicalType: "string", OccurrenceID: authoringv2.RootOccurrenceID, Source: authoringv2.ColumnSource{Kind: authoringv2.SourceProjectID}},
			{ColumnID: "value", Column: "value", Label: "Value", LogicalType: "string", OccurrenceID: authoringv2.RootOccurrenceID, Source: authoringv2.ColumnSource{Kind: authoringv2.SourceProjectID}},
		},
		Construction: &construction,
	}
	return document, construction
}

func lifecycleTestStringScalar(value string) authoringv2.TableScalar {
	return authoringv2.TableScalar{Kind: authoringv2.TableScalarString, String: &value}
}

func pivotPresentationBool(value bool) *bool { return &value }

func pivotPresentationInt(value int) *int { return &value }

func TestPivotSourcePresentationFailureIsPrefixScopeRejection(t *testing.T) {
	document, construction := pivotSourcePresentationFixture()
	candidate := construction
	candidate.Steps = append([]authoringv2.ConstructionStep(nil), construction.Steps...)
	candidate.Steps[0].Outputs = append([]authoringv2.StageColumn(nil), construction.Steps[0].Outputs...)
	candidate.Steps[0].Outputs[len(candidate.Steps[0].Outputs)-1].Table = nil
	candidate.Steps[1].Outputs = append([]authoringv2.StageColumn(nil), construction.Steps[1].Outputs...)
	candidate.Steps[1].Outputs[len(candidate.Steps[1].Outputs)-1].Label = "Observed quantity d"
	_, _, err := document.AnalyzeConstructionCandidate(candidate, "pivot", nil)
	if err == nil || !strings.Contains(err.Error(), "candidate changes step") || !strings.Contains(err.Error(), construction.Steps[0].ID) {
		t.Fatalf("broken regenerated prefix error = %v, want prefix-scope rejection for %q", err, construction.Steps[0].ID)
	}
}
