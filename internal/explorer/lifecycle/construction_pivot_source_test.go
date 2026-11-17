package lifecycle

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
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
	construction.Steps[0].Outputs[len(construction.Steps[0].Outputs)-1].Label = "Status"
	construction.Steps[0].Outputs[len(construction.Steps[0].Outputs)-1].Table = &authoringv2.TablePresentation{Visible: pivotPresentationBool(true), Order: pivotPresentationInt(4)}
	construction.Steps[1].Outputs[1].Label = "Status"
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
			{ID: "pivot-input", Name: constructionPivotSourceOutputName("pivot-input"), Label: "Status", Type: "string"},
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
	regeneratedLabel := ""
	for _, step := range prepared.Steps {
		if step.OwnerStepID != "pivot" || step.Operation.RelatedField == nil {
			continue
		}
		for _, column := range step.Outputs {
			if column.ID == "pivot-input" {
				regeneratedLabel = column.Label
			}
		}
	}
	if regeneratedLabel != "Status" {
		t.Fatalf("regenerated Pivot source column label = %q, want the snapshot candidate Title %q", regeneratedLabel, "Status")
	}
	if _, _, err := document.AnalyzeConstructionCandidate(prepared, "pivot", nil); err != nil {
		t.Fatalf("analyze prepared Pivot label edit after source-helper regeneration: %v", err)
	}
}

func TestConstructionFieldDisplayLabelChainAndFallbacks(t *testing.T) {
	rows := []struct {
		name         string
		resourceType string
		path         string
		candidates   []string
		want         string
	}{
		{name: "title wins over later display candidates", resourceType: "Patient", path: "active", candidates: []string{"Birth count", "Active"}, want: "Birth count"},
		{name: "candidates are trimmed", resourceType: "Patient", path: "active", candidates: []string{"  Birth count  "}, want: "Birth count"},
		{name: "blank title falls through to display", resourceType: "Patient", path: "active", candidates: []string{"   ", " Active "}, want: "Active"},
		{name: "all blank falls back to Type.path", resourceType: "Patient", path: "active", candidates: []string{"", " \t "}, want: "Patient.active"},
		{name: "no candidates falls back to Type.path", resourceType: "Patient", path: "active", want: "Patient.active"},
		{name: "snapshot empty-Title collapse yields raw path", resourceType: "Patient", path: "active", candidates: []string{"active"}, want: "active"},
		{name: "prefixed and padded path normalizes", resourceType: "Patient", path: "  Patient.active ", candidates: []string{""}, want: "Patient.active"},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			if got := constructionFieldDisplayLabel(row.resourceType, row.path, row.candidates...); got != row.want {
				t.Fatalf("constructionFieldDisplayLabel(%q, %q, %q) = %q, want %q", row.resourceType, row.path, row.candidates, got, row.want)
			}
		})
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

func TestConstructionPivotSourceCapabilityReportsPreciseSkipClassReasonCodes(t *testing.T) {
	index, err := fhirschema.GeneratedIndex()
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := NewSchemaRowChoiceResolver(index)
	if err != nil {
		t.Fatal(err)
	}
	empty := authoringv2.Construction{Version: authoringv2.ConstructionVersion}
	rowChanging := authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{filterSourceGroupStep()}}
	blockedStages := []explorer.ReceiptConstructionStage{{ID: "group-first", InputStageID: recipe.ConstructionSourceProjectionID, Operation: "GROUP"}}
	bs := func(id, path, logicalType, cardinality string, populated bool) capability.Candidate {
		return rowSourceLaneCandidate(filterSourceRootNode, filterSourceRootType, id, path, logicalType, cardinality, populated)
	}
	candidatesSnapshot := func(token string, candidates ...capability.Candidate) capability.Snapshot {
		snapshot := filterSourceSnapshot(token)
		snapshot.Candidates = candidates
		return snapshot
	}
	rows := []struct {
		name         string
		snapshot     capability.Snapshot
		service      *Service
		construction authoringv2.Construction
		stageID      string
		stages       []explorer.ReceiptConstructionStage
		wantCode     string
	}{
		{
			name: "every eligible field has no observed values",
			snapshot: candidatesSnapshot("pivot-source-precise-empty",
				bs("bs-id", "id", "string", "optional_one", false),
				bs("bs-active", filterSourceField, "boolean", "optional_one", false)),
			service: filterSourceService(t), construction: empty,
			stageID:  recipe.ConstructionSourceProjectionID,
			wantCode: "NO_OBSERVED_VALUES_PIVOT_SOURCE_FIELDS",
		},
		{
			name: "every field carries a logical type that cannot be carried into Pivot",
			snapshot: candidatesSnapshot("pivot-source-precise-type",
				bs("bs-id", "id", "quantity", "optional_one", true),
				bs("bs-active", filterSourceField, "quantity", "optional_one", true)),
			service: filterSourceService(t), construction: empty,
			stageID:  recipe.ConstructionSourceProjectionID,
			wantCode: "NO_TYPE_ELIGIBLE_PIVOT_SOURCE_FIELDS",
		},
		{
			name:     "every field is repeated at the row root",
			snapshot: candidatesSnapshot("pivot-source-precise-route", bs("bs-active", filterSourceField, "boolean", "many", true)),
			service:  filterSourceService(t), construction: empty,
			stageID:  recipe.ConstructionSourceProjectionID,
			wantCode: "NO_ROUTE_ELIGIBLE_PIVOT_SOURCE_FIELDS",
		},
		{
			name:     "choices were issued for a previous dataset snapshot",
			snapshot: filterSourceSnapshot("pivot-source-precise-stale"),
			service: rowSourceServiceWithPlanner(staleRowSourceChoicePlanner{
				inner:  resolver,
				issued: rowSourceSnapshotWithGeneration(filterSourceSnapshot("pivot-source-precise-stale"), "pivot-source-precise-stale-issued"),
			}),
			construction: empty, stageID: recipe.ConstructionSourceProjectionID,
			wantCode: "STALE_PIVOT_SOURCE_SNAPSHOT",
		},
		{
			name:     "the construction prefix cannot carry root fields to the stage",
			snapshot: filterSourceSnapshot("pivot-source-precise-reach"),
			service:  filterSourceService(t), construction: rowChanging, stageID: "group-first",
			stages:   blockedStages,
			wantCode: "ROOT_SOURCE_PROJECTION_UNAVAILABLE",
		},
		{
			name:     "the authorized choice planner is not configured",
			snapshot: filterSourceSnapshot("pivot-source-precise-planner"),
			service:  rowSourceServiceWithPlanner(nil), construction: empty,
			stageID:  recipe.ConstructionSourceProjectionID,
			wantCode: "ROW_CHOICE_UNAVAILABLE",
		},
		{
			name:     "the planner enumerates no fields at all",
			snapshot: filterSourceSnapshot("pivot-source-precise-none"),
			service:  rowSourceServiceWithPlanner(emptyRowSourceChoicePlanner{}), construction: empty,
			stageID:  recipe.ConstructionSourceProjectionID,
			wantCode: "NO_ELIGIBLE_POPULATED_PIVOT_SOURCE_FIELDS",
		},
		{
			name: "distinct skipped classes stay on the generic fallback",
			snapshot: candidatesSnapshot("pivot-source-precise-mixed",
				bs("bs-id", "id", "string", "optional_one", false),
				bs("bs-active", filterSourceField, "quantity", "optional_one", true)),
			service: filterSourceService(t), construction: empty,
			stageID:  recipe.ConstructionSourceProjectionID,
			wantCode: "NO_ELIGIBLE_POPULATED_PIVOT_SOURCE_FIELDS",
		},
	}
	genericCode := "NO_ELIGIBLE_POPULATED_PIVOT_SOURCE_FIELDS"
	distinct := map[string]string{}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			base := filterSourceBase(row.snapshot, row.construction)
			base.stages = append(base.stages, row.stages...)
			result, err := row.service.constructionPivotSourceCapability(context.Background(), base, filterSourceOutput, row.stageID)
			if err != nil {
				t.Fatalf("pivot source capability: %v", err)
			}
			if result.Supported || len(result.Choices) != 0 {
				t.Fatalf("capability = %#v, want unsupported with no advertised choices", result)
			}
			if result.ReasonCode != row.wantCode {
				t.Fatalf("reasonCode = %q (reason %q), want %q", result.ReasonCode, result.Reason, row.wantCode)
			}
			if strings.TrimSpace(result.Reason) == "" {
				t.Fatalf("reason is blank for %s", row.wantCode)
			}
			if previous, seen := distinct[result.ReasonCode]; seen && previous != row.name && result.ReasonCode != genericCode {
				t.Fatalf("rows %q and %q share reasonCode %q", previous, row.name, result.ReasonCode)
			}
			if _, seen := distinct[result.ReasonCode]; !seen {
				distinct[result.ReasonCode] = row.name
			}
			if result.StageID != row.stageID {
				t.Fatalf("stageId = %q, want %q", result.StageID, row.stageID)
			}
		})
	}
	if len(distinct) != 7 {
		t.Fatalf("expected seven distinct lane reason codes, got %#v", distinct)
	}
}

func TestConstructionFieldDisplayLabelRefusesControlBytes(t *testing.T) {
	cases := []struct{ name, candidate, fallback, want string }{
		{"nul-esc-in-title-falls-to-next", "Active\x00flag\x1b[31mred", "Clean Title", "Clean Title"},
		{"control-then-blank-fallback-all", "red\x7fbow", "   ", "Observation.status"},
		{"newline-is-control-too", "line1\nline2", "Next", "Next"},
		{"clean-candidate-still-first", "  Nice Title  ", "unused", "Nice Title"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := constructionFieldDisplayLabel("Observation", "status", tc.candidate, tc.fallback); got != tc.want {
				t.Fatalf("label = %q, want %q", got, tc.want)
			}
		})
	}
	if got := constructionFieldDisplayLabel("Observation", "status", "bad\x00", "also\x01bad"); got != "Observation.status" {
		t.Fatalf("all-control candidates must fall back to Type.path, got %q", got)
	}
}

func TestConstructionPivotSourceCapabilityNamesInventoryFailureDistinctly(t *testing.T) {
	const internalDetail = "capability-store-dsn=secret-token"
	inventoryErr := errors.New("row-choice inventory store unreachable: " + internalDetail)
	empty := authoringv2.Construction{Version: authoringv2.ConstructionVersion}
	observedSnapshot := filterSourceSnapshot("pivot-source-inventory-observed")
	observedSnapshot.Candidates = []capability.Candidate{
		rowSourceLaneCandidate(filterSourceRootNode, filterSourceRootType, "bs-active", filterSourceField, "boolean", "optional_one", false),
	}
	rows := []struct {
		name     string
		service  *Service
		snapshot capability.Snapshot
		wantCode string
		wantFail bool
	}{
		{name: "planner enumeration failure names the inventory class",
			service:  rowSourceServiceWithPlanner(failingRowSourcePlanner{err: inventoryErr}),
			snapshot: filterSourceSnapshot("pivot-source-inventory-failure"),
			wantCode: "PIVOT_SOURCE_INVENTORY_UNAVAILABLE", wantFail: true},
		{name: "a planner that enumerates nothing stays generic",
			service:  rowSourceServiceWithPlanner(emptyRowSourceChoicePlanner{}),
			snapshot: filterSourceSnapshot("pivot-source-inventory-empty"),
			wantCode: "NO_ELIGIBLE_POPULATED_PIVOT_SOURCE_FIELDS"},
		{name: "truly empty data names the observed class, not the inventory class",
			service: filterSourceService(t), snapshot: observedSnapshot,
			wantCode: "NO_OBSERVED_VALUES_PIVOT_SOURCE_FIELDS"},
	}
	distinct := map[string]bool{}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			result, err := row.service.constructionPivotSourceCapability(context.Background(),
				filterSourceBase(row.snapshot, empty), filterSourceOutput, recipe.ConstructionSourceProjectionID)
			if row.wantFail {
				if err == nil {
					t.Fatal("a failed enumeration was swallowed: the capability must still return the wrapped error")
				}
				if !errors.Is(err, inventoryErr) {
					t.Fatalf("the planner cause was not wrapped through the typed error: %v", err)
				}
				if code := lifecycleErrorCode(err); code != row.wantCode {
					t.Fatalf("typed error code = %q, want %q", code, row.wantCode)
				}
				if strings.Contains(result.Reason, internalDetail) || strings.Contains(result.ReasonCode, internalDetail) {
					t.Fatalf("planner error detail %q crossed into advertised payload: %#v", internalDetail, result)
				}
			} else if err != nil {
				t.Fatalf("soft empty verdict must not surface as an error: %v", err)
			}
			if result.Supported || len(result.Choices) != 0 {
				t.Fatalf("capability = %#v, want fail-closed unsupported with no advertised choices", result)
			}
			if result.ReasonCode != row.wantCode || strings.TrimSpace(result.Reason) == "" {
				t.Fatalf("reasonCode = %q (reason %q), want %q with a non-blank reason", result.ReasonCode, result.Reason, row.wantCode)
			}
			if result.StageID != recipe.ConstructionSourceProjectionID {
				t.Fatalf("stageId = %q, want the requested stage even inside the failure class", result.StageID)
			}
			distinct[result.ReasonCode] = true
		})
	}
	if len(distinct) != 3 {
		t.Fatalf("inventory-error and empty-data verdicts collapsed: %#v", distinct)
	}
}
