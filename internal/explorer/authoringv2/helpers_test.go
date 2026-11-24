package authoringv2

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/columntransform"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/workspace"
	ws "github.com/calypr/loom/internal/explorer/workspace"
	fhirbinding "github.com/calypr/loom/internal/fhir/binding"
)

func rowValueGroupedDocument() ws.Document {
	document := workspaceDocument("patients")
	document.Columns = []ws.Column{constructionSourceColumn("status", "status", "Status", "string")}
	document.Construction = &ws.Construction{Version: ws.ConstructionVersion, Steps: []ws.ConstructionStep{{
		ID: "group_status", Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputSourceProjection}},
		Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationGroup, Group: &ws.ConstructionGroup{
			ConstructionID: "group_status", Keys: []ws.ConstructionGroupKey{{InputColumnID: "status", OutputColumnID: "status_group"}},
			Aggregates: []ws.ConstructionGroupAggregate{{Operation: ws.ConstructionGroupCountRows, OutputColumnID: "records"}},
		}}, Outputs: []ws.StageColumn{{ID: "status_group", Name: "status_group", Label: "Status", Type: "string"}, {ID: "records", Name: "records", Label: "Records", Type: "integer"}},
	}}}
	return document
}

func TestPopulateGroupedRowsPreservesIdentityReloadAndRemoval(t *testing.T) {
	document := rowValueGroupedDocument()
	keys := append([]ws.ConstructionGroupKey(nil), document.Construction.Steps[0].Operation.Group.Keys...)
	column := constructionSourceColumn("gender_source", "gender", "Gender", "string")
	document.Columns = append(document.Columns, column)
	if err := populateConstructionColumn(&document, column, ws.ConstructionRowValueAll); err != nil {
		t.Fatal(err)
	}
	if err := document.Validate(); err != nil {
		t.Fatal(err)
	}
	step := document.Construction.Steps[0]
	if !reflect.DeepEqual(step.Operation.Group.Keys, keys) || len(step.RowValues) != 1 || step.Outputs[2].Type != "array" {
		t.Fatalf("column changed grouping or omitted its value binding: %#v", step)
	}
	raw, err := constructionWorkspace(document).CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := ws.DecodeWorkspace(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(reloaded.Documents[0].Construction.Steps[0].RowValues, step.RowValues) {
		t.Fatal("reload lost contributing-record value policy")
	}
	removed, _, err := ApplyCommands(reloaded, commandCatalog(), "remove-gender", []Command{{Type: CommandRemoveColumn, OutputID: "patients", Payload: &ColumnPayload{Column: "gender"}}})
	if err != nil {
		t.Fatal(err)
	}
	remaining := removed.Documents[0].Construction.Steps[0]
	if len(remaining.RowValues) != 0 || len(remaining.Outputs) != 2 || !reflect.DeepEqual(remaining.Operation.Group.Keys, keys) {
		t.Fatal("removing a column changed row grouping")
	}
}

func TestRemoveProjectedFieldUsesStableIDAndExistingDownstreamCleanup(t *testing.T) {
	document := rowValueGroupedDocument()
	column := constructionSourceColumn("gender_source", "gender", "Gender", "string")
	document.Columns = append(document.Columns, column)
	if err := populateConstructionColumn(&document, column, ws.ConstructionRowValueAll); err != nil {
		t.Fatal(err)
	}
	workspace := constructionWorkspace(document)
	removed, _, err := ApplyCommands(workspace, commandCatalog(), "remove-projected-gender", []Command{{Type: CommandRemoveProjectedField, OutputID: "patients", Payload: &ColumnPayload{ColumnID: "gender_source"}}})
	if err != nil {
		t.Fatal(err)
	}
	result := removed.Documents[0]
	if len(result.Columns) != 1 || result.Columns[0].ColumnID != "status" {
		t.Fatalf("stable-ID removal left the wrong source columns: %#v", result.Columns)
	}
	if len(result.Construction.Steps[0].RowValues) != 0 || len(result.Construction.Steps[0].Outputs) != 2 {
		t.Fatalf("removal did not clean dependent row-value outputs: %#v", result.Construction.Steps[0])
	}
	if result.Construction.Steps[0].Operation.Group.Keys[0].InputColumnID != "status" {
		t.Fatal("removing a projected field changed the unrelated grouping key")
	}
}

func TestRemoveProjectedFieldRejectsClientOwnershipClaims(t *testing.T) {
	var command Command
	if err := json.Unmarshal([]byte(`{"type":"REMOVE_PROJECTED_FIELD","outputId":"patients","columnId":"column-a","owner":"PINNED_SOURCE"}`), &command); err == nil {
		t.Fatal("accepted client-supplied projected field ownership")
	}
}

func TestPopulatePivotRowsCarriesValuesThroughFollowingFilter(t *testing.T) {
	document := stagedConstructionDocument()
	document.Construction.Steps = document.Construction.Steps[:3]
	column := constructionSourceColumn("extra", "extra", "Extra", "string")
	document.Columns = append(document.Columns, column)
	if err := populateConstructionColumn(&document, column, ws.ConstructionRowValueOne); err != nil {
		t.Fatal(err)
	}
	if err := document.Validate(); err != nil {
		t.Fatal(err)
	}
	rowValue := document.Construction.Steps[0].RowValues[0]
	if rowValue.Policy != ws.ConstructionRowValueOne {
		t.Fatal("ONE policy was not saved")
	}
	for _, step := range document.Construction.Steps {
		if _, exists := stageColumnByID(step.Outputs, rowValue.OutputColumnID); !exists {
			t.Fatalf("step %s dropped the new column", step.ID)
		}
	}
}

func TestExplicitGroupsCreateAnchoredEffectiveConstructionStage(t *testing.T) {
	document := rowValueGroupedDocument()
	document.Rows = ws.RowDefinition{Kind: ws.RowDefinitionGroups, Groups: &ws.GroupedRows{
		Source: ws.GroupSource{Kind: ws.GroupSourceExplicit, Explicit: &ws.ExplicitGroupSource{
			RevisionID: "group-revision", UnassignedMemberPolicy: ws.UnassignedMemberExclude,
		}},
		AfterStepID: "filter_status",
	}}
	filterSource := ws.ConstructionStep{
		ID: "filter_status", Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputSourceProjection}},
		Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationFilter, Filter: &ws.ConstructionFilter{ColumnID: "status", Operator: ws.ConstructionFilterExists}},
		Outputs:   []ws.StageColumn{{ID: "status", Name: "status", Label: "Status", Type: "string"}},
	}
	filterCohorts := ws.ConstructionStep{
		ID: "filter_cohorts", Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputStepOutput, StepID: "group_rows"}},
		Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationFilter, Filter: &ws.ConstructionFilter{ColumnID: "group_id", Operator: ws.ConstructionFilterExists}},
		Outputs: []ws.StageColumn{
			{ID: "group_id", Name: "group_id", Label: "Group ID", Type: "string"},
			{ID: "group_label", Name: "group_label", Label: "Group label", Type: "string"},
			{ID: "group_ordinal", Name: "group_ordinal", Label: "Group ordinal", Type: "integer"},
			{ID: "members", Name: "members", Label: "Members", Type: "array"},
		},
	}
	document.Construction.Steps = []ws.ConstructionStep{filterSource, filterCohorts}
	if err := document.Validate(); err != nil {
		t.Fatalf("valid source → cohort → authored filter sequence: %v", err)
	}
	removed, _, err := document.ProposeStepRemoval("filter_status", nil)
	if err != nil {
		t.Fatalf("remove the persisted cohort anchor: %v", err)
	}
	if removed.Rows.Groups.AfterStepID != "" || len(removed.Construction.Steps) != 1 ||
		removed.Construction.Steps[0].Inputs[0] != (ws.ConstructionInputRef{Kind: ws.ConstructionInputStepOutput, StepID: "group_rows"}) {
		t.Fatalf("anchor removal did not move the virtual stage to source projection: %#v", removed)
	}
	if document.Rows.Groups.AfterStepID != "filter_status" {
		t.Fatal("proposed anchor repair mutated the accepted document")
	}
	if err := removed.Validate(); err != nil {
		t.Fatalf("reanchored cohort sequence is invalid: %v", err)
	}
	withoutCohortSuffix, _, err := document.ProposeStepRemoval("filter_cohorts", nil)
	if err != nil {
		t.Fatalf("explicitly remove the post-cohort dependent stage: %v", err)
	}
	withoutCohortSuffix.Rows = ws.RecordsRowDefinition()
	if err := withoutCohortSuffix.Validate(); err != nil {
		t.Fatalf("return to records after removing the virtual-stage dependent: %v", err)
	}
	if withoutCohortSuffix.Construction.Steps[0].Inputs[0].StepID == "group_rows" {
		t.Fatal("returning to records left a virtual group_rows reference")
	}

	wrongBoundary := document
	wrongBoundary.Construction = &ws.Construction{Version: ws.ConstructionVersion, Steps: append([]ws.ConstructionStep(nil), document.Construction.Steps...)}
	wrongBoundary.Construction.Steps[1].Inputs = []ws.ConstructionInputRef{{Kind: ws.ConstructionInputStepOutput, StepID: "filter_status"}}
	if err := wrongBoundary.Validate(); err == nil {
		t.Fatal("post-cohort operation accepted the pre-cohort authored step as its input")
	}
}

func TestNormalizeConstructionOutputsUsesAnchoredCohortSchema(t *testing.T) {
	document := workspaceDocument("patients")
	document.Columns = []ws.Column{constructionSourceColumn("status", "status", "Status", "string")}
	document.Columns[0].Source = ws.ColumnSource{Kind: ws.SourceField, Field: &ws.FieldSource{Path: "status", ProjectionMode: "VALUE"}}
	document.Rows = ws.RowDefinition{Kind: ws.RowDefinitionGroups, Groups: &ws.GroupedRows{
		Source: ws.GroupSource{Kind: ws.GroupSourceExplicit, Explicit: &ws.ExplicitGroupSource{
			RevisionID: "group-revision", UnassignedMemberPolicy: ws.UnassignedMemberExclude,
		}},
		AfterStepID: "filter_status",
		RowValues:   []ws.ExplicitGroupRowValue{{ColumnID: "status", Policy: ws.ConstructionRowValueAll}},
	}}
	filterSource := ws.ConstructionStep{
		ID: "filter_status", Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputSourceProjection}},
		Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationFilter, Filter: &ws.ConstructionFilter{ColumnID: "status", Operator: ws.ConstructionFilterExists}},
		Outputs:   []ws.StageColumn{{ID: "status", Name: "status", Label: "Status", Type: "string"}},
	}
	document.Construction = &ws.Construction{Version: ws.ConstructionVersion}
	document.Construction.Steps = []ws.ConstructionStep{filterSource}
	if err := document.Validate(); err != nil {
		t.Fatalf("valid source-stage group row setup: %v", err)
	}
	filterCohorts := ws.ConstructionStep{
		ID: "filter_cohorts", Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputStepOutput, StepID: "group_rows"}},
		Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationFilter, Filter: &ws.ConstructionFilter{ColumnID: "group_label", Operator: ws.ConstructionFilterExists}},
		Outputs: []ws.StageColumn{
			{ID: "group_label", Name: "group_label", Label: "Group label", Type: "string"},
			{ID: "group_ordinal", Name: "group_ordinal", Label: "Group ordinal", Type: "integer"},
			{ID: "members", Name: "members", Label: "Members", Type: "array"},
			{ID: "status", Name: "status", Label: "Status", Type: "array"},
		},
	}
	candidate := *document.Construction
	candidate.Steps = append(append([]ws.ConstructionStep(nil), candidate.Steps...), filterCohorts)
	normalized, _, err := document.AnalyzeConstructionCandidate(candidate, "filter_cohorts", nil)
	if err != nil {
		t.Fatalf("rebuild cohort filter through construction candidate API: %v", err)
	}
	outputs := normalized.Construction.Steps[1].Outputs
	want := []string{"group_id", "group_label", "group_ordinal", "members", "status"}
	if len(outputs) != len(want) {
		t.Fatalf("normalized cohort filter outputs = %#v, want five virtual-stage columns", outputs)
	}
	for index, id := range want {
		if outputs[index].ID != id {
			t.Fatalf("normalized cohort filter output %d = %#v, want column %q", index, outputs[index], id)
		}
	}
	if err := normalized.Validate(); err != nil {
		t.Fatalf("anchored virtual cohort schema should remain valid after normalization: %v", err)
	}
}

func TestExplicitGroupStageAcceptsExactRecodedRootStringRowValue(t *testing.T) {
	transformation := columntransform.ValueTransformation{
		Kind: columntransform.KindExactCategoryRecode,
		ExactCategoryRecode: &columntransform.ExactCategoryRecode{
			Mappings:      []columntransform.CategoryMapping{{From: "recorded", To: "shared"}},
			UnknownPolicy: columntransform.UnknownKeepOriginal,
		},
	}
	column := constructionSourceColumn("status-id", "status", "Status", "string")
	column.Source = ws.ColumnSource{Kind: ws.SourceField, Field: &ws.FieldSource{Path: "status", ProjectionMode: "VALUE"}}
	column.ValueTransformation = &transformation
	document := workspaceDocument("patients")
	document.Columns = []ws.Column{column}
	document.Rows = ws.RowDefinition{Kind: ws.RowDefinitionGroups, Groups: &ws.GroupedRows{
		Source: ws.GroupSource{Kind: ws.GroupSourceExplicit, Explicit: &ws.ExplicitGroupSource{
			RevisionID: "group-revision", UnassignedMemberPolicy: ws.UnassignedMemberExclude,
		}},
		RowValues: []ws.ExplicitGroupRowValue{{ColumnID: column.ColumnID, Policy: ws.ConstructionRowValueOne}},
	}}
	document.Construction = &ws.Construction{Version: ws.ConstructionVersion}
	if err := document.Validate(); err != nil {
		t.Fatalf("validate transformed exact-category group row value: %v", err)
	}
}

func TestCompoundSourceGroupingOwnsAndPrunesItsInputs(t *testing.T) {
	document := rowValueGroupedDocument()
	document.Columns = nil
	group := document.Construction.Steps[0].Operation.Group
	group.Keys = []ws.ConstructionGroupKey{{InputColumnID: "active_source", OutputColumnID: "active_group"}, {InputColumnID: "gender_source", OutputColumnID: "gender_group"}}
	document.Construction.SourceProjections = []ws.ConstructionSourceProjection{
		{ColumnID: "active_source", OwnerStepID: "group_status", OccurrenceID: ws.RootOccurrenceID, FieldPath: "active", FHIRType: "boolean", LogicalType: "boolean", Label: "Active"},
		{ColumnID: "gender_source", OwnerStepID: "group_status", OccurrenceID: ws.RootOccurrenceID, FieldPath: "gender", FHIRType: "code", LogicalType: "string", Label: "Gender"},
	}
	document.Construction.Steps[0].Outputs = []ws.StageColumn{{ID: "active_group", Name: "active", Label: "Active", Type: "boolean"}, {ID: "gender_group", Name: "gender", Label: "Gender", Type: "string"}, {ID: "records", Name: "records", Label: "Records", Type: "integer"}}
	if err := document.Validate(); err != nil {
		t.Fatal(err)
	}
	removed, _, err := document.AnalyzeConstructionCandidate(ws.Construction{Version: ws.ConstructionVersion}, "", []string{"group_status"})
	if err != nil {
		t.Fatal(err)
	}
	if len(removed.Construction.SourceProjections) != 0 {
		t.Fatal("removed grouping left source inputs behind")
	}
}

func TestNextTableOrderIncludesGeneratedGroupColumns(t *testing.T) {
	document := rowValueGroupedDocument()
	if got := nextTableOrder(document); got != 2 {
		t.Fatalf("new field order = %d, want 2 after the group key and count", got)
	}
	order := 7
	document.Construction.Steps[0].Outputs[1].Table = &ws.TablePresentation{Order: &order}
	if got := nextTableOrder(document); got != 8 {
		t.Fatalf("new field order = %d, want 8 after the explicitly ordered count", got)
	}
}

type storedTableShape = workspace.StoredTableShape

type storedTableReshape = workspace.StoredTableReshape

type storedPivotConstruction = workspace.StoredPivotConstruction

type storedPivotCategory = workspace.StoredPivotCategory

type storedUnpivotConstruction = workspace.StoredUnpivotConstruction

type storedUnpivotInput = workspace.StoredUnpivotInput

type storedDerivedConstruction = workspace.StoredDerivedConstruction

type storedArithmeticOperand = workspace.StoredArithmeticOperand

const (
	tableShapeSemanticsVersion              = 9
	explicitRowsSemanticsVersion            = 8
	contributorSemanticsVersion             = 4
	aggregateTemporalPolicySemanticsVersion = 9
)

func stageColumnByID(columns []ws.StageColumn, id string) (ws.StageColumn, bool) {
	for _, column := range columns {
		if column.ID == id {
			return column, true
		}
	}
	return ws.StageColumn{}, false
}

func constructionStageIDs(columns []ws.StageColumn) []string {
	ids := make([]string, len(columns))
	for index, column := range columns {
		ids[index] = column.ID
	}
	return ids
}

func cloneDocumentFixtureByJSON(document ws.Document) ws.Document {
	raw, err := json.Marshal(document)
	if err != nil {
		panic(err)
	}
	var cloned ws.Document
	if err := json.Unmarshal(raw, &cloned); err != nil {
		panic(err)
	}
	return cloned
}

func testCatalog() ws.CatalogSnapshot {
	nodes := []ws.CatalogNode{
		{ID: "patient", ResourceType: "Patient", RowRootEligible: true},
		{ID: "encounter", ResourceType: "Encounter", RowRootEligible: true},
	}
	edges := []ws.CatalogEdge{
		{ID: "patient-encounter", FromNodeID: "patient", ToNodeID: "encounter", Label: "encounters"},
		{ID: "encounter-self", FromNodeID: "encounter", ToNodeID: "encounter", Label: "revisits"},
	}
	return ws.CatalogSnapshot{
		APIVersion: ws.APIVersion, Kind: ws.CatalogKind, Project: "p", ExplorerID: "e",
		SourceGeneration: "g", AuthorizationScopeDigest: "scope", SnapshotToken: "sha256:snapshot",
		Complete: true, Nodes: nodes, Edges: edges,
		Candidates: []ws.CatalogCandidate{
			{ID: "patient-id", NodeID: "patient", FieldPath: "id", Cardinality: "optional_one", Label: "ID", LogicalType: "string", ProjectionModes: []string{"VALUE"}, DefaultProjectionMode: "VALUE", ConstructionChoice: testFieldConstructionChoice("sha256:snapshot", "patient-id", "patient", "Patient", "id", "optional_one", capability.ProjectionScalar)},
			{ID: "encounter-id", NodeID: "encounter", FieldPath: "id", Cardinality: "optional_one", Label: "ID", LogicalType: "string", ProjectionModes: []string{"VALUE"}, DefaultProjectionMode: "VALUE", ConstructionChoice: testFieldConstructionChoice("sha256:snapshot", "encounter-id", "encounter", "Encounter", "id", "optional_one", capability.ProjectionScalar)},
		},
		RoutePolicy: ws.RoutePolicy{Unbounded: true, AllowRepeatedEdges: true, AllowSelfLoops: true},
	}
}

func testFieldConstructionChoice(snapshotToken, candidateID, nodeID, resourceType, path, cardinality string, projection capability.ProjectionMode) *capability.ConstructionChoice {
	choice, err := capability.NewFieldConstructionChoice(snapshotToken, capability.Candidate{
		ID: candidateID, NodeID: nodeID, ResourceType: resourceType, FieldPath: path,
		Cardinality: cardinality, LogicalType: "string", ProjectionModes: []capability.ProjectionMode{projection},
	})
	if err != nil {
		panic(err)
	}
	return &choice
}

func migrateStoredDocumentFixture(document ws.Document) (ws.Document, error) {
	if document.ArchiveRetiredShape == nil {
		return ws.StartDocumentConstruction(document)
	}
	raw, err := json.Marshal(constructionWorkspace(document))
	if err != nil {
		return ws.Document{}, err
	}
	result, err := ws.MigrateStoredTableShapeWorkspace(raw, ws.CatalogSnapshot{})
	if err != nil {
		return ws.Document{}, err
	}
	return result.Workspace.Documents[0], nil
}

func cloneStoredShapeFixture(shape *storedTableShape) (*storedTableShape, error) {
	raw, err := json.Marshal(shape)
	if err != nil {
		return nil, err
	}
	var out *storedTableShape
	err = json.Unmarshal(raw, &out)
	return out, err
}

func validCorrelatedLookup() *ws.LookupSource {
	return &ws.LookupSource{
		Binding: &fhirbinding.CorrelatedBinding{OwnerPath: "component[]", KeyPath: "component[].code.coding[]", SystemPath: "system", CodePath: "code", ValuePath: "valueQuantity.value", LogicalType: "decimal"},
		Key:     &fhirbinding.CorrelatedKey{System: "urn:study:A", Code: "shared"},
	}
}

func dependencyWorkspace(documents ...ws.Document) ws.Workspace {
	w := ws.Workspace{APIVersion: ws.APIVersion, Kind: ws.WorkspaceKind, Explorer: ws.ExplorerMetadata{Title: "Dependencies"}, Documents: documents}
	for index, document := range documents {
		w.Tabs = append(w.Tabs, ws.Tab{ID: "tab_" + document.Output.ID, Title: document.Output.Title, OutputID: document.Output.ID, Order: index, Visible: true})
	}
	return w
}

func workspaceOutputCombineDocument(id string, workspaceOutputID string) ws.Document {
	document := terminalCombineDocument()
	document.Output.ID = id
	document.Output.Title = id
	document.Construction.Steps[0].Inputs[0] = ws.ConstructionInputRef{Kind: ws.ConstructionInputWorkspaceOutput, OutputID: workspaceOutputID}
	return document
}

func terminalCombineDocument() ws.Document {
	document := workspaceDocument("training")
	document.Columns = nil
	document.Construction = &ws.Construction{Version: ws.ConstructionVersion, Steps: []ws.ConstructionStep{{
		ID: "join_labs",
		Inputs: []ws.ConstructionInputRef{
			{Kind: ws.ConstructionInputTableRevision, TableID: "project:1:patients", RevisionID: "patients-r4", OutputID: "patients"},
			{Kind: ws.ConstructionInputTableRevision, TableID: "project:1:labs", RevisionID: "labs-r9", OutputID: "labs"},
		},
		Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationCombine, Combine: &ws.ConstructionCombine{
			Kind: ws.ConstructionCombineKeyJoin,
			Keys: []ws.ConstructionCombineKey{{LeftColumnID: "patient_key", RightColumnID: "subject_key"}},
			Projections: []ws.ConstructionCombineProjection{
				{OutputColumnID: "patient_id", InputIndex: 0, InputColumnID: "patient_key"},
				{OutputColumnID: "lab_value", InputIndex: 1, InputColumnID: "result_value"},
			},
			JoinType: ws.ConstructionCombineLeftJoin, RightMatchPolicy: ws.ConstructionCombinePreserveAllMatches,
		}},
		Outputs: []ws.StageColumn{
			{ID: "patient_id", Name: "patient_id", Label: "Patient ID", Type: "integer"},
			{ID: "lab_value", Name: "lab_value", Label: "Lab value", Type: "decimal", Nullable: true},
		},
	}}}
	return document
}

func TestApplyConstructionProposalStrictlyAppliesServerResolvedCandidate(t *testing.T) {
	legacy := legacyPivotDocument()
	base := legacy
	base.ArchiveRetiredShape = nil
	workspace := constructionWorkspace(base)
	original, err := cloneWorkspace(workspace)
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := migrateStoredDocumentFixture(legacy)
	if err != nil {
		t.Fatalf("migrate proposal base: %v", err)
	}
	proposal := Command{Type: CommandApplyConstructionProposal, OutputID: "patients", Payload: &ProposalPayload{ProposalID: "receipt-1"}}
	if err := proposal.ResolveConstructionProposal(&candidate); err != nil {
		t.Fatalf("resolve proposal: %v", err)
	}
	candidate.Construction.Steps[0].Operation.Pivot.Categories[0].OutputColumnID = "mutated_after_resolution"

	wire, err := json.Marshal(proposal)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(wire), "construction") || strings.Contains(string(wire), "legacy_pivot") {
		t.Fatalf("resolved construction leaked into browser command: %s", wire)
	}

	request := ApplyCommandsRequest{
		CommandID: "command-1", SemanticsVersion: ws.CurrentSemanticsVersion, SnapshotToken: "snapshot-1",
		ExpectedDraftVersion: 3, ExpectedDraftDigest: "sha256:draft", Commands: []Command{proposal},
	}
	if err := request.Validate(); err != nil {
		t.Fatalf("valid proposal request rejected: %v", err)
	}
	got, results, err := ApplyCommands(workspace, commandCatalog(), request.CommandID, request.Commands)
	if err != nil {
		t.Fatalf("apply construction proposal: %v", err)
	}
	want, err := migrateStoredDocumentFixture(legacy)
	if err != nil {
		t.Fatal(err)
	}
	wantWorkspace := constructionWorkspace(want).NormalizePresentationOrders()
	want = wantWorkspace.Documents[0]
	if !reflect.DeepEqual(got.Documents[0], want) {
		t.Fatalf("applied document differs from server candidate\n got: %#v\nwant: %#v", got.Documents[0], want)
	}
	if len(results) != 1 || results[0].Type != CommandResultTableChanged || results[0].OutputID != "patients" {
		t.Fatalf("results = %#v", results)
	}
	if !reflect.DeepEqual(workspace, original) {
		t.Fatal("proposal reducer mutated accepted workspace")
	}
}

func TestApplyConstructionProposalRejectsClientPlanAndUnresolvedApply(t *testing.T) {
	for _, raw := range []string{
		`{"type":"APPLY_CONSTRUCTION_PROPOSAL","outputId":"patients","proposalId":"receipt-1","construction":{"version":1,"steps":[]}}`,
		`{"type":"APPLY_CONSTRUCTION_PROPOSAL","outputId":"patients","proposalId":"receipt-1","removeStepIds":["step-1"]}`,
	} {
		var decoded Command
		if err := json.Unmarshal([]byte(raw), &decoded); err == nil {
			t.Fatalf("proposal accepted client candidate fields: %s", raw)
		}
	}

	command := Command{Type: CommandApplyConstructionProposal, OutputID: "patients", Payload: &ProposalPayload{ProposalID: "receipt-1"}}
	if err := command.validate(); err != nil {
		t.Fatalf("closed proposal command invalid: %v", err)
	}
	base := legacyPivotDocument()
	base.ArchiveRetiredShape = nil
	workspace := constructionWorkspace(base)
	if _, _, err := ApplyCommands(workspace, commandCatalog(), "command-1", []Command{command}); err == nil || !strings.Contains(err.Error(), "no lifecycle-resolved construction") {
		t.Fatalf("unresolved apply error = %v", err)
	}
	if err := command.ResolveConstructionProposal(nil); err == nil {
		t.Fatal("nil resolved candidate was accepted")
	}
}

func TestApplyConstructionProposalCarriesValidatedCohortAnchorRepair(t *testing.T) {
	document := rowValueGroupedDocument()
	document.Columns[0].Source = ws.ColumnSource{Kind: ws.SourceField, Field: &ws.FieldSource{Path: "status", ProjectionMode: "VALUE"}}
	document.Rows = ws.RowDefinition{Kind: ws.RowDefinitionGroups, Groups: &ws.GroupedRows{
		Source: ws.GroupSource{Kind: ws.GroupSourceExplicit, Explicit: &ws.ExplicitGroupSource{
			RevisionID: "group-revision", UnassignedMemberPolicy: ws.UnassignedMemberExclude,
		}},
		AfterStepID: "source_filter",
		RowValues:   []ws.ExplicitGroupRowValue{{ColumnID: "status", Policy: ws.ConstructionRowValueAll}},
	}}
	document.Construction.Steps = []ws.ConstructionStep{
		{
			ID: "source_filter", Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputSourceProjection}},
			Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationFilter, Filter: &ws.ConstructionFilter{ColumnID: "status", Operator: ws.ConstructionFilterExists}},
			Outputs:   []ws.StageColumn{{ID: "status", Name: "status", Label: "Status", Type: "string"}},
		},
		{
			ID: "cohort_filter", Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputStepOutput, StepID: "group_rows"}},
			Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationFilter, Filter: &ws.ConstructionFilter{ColumnID: "group_label", Operator: ws.ConstructionFilterExists}},
			Outputs: []ws.StageColumn{
				{ID: "group_id", Name: "group_id", Label: "Group ID", Type: "string"},
				{ID: "group_label", Name: "group_label", Label: "Group label", Type: "string"},
				{ID: "group_ordinal", Name: "group_ordinal", Label: "Group ordinal", Type: "integer"},
				{ID: "members", Name: "members", Label: "Members", Type: "array"},
				{ID: "status", Name: "status", Label: "Status", Type: "array"},
			},
		},
	}
	if err := document.Validate(); err != nil {
		t.Fatalf("valid source-filter/cohort-filter base: %v", err)
	}

	candidate, _, err := document.ProposeStepRemoval("source_filter", nil)
	if err != nil {
		t.Fatalf("propose source-filter removal: %v", err)
	}
	if candidate.Rows.Groups.AfterStepID != "" {
		t.Fatalf("candidate anchor was not rebased to source projection: %#v", candidate.Rows.Groups)
	}

	command := Command{Type: CommandApplyConstructionProposal, OutputID: "patients", Payload: &ProposalPayload{ProposalID: "receipt-1"}}
	if err := command.ResolveConstructionProposal(&candidate); err != nil {
		t.Fatalf("resolve exact candidate: %v", err)
	}
	wantRows, err := cloneRowDefinition(candidate.Rows)
	if err != nil {
		t.Fatal(err)
	}
	wantConstruction, err := cloneConstruction(candidate.Construction)
	if err != nil {
		t.Fatal(err)
	}
	candidate.Rows.Groups.Source.Explicit.RevisionID = "mutated-after-resolution"
	candidate.Rows.Groups.RowValues[0].ColumnID = "mutated-after-resolution"
	candidate.Construction.Steps[0].ID = "mutated-after-resolution"
	workspace, results, err := ApplyCommands(constructionWorkspace(document), commandCatalog(), "apply-anchor-removal", []Command{command})
	if err != nil {
		t.Fatalf("apply anchor removal: %v", err)
	}
	got := workspace.Documents[0]
	if !reflect.DeepEqual(got.Rows, wantRows) {
		t.Fatalf("apply lost validated cohort rebase/source/values: got=%#v want=%#v", got.Rows, wantRows)
	}
	if !reflect.DeepEqual(got.Construction, wantConstruction) || len(results) != 1 {
		t.Fatalf("applied construction/results = %#v / %#v, want exact candidate", got.Construction, results)
	}
	if err := got.Validate(); err != nil {
		t.Fatalf("applied rebased cohort document is invalid: %v", err)
	}
}

func TestApplyRowDefinitionProposalValidatesClosedCommand(t *testing.T) {
	base := ApplyCommandsRequest{
		CommandID: "command-1", SemanticsVersion: ws.CurrentSemanticsVersion,
		SnapshotToken: "snapshot-1", ExpectedDraftVersion: 3, ExpectedDraftDigest: "sha256:draft",
		Commands: []Command{{Type: CommandApplyRowDefinitionProposal, OutputID: "patients", Payload: &ProposalPayload{ProposalID: "receipt-1"}}},
	}
	if err := base.Validate(); err != nil {
		t.Fatalf("valid proposal command rejected: %v", err)
	}

	for _, test := range []struct {
		name    string
		command Command
	}{
		{name: "missing output", command: Command{Type: CommandApplyRowDefinitionProposal, Payload: &ProposalPayload{ProposalID: "receipt-1"}}},
		{name: "missing proposal", command: Command{Type: CommandApplyRowDefinitionProposal, OutputID: "patients", Payload: &ProposalPayload{}}},
		{name: "trimmed proposal", command: Command{Type: CommandApplyRowDefinitionProposal, OutputID: "patients", Payload: &ProposalPayload{ProposalID: " receipt-1 "}}},
		{name: "extra column", command: Command{Type: CommandApplyRowDefinitionProposal, OutputID: "patients", Payload: &ColumnPayload{Column: "patient-id"}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := base
			request.Commands = []Command{test.command}
			if err := request.Validate(); err == nil {
				t.Fatal("invalid proposal command was accepted")
			}
		})
	}

	request := base
	request.Commands = append(request.Commands, Command{Type: CommandRenameTable, OutputID: "patients", Title: "Renamed", Payload: &TablePayload{}})
	if err := request.Validate(); err == nil || !strings.Contains(err.Error(), "must be the only command") {
		t.Fatalf("proposal was not required to be alone: %v", err)
	}

	var decoded Command
	if err := json.Unmarshal([]byte(`{"type":"APPLY_ROW_DEFINITION_PROPOSAL","outputId":"patients","proposalId":"receipt-1","column":"client-controlled"}`), &decoded); err == nil {
		t.Fatal("proposal command accepted a client-supplied extra field")
	}
}

func TestApplyRowDefinitionProposalChangesOnlyRows(t *testing.T) {
	catalog := commandCatalog()
	workspace := rowDefinitionTestWorkspace(ws.RecordsRowDefinition())
	workspace, err := ws.MigrateLegacyContributors(workspace, catalog)
	if err != nil {
		t.Fatal(err)
	}
	workspace = ws.MigrateLosslessDefaults(workspace, catalog).NormalizePresentationOrders()
	original, err := cloneWorkspace(workspace)
	if err != nil {
		t.Fatal(err)
	}
	rows := ws.RowDefinition{Kind: ws.RowDefinitionGroups, Groups: &ws.GroupedRows{
		Source: ws.GroupSource{Kind: ws.GroupSourceField, Field: &ws.FieldGroupSource{
			OccurrenceID: ws.RootOccurrenceID, FieldPath: "active", MissingKeyPolicy: ws.MissingKeyError,
		}},
	}}
	command := Command{Type: CommandApplyRowDefinitionProposal, OutputID: "patients", Payload: &ProposalPayload{ProposalID: "receipt-1"}}
	if err := command.ResolveRowDefinitionProposal(rows); err != nil {
		t.Fatal(err)
	}
	rows.Groups.Source.Field.FieldPath = "mutated-after-resolution"

	wire, err := json.Marshal(command)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(wire), "fieldPath") || strings.Contains(string(wire), "mutated-after-resolution") {
		t.Fatalf("resolved row definition leaked into browser command: %s", wire)
	}

	got, _, err := ApplyCommands(workspace, catalog, "command-1", []Command{command})
	if err != nil {
		t.Fatal(err)
	}
	expected, err := cloneWorkspace(original)
	if err != nil {
		t.Fatal(err)
	}
	expected.Documents[0].Rows = ws.RowDefinition{Kind: ws.RowDefinitionGroups, Groups: &ws.GroupedRows{
		Source: ws.GroupSource{Kind: ws.GroupSourceField, Field: &ws.FieldGroupSource{
			OccurrenceID: ws.RootOccurrenceID, FieldPath: "active", MissingKeyPolicy: ws.MissingKeyError,
		}},
	}}
	if !reflect.DeepEqual(got, expected) {
		t.Fatalf("proposal changed more than ws.Document.Rows\n got: %#v\nwant: %#v", got, expected)
	}
	if !reflect.DeepEqual(workspace, original) {
		t.Fatal("pure reducer mutated the input workspace")
	}
}

func TestApplyRowDefinitionProposalRequiresLifecycleResolution(t *testing.T) {
	workspace := rowDefinitionTestWorkspace(ws.RecordsRowDefinition())
	before, err := cloneWorkspace(workspace)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = ApplyCommands(workspace, commandCatalog(), "command-1", []Command{{Type: CommandApplyRowDefinitionProposal, OutputID: "patients", Payload: &ProposalPayload{ProposalID: "receipt-1"}}})
	if err == nil || !strings.Contains(err.Error(), "no lifecycle-resolved row definition") {
		t.Fatalf("unprepared proposal command error = %v", err)
	}
	if !reflect.DeepEqual(workspace, before) {
		t.Fatal("failed proposal reducer mutated the input workspace")
	}
}

func exactCategoryTransformation() columntransform.ValueTransformation {
	return columntransform.ValueTransformation{
		Kind: columntransform.KindExactCategoryRecode,
		ExactCategoryRecode: &columntransform.ExactCategoryRecode{
			Mappings:      []columntransform.CategoryMapping{{From: "recorded-A", To: "group-1"}},
			UnknownPolicy: columntransform.UnknownKeepOriginal,
		},
	}
}

func TestUpdateColumnTransformationPreservesIdentityAndPersistsRoundTrip(t *testing.T) {
	catalog := commandCatalog()
	workspace := ws.Workspace{
		APIVersion: ws.APIVersion, Kind: ws.WorkspaceKind, SemanticsVersion: ws.CurrentSemanticsVersion, Explorer: ws.ExplorerMetadata{Title: "Patients"},
		Documents: []ws.Document{workspaceDocument("patients")},
		Tabs:      []ws.Tab{{ID: "patients", Title: "Patients", OutputID: "patients", Order: 0, Visible: true}},
	}
	original := workspace.Documents[0].Columns[0]
	workspace, results, err := ApplyCommands(workspace, catalog, "recode-status", []Command{{Type: CommandUpdateColumnTransformation, OutputID: "patients", Payload: &ColumnPayload{Column: original.Column, TransformationChange: &ColumnTransformationChange{Kind: "SET", Transformation: ptrTransformation(exactCategoryTransformation())}}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Column != original.Column {
		t.Fatalf("command results = %#v, want stable column %q", results, original.Column)
	}
	updated := workspace.Documents[0].Columns[0]
	if updated.Column != original.Column || !reflect.DeepEqual(updated.Source, original.Source) {
		t.Fatalf("transformation changed column identity or source: before=%#v after=%#v", original, updated)
	}
	if updated.ValueTransformation == nil || !reflect.DeepEqual(*updated.ValueTransformation, exactCategoryTransformation()) {
		t.Fatalf("transformation was not saved: %#v", updated.ValueTransformation)
	}

	canonical, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := ws.DecodeWorkspace(canonical)
	if err != nil {
		t.Fatal(err)
	}
	if got := reloaded.Documents[0].Columns[0]; got.ValueTransformation == nil || !reflect.DeepEqual(*got.ValueTransformation, exactCategoryTransformation()) {
		t.Fatalf("persisted transformation did not round-trip: %#v", got.ValueTransformation)
	}

	reloaded, _, err = ApplyCommands(reloaded, catalog, "remove-recode", []Command{{Type: CommandUpdateColumnTransformation, OutputID: "patients", Payload: &ColumnPayload{Column: original.Column, TransformationChange: &ColumnTransformationChange{Kind: "REMOVE"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if got := reloaded.Documents[0].Columns[0]; got.ValueTransformation != nil || got.Column != original.Column {
		t.Fatalf("REMOVE did not clear only the transformation: %#v", got)
	}
}

func TestUpdateColumnTransformationRejectsUnsupportedAndIncompleteMappings(t *testing.T) {
	workspace := ws.Workspace{
		APIVersion: ws.APIVersion, Kind: ws.WorkspaceKind, SemanticsVersion: ws.CurrentSemanticsVersion, Explorer: ws.ExplorerMetadata{Title: "Patients"},
		Documents: []ws.Document{workspaceDocument("patients")},
		Tabs:      []ws.Tab{{ID: "patients", Title: "Patients", OutputID: "patients", Order: 0, Visible: true}},
	}
	catalog := commandCatalog()
	catalog.Candidates[0].LogicalType = "boolean"
	command := func(transformation columntransform.ValueTransformation) Command {
		return Command{Type: CommandUpdateColumnTransformation, OutputID: "patients", Payload: &ColumnPayload{Column: "patient_id", TransformationChange: &ColumnTransformationChange{Kind: "SET", Transformation: ptrTransformation(transformation)}}}
	}
	if _, _, err := ApplyCommands(workspace, catalog, "unsupported-type", []Command{command(exactCategoryTransformation())}); err == nil || !strings.Contains(err.Error(), "COLUMN_VALUE_TYPE_UNSUPPORTED") {
		t.Fatalf("unsupported source type error = %v, want COLUMN_VALUE_TYPE_UNSUPPORTED", err)
	}

	incomplete := exactCategoryTransformation()
	incomplete.ExactCategoryRecode.Mappings = nil
	if _, _, err := ApplyCommands(workspace, commandCatalog(), "incomplete-map", []Command{command(incomplete)}); err == nil || !strings.Contains(err.Error(), "mappings must not be empty") {
		t.Fatalf("incomplete map error = %v, want mapping diagnostic", err)
	}
}

func TestCodedValueTransformationRefusalPreservesCodingIdentity(t *testing.T) {
	document := workspaceDocument("patients")
	column := document.Columns[0]
	column.Source = ws.ColumnSource{Kind: ws.SourceCodedValue}
	err := validateColumnValueTransformationForCatalog(document, commandCatalog(), column, column.Source, exactCategoryTransformation())
	if err == nil || !strings.Contains(err.Error(), "CODED_VALUE_RECODE_UNAVAILABLE") || !strings.Contains(err.Error(), "Coding.system and Coding.code") {
		t.Fatalf("coded-value transformation error = %v, want exact identity-preservation refusal", err)
	}
}

func ptrTransformation(value columntransform.ValueTransformation) *columntransform.ValueTransformation {
	return &value
}

func TestRetiredTableShapeProposalCannotBeValidated(t *testing.T) {
	for _, raw := range []string{
		`{"type":"APPLY_TABLE_SHAPE_PROPOSAL","outputId":"patients","proposalId":"receipt-1"}`,
		`{"type":"APPLY_TABLE_SHAPE_PROPOSAL","outputId":"patients","proposalId":"receipt-1","column":"client-controlled"}`,
		`{"type":"APPLY_TABLE_SHAPE_PROPOSAL","outputId":"patients"}`,
	} {
		var command Command
		if err := json.Unmarshal([]byte(raw), &command); err == nil {
			if err := command.validate(); err == nil {
				t.Fatalf("retired command validated: %s", raw)
			}
		}
	}
	request := ApplyCommandsRequest{CommandID: "command-1", SemanticsVersion: ws.CurrentSemanticsVersion, SnapshotToken: "snapshot-1", ExpectedDraftVersion: 3, ExpectedDraftDigest: "sha256:draft", Commands: []Command{{Type: "APPLY_TABLE_SHAPE_PROPOSAL", OutputID: "patients", Payload: &ProposalPayload{ProposalID: "receipt-1"}}}}
	if err := request.Validate(); err == nil {
		t.Fatal("retired command validated")
	}
}

func TestRetiredTableShapeProposalCannotMutateCurrentWorkspace(t *testing.T) {
	workspace := rowDefinitionTestWorkspace(ws.RecordsRowDefinition())
	before, err := cloneWorkspace(workspace)
	if err != nil {
		t.Fatal(err)
	}
	for _, proposal := range []string{"receipt-1", "receipt-removal", ""} {
		_, _, err := ApplyCommands(workspace, commandCatalog(), "command-1", []Command{{Type: "APPLY_TABLE_SHAPE_PROPOSAL", OutputID: "patients", Payload: &ProposalPayload{ProposalID: proposal}}})
		if err == nil {
			t.Fatal("retired proposal reached reducer")
		}
		if !reflect.DeepEqual(workspace, before) {
			t.Fatal("failed retired command mutated current workspace")
		}
	}
}
