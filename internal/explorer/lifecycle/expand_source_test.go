package lifecycle

// ROW-SOURCE-08 EXPAND source lane tests. Clones the Unpivot source lane test
// structure for the EXPAND lane: capability advertisement (exact six-field
// wire shape, skip-class reason codes, inventory fail-closed), request-boundary
// validation, candidate-prep refusals that fire before choice resolution, and
// the Apply-time reauthorization contract.
//
// Provenance: this file first shipped as a red deliverable whose acceptance
// tests pinned four product bugs (BUG-001..004). The three lane defects were
// repaired against these tests:
//
// BUG-001 (repaired) — the resolve/capability gates demanded a FieldGroupKey
// identity with Cardinality==Many && Shape==Array, but the mint gate
// (capability/row_choice.go validateRowChoiceFacts, re-run inside
// DecodeRowChoiceID) pins FieldGroupKey~SCALAR/ONE and ExpandedScope~ARRAY/
// MANY as disjoint classes, and the planner mints arrays only as
// ExpandedScope (applicableRowChoiceKinds). The lane now enumerates, resolves,
// and reauthorizes the honest ExpandedScope class, so an advertised array is
// exactly an acceptable selection.
//
// BUG-002 (repaired) — authoringv2 validatePivotSourceProjection had no
// ConstructionOperationExpand owner case, so the EXPAND-owned
// SourceProjections entry the lane signs was rejected as INVALID_DOCUMENT on
// every validation path. The owner case now requires the projection to be the
// owner Expand step's declared array input.
//
// BUG-003 (repaired) — a lane-owned input with a stripped ExpandedField used
// to reach a nil-pointer dereference in Apply-time reauthorization; both
// payload consumers now refuse nil fail-closed with the contract error.
//
// BUG-004 (tracked) — the compiled root SOURCE_PROJECTION stage does not yet
// map the Many-cardinality array column the physical EXPAND gate consumes
// (lower/recipe_construction_reshape.go:469-478). The compile-package
// positive test pins the finished contract; root-stage mapping completion is
// tracked there.

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
)

const (
	expandTestStep    = "expand-uri"
	expandTestInput   = "expand_source_uri"
	expandTestItem    = "expand_uri_item"
	expandTestCarrier = "derive-base"
	expandTestOutput  = "patients"
)

// expandTestPlanner hands back exactly the ExpandedScope array rows the real
// planner mints; the struct fields (not the tokens) are what the lane
// enumeration reads (expand_source.go).
type expandTestPlanner struct{ choices []capability.RowChoice }

func (p expandTestPlanner) ListRowChoices(context.Context, capability.Snapshot, authoringv2.Document) ([]capability.RowChoice, error) {
	return p.choices, nil
}

// expandTestMintArrayChoice mints the only token shape that a legal ARRAY/MANY
// row can carry: capability.NewRowChoice accepts RowChoiceExpandedScope with
// ARRAY/MANY facts (capability/row_choice.go:123-126) and refuses
// FieldGroupKey for anything but SCALAR/ONE. The planner itself mints arrays
// only as ExpandedScope (applicableRowChoiceKinds), so this is the honest
// input for the resolve-path tests: exactly what the server can sign for an
// array root field.
func expandTestMintArrayChoice(t *testing.T, snapshot capability.Snapshot, nodeID, resourceType, path, fhirType string) capability.RowChoice {
	t.Helper()
	occurrence := capability.RowChoiceOccurrence{OccurrenceID: authoringv2.RootOccurrenceID, NodeID: nodeID, ResourceType: resourceType}
	facts := capability.RowChoiceFacts{ResourceType: resourceType, CanonicalPath: path, FHIRType: fhirType, Cardinality: capability.RowChoiceMany, Shape: capability.RowChoiceArray}
	choice, err := capability.NewRowChoice(snapshot, []capability.RowChoiceOccurrence{occurrence}, authoringv2.RootOccurrenceID, capability.RowChoiceExpandedScope, facts)
	if err != nil {
		t.Fatalf("mint array row choice %s on %s: %v", path, resourceType, err)
	}
	return choice
}

// expandTestAdvertisedRow sets the display label on a minted ExpandedScope
// array row for the planner double. The lane enumerates exactly the
// ExpandedScope + Many + non-reference class (expand_source.go), matching
// what the real SchemaRowChoiceResolver mints for array root fields.
func expandTestAdvertisedRow(choice capability.RowChoice, label string) capability.RowChoice {
	choice.Label = label
	return choice
}

func expandTestPatientArrayCandidate(id, path, logicalType, label string, populated bool) capability.Candidate {
	// The capability builder mints repeated fields as many-cardinality with
	// the ARRAY family of projection modes and a self-describing boundary;
	// the lane's backing check requires exactly that honest shape.
	candidate := rowSourceLaneCandidate("node-patient", "Patient", id, path, logicalType, "many", populated)
	candidate.Label = label
	candidate.ProjectionModes = []capability.ProjectionMode{capability.ProjectionArray, capability.ProjectionFirst, capability.ProjectionDistinctArray}
	candidate.RepeatedBoundaries = []capability.RepeatedBoundary{{Path: path, MaxItems: 2}}
	return candidate
}

func expandTestProcedureSnapshot(token string) capability.Snapshot {
	return capability.NewSnapshot(
		capability.SnapshotIdentity{
			Project: filterSourceProject, Generation: token, AuthorizationScopeDigest: "scope",
			SchemaDigest: "schema-a", ResourceInventoryDigest: "resources", RelationshipDigest: "relationships",
			FieldDigest: "fields", ProtocolVersion: "protocol", CompilerVersion: "compiler",
			TraversalPolicyVersion: "traversal", ProjectionPolicyVersion: "projection",
		},
		capability.Policy{Route: capability.RoutePolicy{Version: "traversal", AllowsRepeatedEdges: true, AllowsSelfLoops: true}},
		capability.StatusReady, true, false,
		[]capability.Node{{ID: "procedure", ResourceType: "Procedure", RowRootEligible: true, RowGrain: "RESOURCE", Populated: true, DocumentCount: 4}},
		nil,
		[]capability.Candidate{
			rowSourceLaneCandidate("procedure", "Procedure", "c_procedure_id", "id", "string", "optional_one", true),
			{ID: "c_procedure_uri", NodeID: "procedure", ResourceType: "Procedure", FieldPath: "instantiatesUri[]",
				Label: "Instantiates uri", LogicalType: "string", Cardinality: "many",
				ProjectionModes:     []capability.ProjectionMode{capability.ProjectionArray, capability.ProjectionFirst, capability.ProjectionDistinctArray},
				RepeatedBoundaries:  []capability.RepeatedBoundary{{Path: "instantiatesUri[]", MaxItems: 2}},
				SupportedOperations: []capability.Operation{capability.OperationSelect},
				Observed:            true, Populated: true, ObservedDocumentCount: 4},
		},
		nil,
	)
}

func expandTestProcedureDocument() authoringv2.Document {
	return authoringv2.Document{
		Kind:             authoringv2.Kind,
		Output:           authoringv2.Output{ID: "procedures", Title: "Procedures"},
		RootResourceType: "Procedure",
		Route:            authoringv2.RouteNode{OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "Procedure"},
		Rows:             authoringv2.RecordsRowDefinition(),
		Columns: []authoringv2.Column{{
			ColumnID: "procedure-id", Column: "procedure_id", Label: "Procedure ID",
			LogicalType: "string", OccurrenceID: authoringv2.RootOccurrenceID,
			Source: authoringv2.ColumnSource{Kind: authoringv2.SourceField, Field: &authoringv2.FieldSource{Path: "id", ProjectionMode: "VALUE"}},
		}},
	}
}

func expandTestProcedureBase(snapshot capability.Snapshot, construction authoringv2.Construction) constructionBase {
	return constructionBase{
		document:     expandTestProcedureDocument(),
		construction: construction,
		snapshot:     snapshot,
		stages: []explorer.ReceiptConstructionStage{{
			ID: recipe.ConstructionSourceProjectionID, RowIdentityColumn: "_key",
			Columns: []explorer.ReceiptConstructionStageColumn{
				{ID: "procedure-id", Name: "procedure_id", Label: "Procedure ID", Type: "string"},
			},
		}},
	}
}

func expandTestExpandStep(inputColumn, previousStep string) authoringv2.ConstructionStep {
	inputs := []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputSourceProjection}}
	if previousStep != "" {
		inputs = []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputStepOutput, StepID: previousStep}}
	}
	return authoringv2.ConstructionStep{
		ID:     expandTestStep,
		Inputs: inputs,
		Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationExpand, Expand: &authoringv2.ConstructionExpand{
			ConstructionID: expandTestStep, InputColumnID: inputColumn, OutputColumnID: expandTestItem,
			EmptyPolicy: authoringv2.ConstructionExpandEmptyExclude,
		}},
		Outputs: []authoringv2.StageColumn{{ID: expandTestItem, Name: "expand_uri_item", Label: "Uri item", Type: "string"}},
	}
}

func expandTestSignedPayload(path, fhirType, logicalType string) *authoringv2.ConstructionExpandedFieldSource {
	return &authoringv2.ConstructionExpandedFieldSource{Path: path, Cardinality: authoringv2.ConstructionExpandedFieldCardinalityMany, FHIRType: fhirType, LogicalType: logicalType}
}

func expandTestService(planner RowChoicePlanner) *Service {
	return &Service{config: Config{RowChoicePlanner: planner}}
}

func expandTestPatientBase(snapshot capability.Snapshot, construction authoringv2.Construction) constructionBase {
	document := authoringv2.Document{
		Kind:             authoringv2.Kind,
		Output:           authoringv2.Output{ID: expandTestOutput, Title: "Patients"},
		RootResourceType: "Patient",
		Route:            authoringv2.RouteNode{OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "Patient"},
		Rows:             authoringv2.RecordsRowDefinition(),
		Columns: []authoringv2.Column{{
			ColumnID: "patient-id", Column: "patient_id", Label: "Patient ID",
			LogicalType: "string", OccurrenceID: authoringv2.RootOccurrenceID,
			Source: authoringv2.ColumnSource{Kind: authoringv2.SourceField, Field: &authoringv2.FieldSource{Path: "id", ProjectionMode: "VALUE"}},
		}},
	}
	return constructionBase{
		document:     document,
		construction: construction,
		snapshot:     snapshot,
		stages: []explorer.ReceiptConstructionStage{{
			ID: recipe.ConstructionSourceProjectionID, RowIdentityColumn: "_key",
			Columns: []explorer.ReceiptConstructionStageColumn{{ID: "patient-id", Name: "patient_id", Label: "Patient ID", Type: "string"}},
		}},
	}
}

// --- G1: stale stage reference ---------------------------------------------

func TestConstructionExpandSourceCapabilityReportsStaleStageReference(t *testing.T) {
	harness := newFilterSourceHarness(t)
	base := expandTestPatientBase(harness.snapshot, authoringv2.Construction{Version: authoringv2.ConstructionVersion})
	result, err := harness.service.constructionExpandSourceCapability(context.Background(), base, expandTestOutput, "stage-that-was-removed")
	if err != nil {
		t.Fatalf("expand source capability: %v", err)
	}
	if result.Supported || len(result.Choices) != 0 || result.ReasonCode != "STALE_STAGE_REFERENCE" {
		t.Fatalf("stale stage capability = %#v, want unsupported STALE_STAGE_REFERENCE", result)
	}
}

// --- G2: advertisement and the pinned six-field wire shape ------------------

func TestConstructionExpandSourceCapabilityAdvertisesPopulatedRootPrimitiveArray(t *testing.T) {
	harness := newFilterSourceHarness(t)
	snapshot := harness.snapshot
	candidate := expandTestPatientArrayCandidate("c-photo-array", "photo[]", "string", "Photo attachment", true)
	snapshot.Candidates = append(snapshot.Candidates, candidate)
	row := expandTestAdvertisedRow(expandTestMintArrayChoice(t, snapshot, "node-patient", "Patient", "photo[]", "string"), "ignored — wire label is candidate-sourced")
	service := expandTestService(expandTestPlanner{choices: []capability.RowChoice{row}})

	result, err := service.constructionExpandSourceCapability(context.Background(), expandTestPatientBase(snapshot, authoringv2.Construction{Version: authoringv2.ConstructionVersion}), expandTestOutput, recipe.ConstructionSourceProjectionID)
	if err != nil {
		t.Fatalf("expand source capability: %v", err)
	}
	if !result.Supported || len(result.Choices) != 1 {
		t.Fatalf("expand source capability = %#v, want one advertised array choice", result)
	}
	choice := result.Choices[0]
	if choice.ChoiceID != row.ChoiceID || choice.FieldPath != "photo[]" || choice.Label != "Photo attachment" ||
		choice.LogicalType != "string" || !choice.IsPopulated || choice.ColumnID != "" {
		t.Fatalf("advertised choice = %#v, want candidate-sourced label/logical with no durable column", choice)
	}
	// The UI capabilities schema is strict: exactly six wire fields, in the
	// pinned spelling (expand_source.go:42-49), with no pivot-style extras.
	encoded, err := json.Marshal(choice)
	if err != nil {
		t.Fatal(err)
	}
	wantJSON := `{"choiceId":"` + row.ChoiceID + `","fieldPath":"photo[]","label":"Photo attachment","logicalType":"string","isPopulated":true,"columnId":""}`
	if string(encoded) != wantJSON {
		t.Fatalf("choice wire = %s, want the pinned six-field encoding %s", encoded, wantJSON)
	}
}

// --- G3: durable columnId restore only while the owner still signs ----------

func TestConstructionExpandSourceCapabilityAdvertisesDurableColumnIdOnlyWhileSigned(t *testing.T) {
	harness := newFilterSourceHarness(t)
	snapshot := harness.snapshot
	snapshot.Candidates = append(snapshot.Candidates, expandTestPatientArrayCandidate("c-photo-array", "photo[]", "string", "Photo attachment", true))
	row := expandTestAdvertisedRow(expandTestMintArrayChoice(t, snapshot, "node-patient", "Patient", "photo[]", "string"), "Photo attachment")
	service := expandTestService(expandTestPlanner{choices: []capability.RowChoice{row}})

	live := expandTestExpandStep(expandTestInput, "")
	live.Operation.Expand.ExpandedField = expandTestSignedPayload("photo[]", "string", "string")
	repointed := expandTestExpandStep("another_column", "")
	repointed.Operation.Expand.ExpandedField = expandTestSignedPayload("photo[]", "string", "string")
	stripped := expandTestExpandStep(expandTestInput, "")
	foreignOwner := expandTestExpandStep(expandTestInput, "")
	foreignOwner.Operation.Expand.ExpandedField = expandTestSignedPayload("photo[]", "string", "string")

	columnIDFor := func(t *testing.T, steps []authoringv2.ConstructionStep, owner string) string {
		t.Helper()
		construction := authoringv2.Construction{
			Version:           authoringv2.ConstructionVersion,
			Steps:             steps,
			SourceProjections: []authoringv2.ConstructionSourceProjection{{ColumnID: expandTestInput, OwnerStepID: owner, OccurrenceID: authoringv2.RootOccurrenceID, FieldPath: "photo[]", FHIRType: "string", LogicalType: "string", Label: "Photo attachment"}},
		}
		base := expandTestPatientBase(snapshot, construction)
		result, err := service.constructionExpandSourceCapability(context.Background(), base, expandTestOutput, recipe.ConstructionSourceProjectionID)
		if err != nil {
			t.Fatal(err)
		}
		for _, choice := range result.Choices {
			if choice.FieldPath == "photo[]" {
				return choice.ColumnID
			}
		}
		return "<missing choice>"
	}

	if got := columnIDFor(t, []authoringv2.ConstructionStep{live}, expandTestStep); got != expandTestInput {
		t.Fatalf("live durable materialization advertised columnId %q, want %q", got, expandTestInput)
	}
	if got := columnIDFor(t, []authoringv2.ConstructionStep{repointed}, expandTestStep); got != "" {
		t.Fatalf("saved expand step that no longer declares the source column was still advertised as columnId %q", got)
	}
	if got := columnIDFor(t, []authoringv2.ConstructionStep{stripped}, expandTestStep); got != "" {
		t.Fatalf("expand step with a stripped payload was still advertised as columnId %q", got)
	}
	if got := columnIDFor(t, []authoringv2.ConstructionStep{live}, "another-step"); got != "" {
		t.Fatalf("materialization not owned by this stage's expand was advertised as columnId %q", got)
	}
}

// --- G4: precise per-skipped-class lane reason codes ------------------------

func TestConstructionExpandSourceCapabilityReportsPreciseSkipClassReasonCodes(t *testing.T) {
	const rootStage = recipe.ConstructionSourceProjectionID
	empty := authoringv2.Construction{Version: authoringv2.ConstructionVersion}
	rowChanging := authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{filterSourceGroupStep()}}
	blockedStages := []explorer.ReceiptConstructionStage{{ID: "group-first", InputStageID: recipe.ConstructionSourceProjectionID, Operation: "GROUP"}}

	photoRow := func(t *testing.T, snapshot capability.Snapshot) capability.RowChoice {
		return expandTestAdvertisedRow(expandTestMintArrayChoice(t, snapshot, "node-patient", "Patient", "photo[]", "string"), "Photo attachment")
	}
	rows := []struct {
		name         string
		service      *Service
		construction authoringv2.Construction
		stageID      string
		stages       []explorer.ReceiptConstructionStage
		candidates   []capability.Candidate
		choices      func(t *testing.T, snapshot capability.Snapshot) []capability.RowChoice
		wantCode     string
	}{
		{
			name: "every eligible array has no observed values",
			candidates: []capability.Candidate{
				expandTestPatientArrayCandidate("c-patient-id", "id", "string", "Logical id", true),
				expandTestPatientArrayCandidate("c-photo-array", "photo[]", "string", "Photo attachment", false),
			},
			service: nil, construction: empty, stageID: rootStage,
			choices: func(t *testing.T, s capability.Snapshot) []capability.RowChoice {
				return []capability.RowChoice{photoRow(t, s)}
			},
			wantCode: "NO_OBSERVED_VALUES_EXPAND_SOURCE_FIELDS",
		},
		{
			name: "every array holds element values the Expand lane refuses to flatten",
			candidates: []capability.Candidate{
				expandTestPatientArrayCandidate("c-photo-array", "photo[]", "string", "Photo attachment", true),
			},
			service: nil, construction: empty, stageID: rootStage,
			choices: func(t *testing.T, s capability.Snapshot) []capability.RowChoice {
				row := photoRow(t, s)
				row.FHIRType = "Coding" // expand_source.go:214 refuses object/Coding element FHIR types
				return []capability.RowChoice{row}
			},
			wantCode: "NO_TYPE_ELIGIBLE_EXPAND_SOURCE_FIELDS",
		},
		{
			name: "every enumerated class is a non-array route membership",
			candidates: []capability.Candidate{
				expandTestPatientArrayCandidate("c-photo-array", "photo[]", "string", "Photo attachment", true),
			},
			service: nil, construction: empty, stageID: rootStage,
			choices: func(t *testing.T, s capability.Snapshot) []capability.RowChoice {
				row := photoRow(t, s)
				row.Cardinality = capability.RowChoiceOne // fails the Cardinality==MANY membership test at L205
				return []capability.RowChoice{row}
			},
			wantCode: "NO_ROUTE_ELIGIBLE_EXPAND_SOURCE_FIELDS",
		},
		{
			name: "choices were issued for a previous dataset snapshot",
			candidates: []capability.Candidate{
				expandTestPatientArrayCandidate("c-photo-array", "photo[]", "string", "Photo attachment", true),
			},
			service: nil, construction: empty, stageID: rootStage,
			choices: func(t *testing.T, s capability.Snapshot) []capability.RowChoice {
				stale := rowSourceSnapshotWithGeneration(s, s.Identity.Generation+"-issued")
				return []capability.RowChoice{photoRow(t, stale)}
			},
			wantCode: "STALE_EXPAND_SOURCE_SNAPSHOT",
		},
		{
			name: "a stage behind an earlier GROUP cannot carry root arrays",
			candidates: []capability.Candidate{
				expandTestPatientArrayCandidate("c-photo-array", "photo[]", "string", "Photo attachment", true),
			},
			service: expandTestService(nil), construction: rowChanging, stageID: "group-first", stages: blockedStages,
			choices: func(t *testing.T, s capability.Snapshot) []capability.RowChoice {
				return []capability.RowChoice{photoRow(t, s)}
			},
			wantCode: "ROOT_SOURCE_PROJECTION_UNAVAILABLE",
		},
		{
			name: "the authorized choice planner is not configured",
			candidates: []capability.Candidate{
				expandTestPatientArrayCandidate("c-photo-array", "photo[]", "string", "Photo attachment", true),
			},
			service: expandTestService(nil), construction: empty, stageID: rootStage,
			choices: func(t *testing.T, s capability.Snapshot) []capability.RowChoice {
				return []capability.RowChoice{photoRow(t, s)}
			},
			wantCode: "ROW_CHOICE_UNAVAILABLE",
		},
		{
			name: "the planner enumerates no fields at all",
			candidates: []capability.Candidate{
				expandTestPatientArrayCandidate("c-photo-array", "photo[]", "string", "Photo attachment", true),
			},
			service: expandTestService(emptyRowSourceChoicePlanner{}), construction: empty, stageID: rootStage,
			choices:  func(t *testing.T, s capability.Snapshot) []capability.RowChoice { return nil },
			wantCode: "NO_ELIGIBLE_POPULATED_EXPAND_SOURCE_FIELDS",
		},
		{
			name: "distinct skipped classes stay on the generic fallback",
			candidates: []capability.Candidate{
				expandTestPatientArrayCandidate("c-photo-array", "photo[]", "string", "Photo attachment", true),
			},
			service: nil, construction: empty, stageID: rootStage,
			choices: func(t *testing.T, s capability.Snapshot) []capability.RowChoice {
				typed := photoRow(t, s)
				typed.FHIRType = "Coding"
				routed := photoRow(t, s)
				routed.Reference = true
				return []capability.RowChoice{typed, routed}
			},
			wantCode: "NO_ELIGIBLE_POPULATED_EXPAND_SOURCE_FIELDS",
		},
	}
	genericCode := "NO_ELIGIBLE_POPULATED_EXPAND_SOURCE_FIELDS"
	distinct := map[string]string{}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			harness := newFilterSourceHarness(t)
			snapshot := harness.snapshot
			snapshot.Candidates = append(snapshot.Candidates, row.candidates...)
			// Drop the fixture's scalars so they cannot skew the candidate
			// verdicts; the lane decision sees only the array rows under test.
			snapshot.Candidates = row.candidates
			service := row.service
			if service == nil {
				service = expandTestService(expandTestPlanner{choices: row.choices(t, snapshot)})
			}
			base := expandTestPatientBase(snapshot, row.construction)
			base.stages = append(base.stages, row.stages...)
			result, err := service.constructionExpandSourceCapability(context.Background(), base, expandTestOutput, row.stageID)
			if err != nil {
				t.Fatalf("expand source capability: %v", err)
			}
			if result.Supported || len(result.Choices) != 0 {
				t.Fatalf("capability = %#v, want unsupported with no advertised choices", result)
			}
			if result.ReasonCode != row.wantCode {
				t.Fatalf("reasonCode = %q (reason %q), want %q", result.ReasonCode, result.Reason, row.wantCode)
			}
			if strings.TrimSpace(result.Reason) == "" {
				t.Fatalf("reason is blank for %s", result.ReasonCode)
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

// --- G5: inventory errors stay distinguishable from empty data --------------

func TestConstructionExpandSourceCapabilityNamesInventoryFailureDistinctly(t *testing.T) {
	harness := newFilterSourceHarness(t)
	snapshot := harness.snapshot
	snapshot.Candidates = append(snapshot.Candidates, expandTestPatientArrayCandidate("c-photo-array", "photo[]", "string", "Photo attachment", true))
	inventoryErr := errors.New("row-choice inventory store unreachable: capability-store-dsn=secret-token")
	service := expandTestService(failingRowSourcePlanner{err: inventoryErr})
	_, err := service.constructionExpandSourceCapability(context.Background(), expandTestPatientBase(snapshot, authoringv2.Construction{Version: authoringv2.ConstructionVersion}), expandTestOutput, recipe.ConstructionSourceProjectionID)
	if err == nil {
		t.Fatal("expand source capability with a failing planner returned no error")
	}
	if code := lifecycleErrorCode(err); code != "EXPAND_SOURCE_INVENTORY_UNAVAILABLE" {
		t.Fatalf("inventory failure code = %q, want EXPAND_SOURCE_INVENTORY_UNAVAILABLE (err %v)", code, err)
	}
}

// --- G6: request-boundary validation ----------------------------------------

func TestConstructionExpandSourceSelectionsRejectMalformedInputAtRequestBoundary(t *testing.T) {
	base := func() ConstructionProposalRequest {
		return ConstructionProposalRequest{
			Project: filterSourceProject, ExplorerID: filterSourceExplorer, SnapshotToken: "expand-source-token",
			ExpectedDraftVersion: 1, ExpectedDraftDigest: "sha256:expand-source-draft", OutputID: expandTestOutput,
			ChangedStepID: expandTestStep, CandidateConstruction: authoringv2.Construction{Version: authoringv2.ConstructionVersion},
		}
	}
	rows := []struct {
		name      string
		sources   []ConstructionExpandSourceSelection
		wantError string
	}{
		{"blank choiceId", []ConstructionExpandSourceSelection{{ChoiceID: "", ColumnID: expandTestInput}}, "expandSources[0].choiceId"},
		{"blank columnId", []ConstructionExpandSourceSelection{{ChoiceID: "row-choice-a", ColumnID: ""}}, "expandSources[0].columnId"},
		{"duplicate choiceId", []ConstructionExpandSourceSelection{
			{ChoiceID: "row-choice-a", ColumnID: "col-a"}, {ChoiceID: "row-choice-a", ColumnID: "col-b"},
		}, "duplicate choiceId or columnId"},
		{"duplicate columnId", []ConstructionExpandSourceSelection{
			{ChoiceID: "row-choice-a", ColumnID: "col-a"}, {ChoiceID: "row-choice-b", ColumnID: "col-a"},
		}, "duplicate choiceId or columnId"},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			request := base()
			request.ExpandSources = row.sources
			err := request.Validate()
			if err == nil || !strings.Contains(err.Error(), row.wantError) {
				t.Fatalf("request validation error = %v, want it to name %q", err, row.wantError)
			}
		})
	}
}

// --- G7-G10: candidate-prep refusals that fire before resolve ---------------

func TestConstructionExpandSourceCandidatePrepIgnoresChannelWithoutExpandChange(t *testing.T) {
	harness := newFilterSourceHarness(t)
	service := harness.service
	base := expandTestPatientBase(harness.snapshot, authoringv2.Construction{Version: authoringv2.ConstructionVersion})
	carrier := deriveSourceLiteralOnlyStep(expandTestCarrier, append(deriveSourcePatientIdentityColumns(), authoringv2.StageColumn{ID: "derived_total", Name: "derived_total", Label: "Derived total", Type: "integer"}))
	carrier.Operation.Derive.OutputColumnID = "derived_total"
	candidate := authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{carrier}}
	unchanged, err := service.constructionCandidateWithExpandSources(context.Background(), base, ConstructionProposalRequest{
		ChangedStepID: expandTestCarrier, CandidateConstruction: candidate,
		ExpandSources: []ConstructionExpandSourceSelection{{ChoiceID: "row-choice-a", ColumnID: expandTestInput}},
	}, candidate)
	if err != nil {
		t.Fatalf("expand channel for a non-expand change: %v", err)
	}
	if !reflect.DeepEqual(unchanged, candidate) {
		t.Fatalf("non-expand candidate changed: %#v", unchanged)
	}
	// A nil channel is likewise a no-op on an expand step.
	step := expandTestExpandStep(expandTestInput, expandTestCarrier)
	withExpand := authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{carrier, step}}
	untouched, err := service.constructionCandidateWithExpandSources(context.Background(), base, ConstructionProposalRequest{
		ChangedStepID: expandTestStep, CandidateConstruction: withExpand,
	}, withExpand)
	if err != nil {
		t.Fatalf("nil expand channel: %v", err)
	}
	if !reflect.DeepEqual(untouched, withExpand) {
		t.Fatalf("nil expand channel changed the candidate: %#v", untouched)
	}
}

func TestConstructionExpandSourceCandidatePrepRequiresExpandPayload(t *testing.T) {
	harness := newFilterSourceHarness(t)
	step := expandTestExpandStep(expandTestInput, expandTestCarrier)
	step.Operation.Expand = nil // kind says Expand, payload absent
	candidate := authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{step}}
	_, err := harness.service.constructionCandidateWithExpandSources(context.Background(),
		expandTestPatientBase(harness.snapshot, authoringv2.Construction{Version: authoringv2.ConstructionVersion}),
		ConstructionProposalRequest{
			ChangedStepID: expandTestStep, CandidateConstruction: candidate,
			ExpandSources: []ConstructionExpandSourceSelection{{ChoiceID: "row-choice-a", ColumnID: expandTestInput}},
		}, candidate)
	if err == nil || lifecycleErrorCode(err) != "EXPAND_SOURCE_REQUIRES_EXPAND" {
		t.Fatalf("nil expand payload error = %v, want EXPAND_SOURCE_REQUIRES_EXPAND", err)
	}
}

func TestConstructionExpandSourceCandidatePrepClearsChannelAndSignedPayload(t *testing.T) {
	harness := newFilterSourceHarness(t)
	saved := authoringv2.Construction{
		Version: authoringv2.ConstructionVersion,
		Steps:   []authoringv2.ConstructionStep{expandTestExpandStep(expandTestInput, "")},
		SourceProjections: []authoringv2.ConstructionSourceProjection{{
			ColumnID: expandTestInput, OwnerStepID: expandTestStep, OccurrenceID: authoringv2.RootOccurrenceID,
			FieldPath: "photo[]", FHIRType: "string", LogicalType: "string", Label: "Photo attachment",
		}},
	}
	saved.Steps[0].Operation.Expand.ExpandedField = expandTestSignedPayload("photo[]", "string", "string")
	saved.Steps[0].Outputs = append(saved.Steps[0].Outputs, authoringv2.StageColumn{
		ID: expandTestInput, Name: authoringv2.ConstructionSourceProjectionName(expandTestInput), Label: "Photo attachment", Type: "string",
	})
	cleared, err := harness.service.constructionCandidateWithExpandSources(context.Background(),
		expandTestPatientBase(harness.snapshot, saved),
		ConstructionProposalRequest{
			ChangedStepID: expandTestStep, CandidateConstruction: saved,
			ExpandSources: []ConstructionExpandSourceSelection{},
		}, saved)
	if err != nil {
		t.Fatalf("clear the expand source channel: %v", err)
	}
	if len(cleared.SourceProjections) != 0 {
		t.Fatalf("cleared candidate kept projections: %#v", cleared.SourceProjections)
	}
	for _, output := range cleared.Steps[0].Outputs {
		if output.ID == expandTestInput {
			t.Fatalf("cleared expand step still carries the owned column: %#v", cleared.Steps[0].Outputs)
		}
	}
	if cleared.Steps[0].Operation.Expand.ExpandedField != nil {
		t.Fatalf("cleared channel left a signed payload behind: %#v", cleared.Steps[0].Operation.Expand.ExpandedField)
	}
}

func TestConstructionExpandSourceCandidatePrepRefusesColumnAndStageMisbinds(t *testing.T) {
	harness := newFilterSourceHarness(t)
	base := expandTestPatientBase(harness.snapshot, authoringv2.Construction{Version: authoringv2.ConstructionVersion})
	step := expandTestExpandStep(expandTestInput, "")

	twoSelections := func() ConstructionProposalRequest {
		return ConstructionProposalRequest{ChangedStepID: expandTestStep, CandidateConstruction: authoringv2.Construction{
			Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{step},
		}, ExpandSources: []ConstructionExpandSourceSelection{
			{ChoiceID: "row-choice-a", ColumnID: expandTestInput},
			{ChoiceID: "row-choice-b", ColumnID: "extra_column"},
		}}
	}
	if _, err := harness.service.constructionCandidateWithExpandSources(context.Background(), base, twoSelections(), twoSelections().CandidateConstruction); err == nil || lifecycleErrorCode(err) != "EXPAND_SOURCE_COLUMN_MISMATCH" {
		t.Fatalf("two selections error = %v, want EXPAND_SOURCE_COLUMN_MISMATCH", err)
	}

	wrongColumn := twoSelections()
	wrongColumn.ExpandSources = []ConstructionExpandSourceSelection{{ChoiceID: "row-choice-a", ColumnID: "not_the_input"}}
	if _, err := harness.service.constructionCandidateWithExpandSources(context.Background(), base, wrongColumn, wrongColumn.CandidateConstruction); err == nil || lifecycleErrorCode(err) != "EXPAND_SOURCE_COLUMN_MISMATCH" {
		t.Fatalf("foreign columnId error = %v, want EXPAND_SOURCE_COLUMN_MISMATCH", err)
	}

	// No resolvable input stage: the expand step declares no inputs.
	orphan := step
	orphan.Inputs = nil
	orphanCandidate := authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{orphan}}
	orphanRequest := ConstructionProposalRequest{ChangedStepID: expandTestStep, CandidateConstruction: orphanCandidate,
		ExpandSources: []ConstructionExpandSourceSelection{{ChoiceID: "row-choice-a", ColumnID: expandTestInput}}}
	if _, err := harness.service.constructionCandidateWithExpandSources(context.Background(), base, orphanRequest, orphanCandidate); err == nil || lifecycleErrorCode(err) != "EXPAND_SOURCE_STAGE_UNAVAILABLE" {
		t.Fatalf("unresolvable input stage error = %v, want EXPAND_SOURCE_STAGE_UNAVAILABLE", err)
	}

	// Input stage bound to a stage absent from the accepted receipt.
	staleStageRequest := twoSelections()
	staleStageRequest.ExpandSources = []ConstructionExpandSourceSelection{{ChoiceID: "row-choice-a", ColumnID: expandTestInput}}
	carrierless := authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{expandTestExpandStep(expandTestInput, expandTestCarrier)}}
	carrierlessRequest := ConstructionProposalRequest{ChangedStepID: expandTestStep, CandidateConstruction: carrierless,
		ExpandSources: []ConstructionExpandSourceSelection{{ChoiceID: "row-choice-a", ColumnID: expandTestInput}}}
	if _, err := harness.service.constructionCandidateWithExpandSources(context.Background(), base, carrierlessRequest, carrierless); err == nil || lifecycleErrorCode(err) != "STALE_STAGE_REFERENCE" {
		t.Fatalf("unbound stage error = %v, want STALE_STAGE_REFERENCE", err)
	}
}

// --- G11-G12: forged or cross-snapshot tokens are refused -------------------

func TestProposeConstructionExpandSourceRejectsForgedChoiceID(t *testing.T) {
	harness := newFilterSourceHarness(t)
	carrier := deriveSourceLiteralOnlyStep(expandTestCarrier, append(deriveSourcePatientIdentityColumns(), authoringv2.StageColumn{ID: "derived_total", Name: "derived_total", Label: "Derived total", Type: "integer"}))
	carrier.Operation.Derive.OutputColumnID = "derived_total"
	harness.saveDraft(t, authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{carrier}})
	candidate := authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{
		carrier, expandTestExpandStep(expandTestInput, expandTestCarrier),
	}}
	_, err := harness.service.ProposeConstruction(context.Background(), ConstructionProposalRequest{
		Project: harness.store.created.Project, ExplorerID: harness.store.created.ExplorerID, SnapshotToken: harness.snapshot.Token,
		ExpectedDraftVersion: harness.store.created.DraftVersion, ExpectedDraftDigest: harness.store.created.DraftDigest, OutputID: expandTestOutput,
		ChangedStepID:         expandTestStep,
		CandidateConstruction: candidate,
		ExpandSources:         []ConstructionExpandSourceSelection{{ChoiceID: "expandsource-forged-choice-000001", ColumnID: expandTestInput}},
	})
	if err == nil || lifecycleErrorCode(err) != "INVALID_EXPAND_SOURCE_CHOICE" {
		t.Fatalf("forged expand choice error = %v, want INVALID_EXPAND_SOURCE_CHOICE", err)
	}
}

// Adversarial review finding (coordinator-reproduced): the candidate-side
// projection used to establish ownership, so a client editing the EXPAND step
// with a nil expandSources channel could author BOTH the expand-owned
// sourceProjection and a self-consistent signed payload and keep them through
// prep, prune, and document validation — a signed lane with no choiceId ever
// presented. Ownership of an expand input is now server-derived only: this
// shape must come out payload-stripped and projection-dropped.
func TestProposeConstructionExpandSourceRefusesClientAuthoredLaneFacts(t *testing.T) {
	snapshot := expandTestProcedureSnapshot("tok")
	base := expandTestProcedureBase(snapshot, authoringv2.Construction{Version: authoringv2.ConstructionVersion})
	step := expandTestExpandStep("uri_column", "")
	step.Operation.Expand.ExpandedField = expandTestSignedPayload("instantiatesUri[]", "string", "string")
	step.Outputs = append([]authoringv2.StageColumn{
		{ID: "procedure-id", Name: "procedure_id", Label: "Procedure ID", Type: "string"},
	}, step.Outputs...)
	clientProjection := authoringv2.ConstructionSourceProjection{
		ColumnID: "uri_column", OwnerStepID: expandTestStep, OccurrenceID: authoringv2.RootOccurrenceID,
		FieldPath: "instantiatesUri[]", FHIRType: "string", LogicalType: "string", Label: "FORGED-LABEL",
	}
	candidate := authoringv2.Construction{
		Version:           authoringv2.ConstructionVersion,
		Steps:             []authoringv2.ConstructionStep{step},
		SourceProjections: []authoringv2.ConstructionSourceProjection{clientProjection},
	}
	// nil planner: a genuine lane resolve would refuse outright, so anything
	// surviving prep cannot have passed the mint.
	out, err := expandTestService(nil).constructionCandidateWithExpandSources(context.Background(), base, ConstructionProposalRequest{
		Project: filterSourceProject, ExplorerID: "explorer-a", OutputID: "procedures",
		ChangedStepID: expandTestStep, CandidateConstruction: candidate,
	}, candidate)
	if err != nil {
		return // a refusal is also a sound fail-closed outcome
	}
	if out.Steps[0].Operation.Expand.ExpandedField != nil {
		t.Fatalf("client-authored signed payload survived prep with no choiceId and no planner: %+v", *out.Steps[0].Operation.Expand.ExpandedField)
	}
	for _, projection := range out.SourceProjections {
		if projection.OwnerStepID == expandTestStep {
			t.Fatalf("client-authored expand-owned projection survived prep: %+v", projection)
		}
	}
}

func TestProposeConstructionExpandSourceRejectsChoiceFromAnotherSnapshot(t *testing.T) {
	harness := newFilterSourceHarness(t)
	carrier := deriveSourceLiteralOnlyStep(expandTestCarrier, append(deriveSourcePatientIdentityColumns(), authoringv2.StageColumn{ID: "derived_total", Name: "derived_total", Label: "Derived total", Type: "integer"}))
	carrier.Operation.Derive.OutputColumnID = "derived_total"
	harness.saveDraft(t, authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{carrier}})
	// A genuinely signed ARRAY/MANY token, but issued for a different dataset
	// generation than the proposal's snapshot.
	issued := rowSourceSnapshotWithGeneration(harness.snapshot, harness.snapshot.Identity.Generation+"-issued")
	stale := expandTestMintArrayChoice(t, issued, "node-patient", "Patient", "photo[]", "string")
	candidate := authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{
		carrier, expandTestExpandStep(expandTestInput, expandTestCarrier),
	}}
	_, err := harness.service.ProposeConstruction(context.Background(), ConstructionProposalRequest{
		Project: harness.store.created.Project, ExplorerID: harness.store.created.ExplorerID, SnapshotToken: harness.snapshot.Token,
		ExpectedDraftVersion: harness.store.created.DraftVersion, ExpectedDraftDigest: harness.store.created.DraftDigest, OutputID: expandTestOutput,
		ChangedStepID:         expandTestStep,
		CandidateConstruction: candidate,
		ExpandSources:         []ConstructionExpandSourceSelection{{ChoiceID: stale.ChoiceID, ColumnID: expandTestInput}},
	})
	if err == nil || lifecycleErrorCode(err) != "INVALID_EXPAND_SOURCE_CHOICE" {
		t.Fatalf("cross-snapshot expand choice error = %v, want INVALID_EXPAND_SOURCE_CHOICE", err)
	}
}

// --- G13: server-side fact derivation ---------------------------------------

func TestConstructionExpandSourceFactsDerivesServerSignedArrayProjection(t *testing.T) {
	snapshot := expandTestProcedureSnapshot("expand-facts-procedure")
	// Procedure.instantiatesUri[] is a generated primitive ROOT ARRAY:
	// canonical instantiatesUri[], ARRAY/MANY, FHIRType string, non-reference.
	facts, ok := ConstructionExpandSourceFacts(snapshot, "Procedure", "instantiatesUri[]")
	if !ok {
		t.Fatalf("facts for a populated generated primitive array were refused")
	}
	if facts.OccurrenceID != authoringv2.RootOccurrenceID || facts.FieldPath != "instantiatesUri[]" ||
		facts.FHIRType != "string" || facts.LogicalType != "string" || facts.Label != "Instantiates uri" {
		t.Fatalf("derived facts = %#v, want server-derived string-array facts with the candidate label", facts)
	}
	// The column and owner identities are claim-sourced (left empty here),
	// exactly as Apply-time DeepEquals expects.
	if facts.ColumnID != "" || facts.OwnerStepID != "" {
		t.Fatalf("facts leaked claim identities: %#v", facts)
	}

	negative := []struct {
		name       string
		rootType   string
		path       string
		candidates []capability.Candidate
	}{
		{"reference array", "Patient", "generalPractitioner[]", []capability.Candidate{expandTestPatientArrayCandidate("c-gp", "generalPractitioner[]", "string", "General practitioner", true)}},
		{"nested-backed array (boundary at a foreign path)", "Procedure", "instantiatesUri[]", []capability.Candidate{
			{ID: "c_procedure_uri", NodeID: "procedure", ResourceType: "Procedure", FieldPath: "instantiatesUri[]", Label: "Instantiates uri",
				LogicalType: "string", Cardinality: "many", ProjectionModes: []capability.ProjectionMode{capability.ProjectionArray, capability.ProjectionFirst, capability.ProjectionDistinctArray},
				RepeatedBoundaries: []capability.RepeatedBoundary{{Path: "performer[]", MaxItems: 2}}, Observed: true, Populated: true, ObservedDocumentCount: 4},
		}},
		{"unpopulated array", "Procedure", "instantiatesUri[]", []capability.Candidate{
			{ID: "c_procedure_uri", NodeID: "procedure", ResourceType: "Procedure", FieldPath: "instantiatesUri[]", Label: "Instantiates uri",
				LogicalType: "string", Cardinality: "many", ProjectionModes: []capability.ProjectionMode{capability.ProjectionArray, capability.ProjectionFirst, capability.ProjectionDistinctArray}, SupportedOperations: []capability.Operation{capability.OperationSelect}},
		}},
		{"scalar path", "Procedure", "status", []capability.Candidate{rowSourceLaneCandidate("procedure", "Procedure", "c_status", "status", "string", "optional_one", true)}},
		{"Coding element array", "Patient", "meta.tag[]", []capability.Candidate{expandTestPatientArrayCandidate("c-tag", "meta.tag[]", "string", "Tag", true)}},
		{"unknown path", "Procedure", "noSuchArray[]", nil},
	}
	for _, row := range negative {
		t.Run(row.name, func(t *testing.T) {
			candidates := row.candidates
			if candidates == nil {
				candidates = expandTestProcedureSnapshot("expand-facts-negative").Candidates
			}
			refused := capability.Snapshot{}
			refused.Candidates = candidates
			if row.rootType == "Patient" {
				refused.Nodes = []capability.Node{{ID: "node-patient", ResourceType: "Patient", RowRootEligible: true, RowGrain: "RESOURCE", Populated: true}}
			} else {
				refused.Nodes = []capability.Node{{ID: "procedure", ResourceType: "Procedure", RowRootEligible: true, RowGrain: "RESOURCE", Populated: true}}
			}
			if _, ok := ConstructionExpandSourceFacts(refused, row.rootType, row.path); ok {
				t.Fatalf("facts accepted a non-flattenable %s %s", row.rootType, row.path)
			}
		})
	}
}

// --- G14-G16: Apply-time reauthorization ------------------------------------

func expandTestLiveProcedureConstruction(payload *authoringv2.ConstructionExpandedFieldSource) (authoringv2.Construction, capability.Snapshot) {
	snapshot := expandTestProcedureSnapshot("expand-reauth-procedure")
	step := expandTestExpandStep(expandTestInput, "")
	step.Operation.Expand.ExpandedField = payload
	return authoringv2.Construction{
		Version: authoringv2.ConstructionVersion,
		Steps:   []authoringv2.ConstructionStep{step},
		SourceProjections: []authoringv2.ConstructionSourceProjection{{
			ColumnID: expandTestInput, OwnerStepID: expandTestStep, OccurrenceID: authoringv2.RootOccurrenceID,
			FieldPath: "instantiatesUri[]", FHIRType: "string", LogicalType: "string", Label: "Instantiates uri",
		}},
	}, snapshot
}

func expandTestAuthorizedRow() capability.RowChoice {
	return capability.RowChoice{
		ChoiceID: "row-choice-expand-live-000001", Kind: capability.RowChoiceExpandedScope,
		Label: "Instantiates uri", OccurrenceID: authoringv2.RootOccurrenceID, NodeID: "procedure",
		ResourceType: "Procedure", Path: "instantiatesUri[]", FHIRType: "string",
		Cardinality: capability.RowChoiceMany, Reference: false,
	}
}

func TestReauthorizeConstructionExpandSourcesCurrentBindingPasses(t *testing.T) {
	construction, snapshot := expandTestLiveProcedureConstruction(expandTestSignedPayload("instantiatesUri[]", "string", "string"))
	service := expandTestService(expandTestPlanner{choices: []capability.RowChoice{expandTestAuthorizedRow()}})
	if err := service.reauthorizeConstructionExpandSources(context.Background(), expandTestProcedureBase(snapshot, construction), "procedures", construction); err != nil {
		t.Fatalf("reauthorize a current expand binding: %v", err)
	}
}

func TestReauthorizeConstructionExpandSourcesRefusesDrift(t *testing.T) {
	service := expandTestService(expandTestPlanner{choices: []capability.RowChoice{expandTestAuthorizedRow()}})
	rows := []struct {
		name    string
		mutate  func(*authoringv2.Construction, *capability.Snapshot)
		wantMsg string
	}{
		{
			name: "payload logical type stops matching the live element type",
			mutate: func(c *authoringv2.Construction, _ *capability.Snapshot) {
				c.Steps[0].Operation.Expand.ExpandedField.LogicalType = "integer"
			},
			wantMsg: "no longer matches the live authorized root array field behind its Expand payload",
		},
		{
			name: "payload path is forged to another field",
			mutate: func(c *authoringv2.Construction, _ *capability.Snapshot) {
				c.Steps[0].Operation.Expand.ExpandedField.Path = "instantiatesCanonical[]"
			},
			wantMsg: "no longer matches the live authorized root array field behind its Expand payload",
		},
		{
			name: "projection claim no longer matches the server derivation",
			mutate: func(c *authoringv2.Construction, _ *capability.Snapshot) {
				c.SourceProjections[0].Label = "Stale claim label"
			},
			wantMsg: "no longer matches an authorized populated root primitive array field",
		},
		{
			name: "the field stops presenting observed values",
			mutate: func(_ *authoringv2.Construction, s *capability.Snapshot) {
				for index := range s.Candidates {
					if s.Candidates[index].FieldPath == "instantiatesUri[]" {
						s.Candidates[index].Populated = false
						s.Candidates[index].Observed = false
					}
				}
			},
			wantMsg: "no longer matches an authorized populated root primitive array field",
		},
		{
			name: "the owner step points the projection at another input",
			mutate: func(c *authoringv2.Construction, _ *capability.Snapshot) {
				c.Steps[0].Operation.Expand.InputColumnID = "another_column"
			},
			wantMsg: "is not the input column of its owner Expand step",
		},
		{
			name: "the owner step disappears",
			mutate: func(c *authoringv2.Construction, _ *capability.Snapshot) {
				c.Steps = nil
			},
			wantMsg: "names a missing owner step",
		},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			construction, snapshot := expandTestLiveProcedureConstruction(expandTestSignedPayload("instantiatesUri[]", "string", "string"))
			row.mutate(&construction, &snapshot)
			err := service.reauthorizeConstructionExpandSources(context.Background(), expandTestProcedureBase(snapshot, construction), "procedures", construction)
			if err == nil || !strings.Contains(err.Error(), row.wantMsg) {
				t.Fatalf("reauthorize error = %v, want it to report %q", err, row.wantMsg)
			}
		})
	}
}

func TestReauthorizeConstructionExpandSourcesRefusesWithoutPlanner(t *testing.T) {
	construction, snapshot := expandTestLiveProcedureConstruction(expandTestSignedPayload("instantiatesUri[]", "string", "string"))
	service := expandTestService(nil)
	err := service.reauthorizeConstructionExpandSources(context.Background(), expandTestProcedureBase(snapshot, construction), "procedures", construction)
	if err == nil || lifecycleErrorCode(err) != "ROW_CHOICE_UNAVAILABLE" {
		t.Fatalf("reauthorize without a planner = %v, want ROW_CHOICE_UNAVAILABLE", err)
	}
}

// BUG-003 — expected failure: a lane-owned expand input whose step lost its
// server-signed payload must be refused fail-closed, never panic.
// expand_source.go:697-700 admits the nil payload because
// authoringv2.ValidateConstructionExpandedFieldSource explicitly treats nil as
// valid (construction.go:1093-1095 — nil is legal for client candidates), and
// line 721 then dereferences signed.Path unconditionally:
//
//	panic: runtime error: invalid memory address or nil pointer dereference
//	  expand_source.go:721 inside reauthorizeConstructionExpandSources
//
// Every durable expand-owned projection therefore carries a crash path into
// ApplyCommands the moment its payload is stripped — exactly the state the
// comment at expand_source.go:693-696 promises to refuse ("a lane-owned input
// whose step lost (or forged) its server-signed ExpandedField never reaches
// the claim-vs-redo DeepEqual below"). The recover block below keeps the
// rest of the suite alive; the test fails today because the guard fires as a
// crash rather than as the promised refusal.
func TestReauthorizeConstructionExpandSourcesKeepsFailClosedWhenPayloadStripped(t *testing.T) {
	construction, snapshot := expandTestLiveProcedureConstruction(expandTestSignedPayload("instantiatesUri[]", "string", "string"))
	construction.Steps[0].Operation.Expand.ExpandedField = nil
	service := expandTestService(expandTestPlanner{choices: []capability.RowChoice{expandTestAuthorizedRow()}})
	var recovered any
	func() {
		defer func() { recovered = recover() }()
		err := service.reauthorizeConstructionExpandSources(context.Background(), expandTestProcedureBase(snapshot, construction), "procedures", construction)
		if err != nil && strings.Contains(err.Error(), "no server-consistent Expand payload") {
			return
		}
		recovered = "unexpected outcome"
		if err == nil {
			recovered = "a stripped payload was accepted without refusal"
		}
	}()
	if recovered != nil {
		t.Fatalf("BUG-003: reauthorize with a stripped Expand payload did not refuse fail-closed: %v", recovered)
	}
}

// --- Server-authoritative acceptance ----------------------------------------
//
// These tests drive ProposeConstruction/ApplyCommands on a Procedure-rooted
// table. The generated FHIR schema carries no admissible primitive root array
// on the Patient fixture (photo[] is Attachment, meta.tag[]/security[] are
// Coding, meta.profile[] is a reference), so Procedure.instantiatesUri[] —
// the only canonical generated ARRAY/MANY non-reference primitive the lane
// can honestly sign — is the acceptance carrier, mirroring the compile
// package's EXPAND fixtures.

// newExpandProcedureHarness reuses the live-compiler proposal harness around
// a Procedure-rooted saved table with the real planner/resolver on the
// generated schema.
func newExpandProcedureHarness(t *testing.T) *filterSourceHarness {
	t.Helper()
	h := newFilterSourceHarness(t)
	// Reuse the harness's accepted authorization scope so the procedure
	// snapshot stays compatible with the configured read-scope check.
	scopeDigest := h.snapshot.Identity.AuthorizationScopeDigest
	shapeDigest := h.snapshot.Identity.ShapeDigest
	h.snapshot = expandTestProcedureSnapshot("expand-procedure-acceptance")
	h.snapshot.Identity.AuthorizationScopeDigest = scopeDigest
	h.snapshot.Identity.ShapeDigest = shapeDigest
	h.service.config.Capability.Catalog = func(got capability.Snapshot, explorerID string) authoringv2.CatalogSnapshot {
		catalog := authoringv2.CatalogSnapshot{
			APIVersion: authoringv2.APIVersion, Kind: authoringv2.CatalogKind, Project: got.Identity.Project,
			ExplorerID: explorerID, SourceGeneration: got.Identity.Generation,
			AuthorizationScopeDigest: got.Identity.AuthorizationScopeDigest, SnapshotToken: got.Token,
			Complete: true, RoutePolicy: authoringv2.RoutePolicy{Unbounded: true},
			Nodes: []authoringv2.CatalogNode{{ID: "procedure", ResourceType: "Procedure", RowRootEligible: true, RowGrain: "procedure"}},
		}
		for _, candidate := range h.snapshot.Candidates {
			choice, choiceErr := capability.NewFieldConstructionChoice(got.Token, candidate)
			if choiceErr != nil {
				t.Fatalf("make procedure source choice for %s: %v", candidate.FieldPath, choiceErr)
			}
			catalog.Candidates = append(catalog.Candidates, authoringv2.CatalogCandidate{
				ID: candidate.ID, NodeID: candidate.NodeID, FieldPath: candidate.FieldPath, Label: candidate.Label,
				LogicalType: candidate.LogicalType, Cardinality: candidate.Cardinality,
				ProjectionModes: []string{"VALUE"}, DefaultProjectionMode: "VALUE", ConstructionChoice: &choice,
			})
		}
		return catalog
	}
	workspace, err := authoringv2.DecodeWorkspace(h.store.created.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	document := expandTestProcedureDocument()
	document.Construction = &authoringv2.Construction{Version: authoringv2.ConstructionVersion}
	workspace.Documents[0] = document
	if len(workspace.Tabs) != 1 {
		t.Fatalf("procedure tab remap expects one tab, got %#v", workspace.Tabs)
	}
	workspace.Tabs[0] = authoringv2.Tab{ID: document.Output.ID, Title: "Procedures", OutputID: document.Output.ID, Order: 0, Visible: true}
	if err := workspace.Validate(); err != nil {
		t.Fatalf("validate procedure draft: %v", err)
	}
	draft, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	h.store.created.DraftConfig, h.store.created.DraftDigest = draft, digest
	return h
}

func expandTestProcedureCarrier() authoringv2.ConstructionStep {
	outputs := []authoringv2.StageColumn{
		{ID: "procedure-id", Name: "procedure_id", Label: "Procedure ID", Type: "string"},
		{ID: "derived_total", Name: "derived_total", Label: "Derived total", Type: "integer"},
	}
	carrier := deriveSourceLiteralOnlyStep(expandTestCarrier, outputs)
	carrier.Operation.Derive.OutputColumnID = "derived_total"
	return carrier
}

// expandTestProcedureExpandStep declares the full step schema the authoring
// contract requires: every carried input column except the expanded array
// itself, plus the item column.
func expandTestProcedureExpandStep(previousStep string, carried []authoringv2.StageColumn) authoringv2.ConstructionStep {
	step := expandTestExpandStep(expandTestInput, previousStep)
	outputs := make([]authoringv2.StageColumn, 0, len(carried)+1)
	for _, column := range carried {
		if column.ID == expandTestInput {
			continue
		}
		outputs = append(outputs, column)
	}
	outputs = append(outputs, authoringv2.StageColumn{ID: expandTestItem, Name: "expand_uri_item", Label: "Uri item", Type: "string"})
	step.Outputs = outputs
	return step
}

// Selecting the advertised array must sign the durable projection and
// overwrite any client-forged payload.
func TestProposeConstructionExpandSourceSignsServerOwnedPayload(t *testing.T) {
	harness := newExpandProcedureHarness(t)
	carrier := expandTestProcedureCarrier()
	harness.saveDraft(t, authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{carrier}})
	step := expandTestProcedureExpandStep(expandTestCarrier, carrier.Outputs)
	// A client-forged payload must be overwritten by the server derivation.
	step.Operation.Expand.ExpandedField = expandTestSignedPayload("status", "string", "code")
	candidate := authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{carrier, step}}
	choice := expandTestMintArrayChoice(t, harness.snapshot, "procedure", "Procedure", "instantiatesUri[]", "string")
	proposal, err := harness.service.ProposeConstruction(context.Background(), ConstructionProposalRequest{
		Project: harness.store.created.Project, ExplorerID: harness.store.created.ExplorerID, SnapshotToken: harness.snapshot.Token,
		ExpectedDraftVersion: harness.store.created.DraftVersion, ExpectedDraftDigest: harness.store.created.DraftDigest, OutputID: "procedures",
		ChangedStepID:         expandTestStep,
		CandidateConstruction: candidate,
		ExpandSources:         []ConstructionExpandSourceSelection{{ChoiceID: choice.ChoiceID, ColumnID: expandTestInput}},
	})
	if err != nil {
		t.Fatalf("ProposeConstruction with the advertised expand selection: %v", err)
	}
	if len(proposal.CandidateConstruction.SourceProjections) != 1 {
		t.Fatalf("proposal projections = %#v, want one durable expand-owned projection", proposal.CandidateConstruction.SourceProjections)
	}
	durable := proposal.CandidateConstruction.SourceProjections[0]
	if durable.OwnerStepID != expandTestStep || durable.ColumnID != expandTestInput || durable.FieldPath != "instantiatesUri[]" ||
		durable.FHIRType != "string" || durable.LogicalType != "string" || durable.OccurrenceID != authoringv2.RootOccurrenceID {
		t.Fatalf("durable projection = %#v, want server-derived instantiatesUri[] facts owned by the expand step", durable)
	}
	signed := proposal.CandidateConstruction.Steps[1].Operation.Expand.ExpandedField
	if signed == nil || signed.Path != "instantiatesUri[]" || signed.Cardinality != authoringv2.ConstructionExpandedFieldCardinalityMany ||
		signed.FHIRType != "string" || signed.LogicalType != "string" {
		t.Fatalf("client-forged payload survived: %#v, want the server-overwritten instantiatesUri[] signature", signed)
	}
}

// The row-preserving prefix must gain the array column so the expand input
// stage can present it, and the expand step must not re-declare its own
// input (the lowerer refuses that).
func TestProposeConstructionExpandSourceCarriesArrayThroughCarryingPrefix(t *testing.T) {
	harness := newExpandProcedureHarness(t)
	carrier := expandTestProcedureCarrier()
	harness.saveDraft(t, authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{carrier}})
	candidate := authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{
		carrier, expandTestProcedureExpandStep(expandTestCarrier, carrier.Outputs),
	}}
	choice := expandTestMintArrayChoice(t, harness.snapshot, "procedure", "Procedure", "instantiatesUri[]", "string")
	proposal, err := harness.service.ProposeConstruction(context.Background(), ConstructionProposalRequest{
		Project: harness.store.created.Project, ExplorerID: harness.store.created.ExplorerID, SnapshotToken: harness.snapshot.Token,
		ExpectedDraftVersion: harness.store.created.DraftVersion, ExpectedDraftDigest: harness.store.created.DraftDigest, OutputID: "procedures",
		ChangedStepID:         expandTestStep,
		CandidateConstruction: candidate,
		ExpandSources:         []ConstructionExpandSourceSelection{{ChoiceID: choice.ChoiceID, ColumnID: expandTestInput}},
	})
	if err != nil {
		t.Fatalf("ProposeConstruction carrying an expand array through the DERIVE prefix: %v", err)
	}
	carrierOutputs := proposal.CandidateConstruction.Steps[0].Outputs
	for _, output := range carrierOutputs {
		if output.ID == expandTestInput {
			if output.Name != authoringv2.ConstructionSourceProjectionName(expandTestInput) {
				t.Fatalf("carried array output name = %q, want the durable source-projection name", output.Name)
			}
			return
		}
	}
	t.Fatalf("carrier prefix outputs must include the carried array column %#v", carrierOutputs)
}

// A row-changing (UNPIVOT) step earlier in the prefix must refuse to
// transport the array into the expand input
// (ConstructionCanCarrySourceProjection excludes UNPIVOT).
func TestProposeConstructionExpandSourceRefusesCarryingThroughRowChangingPrefix(t *testing.T) {
	harness := newExpandProcedureHarness(t)
	melt := unpivotMeltStep("melt-prefix", []string{"procedure-id"}, unpivotMeltOutputs())
	harness.saveDraft(t, authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{melt}})
	carried := append([]authoringv2.StageColumn(nil), melt.Outputs...)
	candidate := authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{
		melt, expandTestProcedureExpandStep("melt-prefix", carried),
	}}
	choice := expandTestMintArrayChoice(t, harness.snapshot, "procedure", "Procedure", "instantiatesUri[]", "string")
	_, err := harness.service.ProposeConstruction(context.Background(), ConstructionProposalRequest{
		Project: harness.store.created.Project, ExplorerID: harness.store.created.ExplorerID, SnapshotToken: harness.snapshot.Token,
		ExpectedDraftVersion: harness.store.created.DraftVersion, ExpectedDraftDigest: harness.store.created.DraftDigest, OutputID: "procedures",
		ChangedStepID:         expandTestStep,
		CandidateConstruction: candidate,
		ExpandSources:         []ConstructionExpandSourceSelection{{ChoiceID: choice.ChoiceID, ColumnID: expandTestInput}},
	})
	if err == nil || lifecycleErrorCode(err) != "EXPAND_SOURCE_CANNOT_CROSS_STAGE" {
		t.Fatalf("row-changing prefix error = %v, want EXPAND_SOURCE_CANNOT_CROSS_STAGE", err)
	}
}

// BUG-001 — expected failure: selecting a Coding-element array must be refused
// by name as an unsupported expand source field, not as an invalid choice.
func TestProposeConstructionExpandSourceRefusesCodingElementArray(t *testing.T) {
	harness := newFilterSourceHarness(t)
	snapshot := harness.snapshot
	snapshot.Candidates = append(snapshot.Candidates, expandTestPatientArrayCandidate("c-tag", "meta.tag[]", "string", "Tag", true))
	harness.snapshot = snapshot
	carrier := deriveSourceLiteralOnlyStep(expandTestCarrier, append(deriveSourcePatientIdentityColumns(), authoringv2.StageColumn{ID: "derived_total", Name: "derived_total", Label: "Derived total", Type: "integer"}))
	carrier.Operation.Derive.OutputColumnID = "derived_total"
	harness.saveDraft(t, authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{carrier}})
	candidate := authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{
		carrier, expandTestExpandStep(expandTestInput, expandTestCarrier),
	}}
	// meta.tag[] is a generated ARRAY/MANY non-reference element of FHIRType
	// Coding: the lane's generated-schema gate must refuse it (its mint is
	// legal — ARRAY/MANY — so reaching any other code hides the defect).
	choice := expandTestMintArrayChoice(t, harness.snapshot, "node-patient", "Patient", "meta.tag[]", "Coding")
	_, err := harness.service.ProposeConstruction(context.Background(), ConstructionProposalRequest{
		Project: harness.store.created.Project, ExplorerID: harness.store.created.ExplorerID, SnapshotToken: harness.snapshot.Token,
		ExpectedDraftVersion: harness.store.created.DraftVersion, ExpectedDraftDigest: harness.store.created.DraftDigest, OutputID: expandTestOutput,
		ChangedStepID:         expandTestStep,
		CandidateConstruction: candidate,
		ExpandSources:         []ConstructionExpandSourceSelection{{ChoiceID: choice.ChoiceID, ColumnID: expandTestInput}},
	})
	if err == nil || lifecycleErrorCode(err) != "UNSUPPORTED_EXPAND_SOURCE_FIELD" {
		t.Fatalf("BUG-001: Coding array error = %v, want UNSUPPORTED_EXPAND_SOURCE_FIELD", err)
	}
}

// --- Durable expand-owned state through Apply -------------------------------

// The accepted proposal's durable binding must be rechecked at Apply: when
// the signed array stops presenting observed values, Apply refuses with
// STALE_EXPAND_SOURCE_BINDING and leaves the draft untouched; while current,
// Apply saves the exact durable state the proposal carried.
func TestApplyConstructionProposalRefusesStaleExpandSourceBinding(t *testing.T) {
	for _, stale := range []bool{false, true} {
		name := "current"
		if stale {
			name = "stale"
		}
		t.Run(name, func(t *testing.T) {
			harness := newExpandProcedureHarness(t)
			carrier := expandTestProcedureCarrier()
			harness.saveDraft(t, authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{carrier}})
			step := expandTestProcedureExpandStep(expandTestCarrier, carrier.Outputs)
			step.Operation.Expand.ExpandedField = expandTestSignedPayload("instantiatesUri[]", "string", "string")
			candidate := authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{carrier, step}}
			choice := expandTestMintArrayChoice(t, harness.snapshot, "procedure", "Procedure", "instantiatesUri[]", "string")
			proposal, err := harness.service.ProposeConstruction(context.Background(), ConstructionProposalRequest{
				Project: harness.store.created.Project, ExplorerID: harness.store.created.ExplorerID, SnapshotToken: harness.snapshot.Token,
				ExpectedDraftVersion: harness.store.created.DraftVersion, ExpectedDraftDigest: harness.store.created.DraftDigest, OutputID: "procedures",
				ChangedStepID:         expandTestStep,
				CandidateConstruction: candidate,
				ExpandSources:         []ConstructionExpandSourceSelection{{ChoiceID: choice.ChoiceID, ColumnID: expandTestInput}},
			})
			if err != nil {
				t.Fatalf("ProposeConstruction staging the expand source binding: %v", err)
			}
			version, digest := harness.store.created.DraftVersion, harness.store.created.DraftDigest
			if stale {
				// The array stops presenting observed values after the
				// proposal was accepted, exactly like the unpivot precedent.
				for index := range harness.snapshot.Candidates {
					if harness.snapshot.Candidates[index].FieldPath == "instantiatesUri[]" {
						harness.snapshot.Candidates[index].Populated = false
						harness.snapshot.Candidates[index].Observed = false
					}
				}
			}
			_, err = harness.service.ApplyCommands(context.Background(), harness.store.created.Project, harness.store.created.ExplorerID, authoringv2.ApplyCommandsRequest{
				CommandID: "expand-source-apply", SemanticsVersion: authoringv2.CurrentSemanticsVersion, SnapshotToken: harness.snapshot.Token,
				ExpectedDraftVersion: version, ExpectedDraftDigest: digest,
				Commands: []authoringv2.Command{{Type: authoringv2.CommandApplyConstructionProposal, OutputID: "procedures", ProposalID: proposal.ProposalID}},
			}, "expand-source-actor")
			if stale {
				if err == nil || lifecycleErrorCode(err) != "STALE_EXPAND_SOURCE_BINDING" {
					t.Fatalf("stale expand source apply error = %v, want STALE_EXPAND_SOURCE_BINDING", err)
				}
				if harness.store.created.DraftVersion != version || harness.store.created.DraftDigest != digest || harness.store.saveDraftCalls != 0 {
					t.Fatalf("stale apply changed the accepted draft: version=%d digest=%q saves=%d", harness.store.created.DraftVersion, harness.store.created.DraftDigest, harness.store.saveDraftCalls)
				}
				return
			}
			if err != nil {
				t.Fatalf("ApplyCommands with a current expand source: %v", err)
			}
			workspace, decodeErr := authoringv2.DecodeWorkspace(harness.store.created.DraftConfig)
			if decodeErr != nil {
				t.Fatal(decodeErr)
			}
			savedConstruction := workspace.Documents[0].Construction
			if savedConstruction == nil || len(savedConstruction.SourceProjections) != 1 ||
				savedConstruction.SourceProjections[0].OwnerStepID != expandTestStep || savedConstruction.SourceProjections[0].FieldPath != "instantiatesUri[]" {
				t.Fatalf("applied construction lost the durable expand projection: %#v", savedConstruction)
			}
		})
	}
}
