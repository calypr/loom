package lifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

const (
	deriveSourceProject  = "project-a"
	deriveSourceExplorer = "body-structures"
	deriveSourceOutput   = "body_structures"
	deriveSourceRootType = "BodyStructure"
	deriveSourceRootNode = "body-structure"
	deriveSourceIdentity = "bs-id"
	deriveSourceField    = "active"
	deriveSourceColumn   = "derive_source_active"
	deriveSourceStep     = "derive-birth"
	deriveSourceBirth    = "derive_source_birth"
)

func deriveSourceSnapshot(token string) capability.Snapshot {
	return capability.NewSnapshot(
		capability.SnapshotIdentity{
			Project: deriveSourceProject, Generation: token, AuthorizationScopeDigest: "scope",
			SchemaDigest: "schema-a", ResourceInventoryDigest: "resources", RelationshipDigest: "relationships",
			FieldDigest: "fields", ProtocolVersion: "protocol", CompilerVersion: "compiler",
			TraversalPolicyVersion: "traversal", ProjectionPolicyVersion: "projection",
		},
		capability.Policy{Route: capability.RoutePolicy{Version: "traversal", AllowsRepeatedEdges: true, AllowsSelfLoops: true}},
		capability.StatusReady, true, false,
		[]capability.Node{{ID: deriveSourceRootNode, ResourceType: deriveSourceRootType, RowRootEligible: true, RowGrain: "RESOURCE", Populated: true, DocumentCount: 4}},
		nil,
		[]capability.Candidate{
			{ID: "bs-id", NodeID: deriveSourceRootNode, ResourceType: deriveSourceRootType, FieldPath: "id", Label: "Logical id", LogicalType: "string", Cardinality: "optional_one", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar}, Observed: true, Populated: true, ObservedDocumentCount: 4},
			{ID: "bs-active", NodeID: deriveSourceRootNode, ResourceType: deriveSourceRootType, FieldPath: deriveSourceField, Label: "Whether this record is in active use", LogicalType: "boolean", Cardinality: "optional_one", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar}, Observed: true, Populated: true, ObservedDocumentCount: 4},
			{ID: "bs-description", NodeID: deriveSourceRootNode, ResourceType: deriveSourceRootType, FieldPath: "description", Label: "Description", LogicalType: "string", Cardinality: "optional_one", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar}, Observed: false, Populated: false},
			{ID: "bs-patient", NodeID: deriveSourceRootNode, ResourceType: deriveSourceRootType, FieldPath: "patient", Label: "Patient", LogicalType: "string", Cardinality: "optional_one", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar}, Observed: true, Populated: true, ObservedDocumentCount: 4},
		},
		nil,
	)
}

func deriveSourceDocument() authoringv2.Document {
	return authoringv2.Document{
		Kind:             authoringv2.Kind,
		Output:           authoringv2.Output{ID: deriveSourceOutput, Title: "Body structures"},
		RootResourceType: deriveSourceRootType,
		Route:            authoringv2.RouteNode{OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: deriveSourceRootType},
		Rows:             authoringv2.RecordsRowDefinition(),
		Columns: []authoringv2.Column{{
			ColumnID: deriveSourceIdentity, Column: "body_structure_id", Label: "Body structure ID",
			LogicalType: "string", OccurrenceID: authoringv2.RootOccurrenceID,
			Source: authoringv2.ColumnSource{Kind: authoringv2.SourceField, Field: &authoringv2.FieldSource{Path: "id", ProjectionMode: "VALUE"}},
		}},
	}
}

func deriveSourceIdentityColumns() []authoringv2.StageColumn {
	return []authoringv2.StageColumn{{ID: deriveSourceIdentity, Name: "body_structure_id", Label: "Body structure ID", Type: "string"}}
}

func deriveSourceInt(value int64) *int64 { return &value }

func deriveSourceService(t *testing.T) *Service {
	t.Helper()
	index, err := fhirschema.GeneratedIndex()
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := NewSchemaRowChoiceResolver(index)
	if err != nil {
		t.Fatal(err)
	}
	return &Service{config: Config{RowChoicePlanner: resolver, RowChoiceResolver: resolver}}
}

func deriveSourceBase(snapshot capability.Snapshot, construction authoringv2.Construction) constructionBase {
	return constructionBase{
		document:     deriveSourceDocument(),
		construction: construction,
		snapshot:     snapshot,
		authorized:   AuthorizedCapability{Snapshot: snapshot, Scope: authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}},
		stages: []explorer.ReceiptConstructionStage{{
			ID: recipe.ConstructionSourceProjectionID, RowIdentityColumn: "_key",
			Columns: []explorer.ReceiptConstructionStageColumn{
				{ID: deriveSourceIdentity, Name: "body_structure_id", Label: "Body structure ID", Type: "string"},
			},
		}},
	}
}

func deriveSourceAddStep(stepID, leftID string, outputs []authoringv2.StageColumn) authoringv2.ConstructionStep {
	return authoringv2.ConstructionStep{
		ID:     stepID,
		Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputSourceProjection}},
		Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationDerive, Derive: &authoringv2.ConstructionDerive{
			ConstructionID: stepID, OutputColumnID: "derived_total",
			Operation: authoringv2.ConstructionDerivedAdd,
			Left: authoringv2.ConstructionOperand{
				Kind: authoringv2.ConstructionColumnOperand, ColumnID: leftID,
			},
			Right: authoringv2.ConstructionOperand{
				Kind:    authoringv2.ConstructionLiteralOperand,
				Literal: &authoringv2.ConstructionLiteral{Kind: authoringv2.ConstructionNumericInteger, Integer: deriveSourceInt(3)},
			},
			MissingInputPolicy: authoringv2.ConstructionMissingInputPropagateNull,
		}},
		Outputs: outputs,
	}
}

func deriveSourceLiteralOnlyStep(stepID string, outputs []authoringv2.StageColumn) authoringv2.ConstructionStep {
	return authoringv2.ConstructionStep{
		ID:     stepID,
		Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputSourceProjection}},
		Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationDerive, Derive: &authoringv2.ConstructionDerive{
			ConstructionID:     stepID,
			OutputColumnID:     "derived_total",
			Operation:          authoringv2.ConstructionDerivedAdd,
			Left:               authoringv2.ConstructionOperand{Kind: authoringv2.ConstructionLiteralOperand, Literal: &authoringv2.ConstructionLiteral{Kind: authoringv2.ConstructionNumericInteger, Integer: deriveSourceInt(2)}},
			Right:              authoringv2.ConstructionOperand{Kind: authoringv2.ConstructionLiteralOperand, Literal: &authoringv2.ConstructionLiteral{Kind: authoringv2.ConstructionNumericInteger, Integer: deriveSourceInt(3)}},
			MissingInputPolicy: authoringv2.ConstructionMissingInputPropagateNull,
		}},
		Outputs: outputs,
	}
}

func deriveSourceRowChoice(t *testing.T, snapshot capability.Snapshot, resourceType, path string) capability.RowChoice {
	t.Helper()
	index, err := fhirschema.GeneratedIndex()
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := NewSchemaRowChoiceResolver(index)
	if err != nil {
		t.Fatal(err)
	}
	document := authoringv2.Document{
		RootResourceType: resourceType,
		Route:            authoringv2.RouteNode{OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: resourceType},
	}
	choices, err := resolver.ListRowChoices(context.Background(), snapshot, document)
	if err != nil {
		t.Fatal(err)
	}
	for _, choice := range choices {
		if choice.Kind == capability.RowChoiceFieldGroupKey && choice.OccurrenceID == authoringv2.RootOccurrenceID && choice.Path == path {
			return choice
		}
	}
	t.Fatalf("planner omitted the %s root field choice: %#v", path, choices)
	return capability.RowChoice{}
}

func deriveSourceSelection(t *testing.T, snapshot capability.Snapshot, resourceType, path, columnID string) ConstructionDeriveSourceSelection {
	t.Helper()
	return ConstructionDeriveSourceSelection{ChoiceID: deriveSourceRowChoice(t, snapshot, resourceType, path).ChoiceID, ColumnID: columnID}
}

func deriveSourceProposalRequest(changedStepID string, sources []ConstructionDeriveSourceSelection) ConstructionProposalRequest {
	return ConstructionProposalRequest{
		Project: deriveSourceProject, ExplorerID: deriveSourceExplorer, SnapshotToken: "derive-source-token",
		ExpectedDraftVersion: 1, ExpectedDraftDigest: "sha256:derive-source-draft", OutputID: deriveSourceOutput,
		ChangedStepID: changedStepID, DeriveSources: sources,
	}
}

type deriveSourcePatientHarness struct {
	*filterSourceHarness
}

func newDeriveSourceHarness(t *testing.T) *deriveSourcePatientHarness {
	t.Helper()
	return &deriveSourcePatientHarness{filterSourceHarness: newFilterSourceHarness(t)}
}

func deriveSourcePatientBase(snapshot capability.Snapshot, construction authoringv2.Construction) constructionBase {
	return constructionBase{
		document:     deriveSourcePatientDocument(construction),
		construction: construction,
		snapshot:     snapshot,
		authorized:   AuthorizedCapability{Snapshot: snapshot, Scope: authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}},
		stages: []explorer.ReceiptConstructionStage{{
			ID: recipe.ConstructionSourceProjectionID, RowIdentityColumn: "_key",
			Columns: []explorer.ReceiptConstructionStageColumn{
				{ID: "patient-id", Name: "patient_id", Label: "Patient ID"},
			},
		}},
	}
}

func deriveSourcePatientProposalRequest(changedStepID string, sources []ConstructionDeriveSourceSelection) ConstructionProposalRequest {
	return ConstructionProposalRequest{
		Project: filterSourceProject, ExplorerID: filterSourceExplorer, SnapshotToken: "derive-source-patient-token",
		ExpectedDraftVersion: 1, ExpectedDraftDigest: "sha256:derive-source-patient-draft", OutputID: filterSourceOutput,
		ChangedStepID: changedStepID, DeriveSources: sources,
	}
}

func deriveSourcePatientDerivedColumns() []authoringv2.StageColumn {
	return append(deriveSourcePatientIdentityColumns(), authoringv2.StageColumn{ID: "derived_total", Name: "derived_total", Label: "Derived total", Type: "integer"})
}

func deriveSourcePatientIdentityColumns() []authoringv2.StageColumn {
	return []authoringv2.StageColumn{{ID: "patient-id", Name: "patient_id", Label: "Patient ID"}}
}

func deriveSourcePatientProjectionColumn() authoringv2.StageColumn {
	return authoringv2.StageColumn{
		ID: deriveSourceBirth, Name: authoringv2.ConstructionSourceProjectionName(deriveSourceBirth),
		Label: "Patient.multipleBirthInteger", Type: "integer",
	}
}

func deriveSourcePatientDocument(construction authoringv2.Construction) authoringv2.Document {
	return authoringv2.Document{
		Kind:             authoringv2.Kind,
		Output:           authoringv2.Output{ID: filterSourceOutput, Title: "Patients"},
		RootResourceType: "Patient",
		Route:            authoringv2.RouteNode{OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "Patient"},
		Rows:             authoringv2.RecordsRowDefinition(),
		Columns: []authoringv2.Column{{
			ColumnID: "patient-id", Column: "patient_id", Label: "Patient ID",
			LogicalType: "string", OccurrenceID: authoringv2.RootOccurrenceID,
			Source: authoringv2.ColumnSource{Kind: authoringv2.SourceField, Field: &authoringv2.FieldSource{Path: "id", ProjectionMode: "VALUE"}},
		}},
		Construction: &construction,
	}
}

func TestConstructionDeriveSourceCapabilityExposesPopulatedUnselectedNumericRootScalar(t *testing.T) {
	harness := newDeriveSourceHarness(t)
	result, err := harness.service.constructionDeriveSourceCapability(context.Background(),
		deriveSourcePatientBase(harness.snapshot, authoringv2.Construction{Version: authoringv2.ConstructionVersion}), "patients", recipe.ConstructionSourceProjectionID)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Supported || result.ReasonCode != "" || result.StageID != recipe.ConstructionSourceProjectionID {
		t.Fatalf("derive source capability = %#v, want a supported source-stage capability", result)
	}
	byPath := make(map[string]ConstructionDeriveSourceChoice, len(result.Choices))
	for _, choice := range result.Choices {
		byPath[choice.FieldPath] = choice
	}
	birth, found := byPath["multipleBirthInteger"]
	if !found {
		t.Fatalf("populated unselected root numeric scalar was not offered: %#v", result.Choices)
	}
	if birth.Label != "Birth count" || birth.LogicalType != "integer" || !birth.IsPopulated {
		t.Fatalf("offered choice facts = %#v, want generated label, integer logical type, populated", birth)
	}
	if birth.ColumnID != "" {
		t.Fatalf("a field with no durable server-owned materialization advertised columnId %q", birth.ColumnID)
	}
	identity, err := capability.DecodeRowChoiceID(birth.ChoiceID)
	if err != nil {
		t.Fatalf("offered choice id is not a signed row choice: %v", err)
	}
	if identity.SnapshotToken != harness.snapshot.Token || identity.SchemaDigest != harness.snapshot.Identity.SchemaDigest ||
		identity.Kind != capability.RowChoiceFieldGroupKey || identity.Path != "multipleBirthInteger" ||
		identity.Shape != capability.RowChoiceScalar || identity.Cardinality != capability.RowChoiceOne || identity.Reference {
		t.Fatalf("offered choice identity = %#v, want a token/schema-pinned non-reference root scalar", identity)
	}
	for _, excluded := range []string{"id"} {
		if offered, exists := byPath[excluded]; exists {
			t.Fatalf("non-numeric field %q was offered as derive source data: %#v", excluded, offered)
		}
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"supported":true`, `"stageId":"source_projection"`, `"choiceId":"rc1.`, `"fieldPath":"multipleBirthInteger"`, `"label":"Birth count"`, `"logicalType":"integer"`, `"isPopulated":true`} {
		if !strings.Contains(string(encoded), want) {
			t.Fatalf("deriveSourceInput wire shape omitted %s: %s", want, encoded)
		}
	}
}

func TestConstructionDeriveSourceRejectsNonNumericFieldOnDiscoveryAndAdmission(t *testing.T) {
	snapshot := deriveSourceSnapshot("derive-source-nonnumeric")
	service := deriveSourceService(t)
	result, err := service.constructionDeriveSourceCapability(context.Background(),
		deriveSourceBase(snapshot, authoringv2.Construction{Version: authoringv2.ConstructionVersion}), deriveSourceOutput, recipe.ConstructionSourceProjectionID)
	if err != nil {
		t.Fatal(err)
	}
	if result.Supported {
		t.Fatalf("capability = %#v, want unsupported (BodyStructure has no populated integer/decimal row path)", result)
	}
	if result.ReasonCode != "NO_ELIGIBLE_POPULATED_DERIVE_SOURCE_FIELDS" || len(result.Choices) != 0 {
		t.Fatalf("unsupported capability = %#v, want the derive-source reason without choices", result)
	}
	nonnumeric := deriveSourceRowChoice(t, snapshot, deriveSourceRootType, deriveSourceField)
	if _, err := capability.DecodeRowChoiceID(nonnumeric.ChoiceID); err != nil {
		t.Fatalf("crafted boolean candidate produced no signed token: %v", err)
	}
	projection, err := service.resolveConstructionDeriveSource(context.Background(),
		deriveSourceBase(snapshot, authoringv2.Construction{Version: authoringv2.ConstructionVersion}),
		deriveSourceProposalRequest(deriveSourceStep, nil), recipe.ConstructionSourceProjectionID,
		ConstructionDeriveSourceSelection{ChoiceID: nonnumeric.ChoiceID, ColumnID: deriveSourceColumn})
	if err == nil {
		t.Fatalf("non-numeric field was admitted as a derive source projection: %#v", projection)
	}
	if code := lifecycleErrorCode(err); code != "UNSUPPORTED_DERIVE_SOURCE_FIELD" {
		t.Fatalf("non-numeric admission error = %v, want UNSUPPORTED_DERIVE_SOURCE_FIELD", err)
	}
}

func TestDeriveSourceLogicalTypeIsNumericAdmitsOnlyArithmeticKinds(t *testing.T) {
	for _, want := range []string{"integer", "decimal", " INTEGER", "Decimal "} {
		if !deriveSourceLogicalTypeIsNumeric(want) {
			t.Fatalf("deriveSourceLogicalTypeIsNumeric(%q) = false, want true", want)
		}
	}
	for _, refuse := range []string{"", "number", "boolean", "string", "code", "date", "uuid", "object", "quantity"} {
		if deriveSourceLogicalTypeIsNumeric(refuse) {
			t.Fatalf("deriveSourceLogicalTypeIsNumeric(%q) = true, want false", refuse)
		}
	}
}

func TestConstructionDeriveSourceRefusesForgedAndCrossSnapshotChoices(t *testing.T) {
	snapshot := deriveSourceSnapshot("derive-source-current")
	other := deriveSourceSnapshot("derive-source-other")
	service := deriveSourceService(t)
	base := deriveSourceBase(snapshot, authoringv2.Construction{Version: authoringv2.ConstructionVersion})
	request := deriveSourceProposalRequest(deriveSourceStep, nil)

	foreign := deriveSourceRowChoice(t, other, deriveSourceRootType, deriveSourceField)
	if _, err := service.resolveConstructionDeriveSource(context.Background(), base, request, recipe.ConstructionSourceProjectionID,
		ConstructionDeriveSourceSelection{ChoiceID: foreign.ChoiceID, ColumnID: deriveSourceColumn}); err == nil {
		t.Fatal("a choice issued against another snapshot was accepted")
	} else if code := lifecycleErrorCode(err); code != "INVALID_DERIVE_SOURCE_CHOICE" {
		t.Fatalf("cross-snapshot error = %v, want INVALID_DERIVE_SOURCE_CHOICE", err)
	}

	parts := strings.Split(deriveSourceRowChoice(t, snapshot, deriveSourceRootType, deriveSourceField).ChoiceID, ".")
	if len(parts) != 3 {
		t.Fatalf("signed choice is not a three-part token: %q", parts)
	}
	forged := parts[0] + "." + parts[1] + "." + strings.Repeat("0", len(parts[2]))
	if _, err := service.resolveConstructionDeriveSource(context.Background(), base, request, recipe.ConstructionSourceProjectionID,
		ConstructionDeriveSourceSelection{ChoiceID: forged, ColumnID: deriveSourceColumn}); err == nil {
		t.Fatal("a tampered choice token was accepted")
	} else if code := lifecycleErrorCode(err); code != "INVALID_DERIVE_SOURCE_CHOICE" {
		t.Fatalf("tampered token error = %v, want INVALID_DERIVE_SOURCE_CHOICE", err)
	}

	if _, err := service.resolveConstructionDeriveSource(context.Background(), base, request, recipe.ConstructionSourceProjectionID,
		ConstructionDeriveSourceSelection{ChoiceID: "rc1.not-a-real-token.deadbeef", ColumnID: deriveSourceColumn}); err == nil {
		t.Fatal("an invented choice token was accepted")
	}
}

func TestConstructionCandidateWithDeriveSourcesMaterializesAndRowPreserves(t *testing.T) {
	harness := newDeriveSourceHarness(t)
	snapshot := harness.snapshot
	service := harness.service
	construction := authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{
		deriveSourceAddStep(deriveSourceStep, "patient-id", deriveSourcePatientDerivedColumns()),
	}}
	base := deriveSourcePatientBase(snapshot, construction)

	candidate := construction
	left := candidate.Steps[0].Operation.Derive.Left
	left.ColumnID = deriveSourceBirth
	candidate.Steps[0].Operation.Derive.Left = left
	candidate.Steps[0].Outputs = append(deriveSourcePatientIdentityColumns(), deriveSourcePatientProjectionColumn(), authoringv2.StageColumn{ID: "derived_total", Name: "derived_total", Label: "Derived total", Type: "integer"})

	prepared, err := service.constructionCandidateWithDeriveSources(context.Background(), base,
		deriveSourcePatientProposalRequest(deriveSourceStep, []ConstructionDeriveSourceSelection{
			deriveSourceSelection(t, snapshot, "Patient", "multipleBirthInteger", deriveSourceBirth),
		}), candidate)
	if err != nil {
		t.Fatalf("prepare derive sources: %v", err)
	}
	if len(prepared.SourceProjections) != 1 {
		t.Fatalf("prepared projections = %#v, want exactly one derive-owned projection", prepared.SourceProjections)
	}
	projection := prepared.SourceProjections[0]
	if projection.ColumnID != deriveSourceBirth || projection.OwnerStepID != deriveSourceStep ||
		projection.OccurrenceID != authoringv2.RootOccurrenceID || projection.FieldPath != "multipleBirthInteger" ||
		projection.FHIRType != "integer" || projection.LogicalType != "integer" || projection.Label != "Birth count" {
		t.Fatalf("durable projection = %#v, want server-derived facts for the unselected field", projection)
	}
	carried := false
	for _, output := range prepared.Steps[0].Outputs {
		if output.ID == deriveSourceBirth && output.Name == authoringv2.ConstructionSourceProjectionName(deriveSourceBirth) && output.Type == "integer" {
			carried = true
		}
	}
	if !carried {
		t.Fatalf("derive step omitted the projection output: %#v", prepared.Steps[0].Outputs)
	}
	document := deriveSourcePatientDocument(prepared)
	analyzed, impact, err := document.AnalyzeConstructionCandidate(prepared, deriveSourceStep, nil)
	if err != nil {
		t.Fatalf("derive-consuming candidate was refused by the real authoring validator: %v", err)
	}
	if impact.HasMissingInputs() {
		t.Fatalf("derive-consuming candidate needs repair: %#v", impact.MissingInputs)
	}
	if len(analyzed.Construction.SourceProjections) != 1 || analyzed.Construction.SourceProjections[0].OwnerStepID != deriveSourceStep {
		t.Fatalf("analysis dropped or re-owned the derive projection: %#v", analyzed.Construction.SourceProjections)
	}
	wantIDs := []string{"patient-id", deriveSourceBirth, "derived_total"}
	gotIDs := make([]string, 0, len(analyzed.Construction.Steps[0].Outputs))
	for _, output := range analyzed.Construction.Steps[0].Outputs {
		gotIDs = append(gotIDs, output.ID)
	}
	if !reflect.DeepEqual(gotIDs, wantIDs) {
		t.Fatalf("derive output ids = %v, want %v", gotIDs, wantIDs)
	}
}

func TestConstructionDeriveSourceCannotCrossRowChangingPrefix(t *testing.T) {
	harness := newDeriveSourceHarness(t)
	group := filterSourceGroupStep()
	deriveInputs := []authoringv2.StageColumn{
		{ID: "group_key", Name: "group_key", Label: "Group", Type: "string"},
		{ID: "rows", Name: "rows", Label: "Rows", Type: "integer"},
	}
	stepOutputs := append(append([]authoringv2.StageColumn(nil), deriveInputs...), deriveSourcePatientProjectionColumn(), authoringv2.StageColumn{ID: "derived_total", Name: "derived_total", Label: "Derived total", Type: "integer"})
	candidate := authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{
		group,
		deriveSourceAddStep(deriveSourceStep, deriveSourceBirth, stepOutputs),
	}}
	base := deriveSourcePatientBase(harness.snapshot, authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{group}})
	base.stages = append(base.stages, explorer.ReceiptConstructionStage{
		ID: "group-first", InputStageID: recipe.ConstructionSourceProjectionID, Operation: "GROUP",
		Columns: []explorer.ReceiptConstructionStageColumn{
			{ID: "group_key", Name: "group_key", Label: "Group", Type: "string"},
			{ID: "rows", Name: "rows", Label: "Rows", Type: "integer"},
		},
	})

	_, err := harness.service.constructionCandidateWithDeriveSources(context.Background(), base,
		deriveSourcePatientProposalRequest(deriveSourceStep, []ConstructionDeriveSourceSelection{
			deriveSourceSelection(t, harness.snapshot, "Patient", "multipleBirthInteger", deriveSourceBirth),
		}), candidate)
	if err == nil {
		t.Fatal("a root derive source was carried across a GROUP")
	}
	if code := lifecycleErrorCode(err); code != "DERIVE_SOURCE_CANNOT_CROSS_STAGE" {
		t.Fatalf("root derive source across a GROUP error = %v, want DERIVE_SOURCE_CANNOT_CROSS_STAGE", err)
	}
}

func TestConstructionDeriveSourceProjectionMustMatchItsOperand(t *testing.T) {
	document := deriveSourcePatientDocument(authoringv2.Construction{Version: authoringv2.ConstructionVersion})

	projectionFacts := authoringv2.ConstructionSourceProjection{
		ColumnID: deriveSourceBirth, OwnerStepID: "another-step", OccurrenceID: authoringv2.RootOccurrenceID,
		FieldPath: "multipleBirthInteger", FHIRType: "integer", LogicalType: "integer", Label: "Patient.multipleBirthInteger",
	}
	foreign := authoringv2.Construction{
		Version:           authoringv2.ConstructionVersion,
		Steps:             []authoringv2.ConstructionStep{deriveSourceAddStep(deriveSourceStep, "patient-id", append(deriveSourcePatientIdentityColumns(), deriveSourcePatientProjectionColumn(), authoringv2.StageColumn{ID: "derived_total", Name: "derived_total", Label: "Derived total", Type: "integer"}))},
		SourceProjections: []authoringv2.ConstructionSourceProjection{projectionFacts},
	}
	foreignDocument := document
	foreignDocument.Construction = &foreign
	if err := foreignDocument.Validate(); err == nil {
		t.Fatal("a projection owned by a step that does not consume it was accepted")
	}

	mismatched := authoringv2.Construction{
		Version: authoringv2.ConstructionVersion,
		Steps:   []authoringv2.ConstructionStep{deriveSourceAddStep(deriveSourceStep, "patient-id", append(deriveSourcePatientIdentityColumns(), deriveSourcePatientProjectionColumn(), authoringv2.StageColumn{ID: "derived_total", Name: "derived_total", Label: "Derived total", Type: "integer"}))},
		SourceProjections: []authoringv2.ConstructionSourceProjection{{
			ColumnID: deriveSourceBirth, OwnerStepID: deriveSourceStep, OccurrenceID: authoringv2.RootOccurrenceID,
			FieldPath: "multipleBirthInteger", FHIRType: "integer", LogicalType: "integer", Label: "Patient.multipleBirthInteger",
		}},
	}
	mismatchedDocument := document
	mismatchedDocument.Construction = &mismatched
	if err := mismatchedDocument.Validate(); err == nil || !strings.Contains(err.Error(), deriveSourceBirth) {
		t.Fatalf("projection not matching a derive operand error = %v, want it named %q", err, deriveSourceBirth)
	}
}

func TestConstructionDeriveSourceRemovalStripsProjectionAndRefusesDanglingDerive(t *testing.T) {
	harness := newDeriveSourceHarness(t)
	snapshot := harness.snapshot
	service := harness.service
	projection := authoringv2.ConstructionSourceProjection{
		ColumnID: deriveSourceBirth, OwnerStepID: deriveSourceStep, OccurrenceID: authoringv2.RootOccurrenceID,
		FieldPath: "multipleBirthInteger", FHIRType: "integer", LogicalType: "integer", Label: "Patient.multipleBirthInteger",
	}
	outputs := append(deriveSourcePatientIdentityColumns(), deriveSourcePatientProjectionColumn(), authoringv2.StageColumn{ID: "derived_total", Name: "derived_total", Label: "Derived total", Type: "integer"})
	saved := authoringv2.Construction{
		Version:           authoringv2.ConstructionVersion,
		Steps:             []authoringv2.ConstructionStep{deriveSourceAddStep(deriveSourceStep, deriveSourceBirth, outputs)},
		SourceProjections: []authoringv2.ConstructionSourceProjection{projection},
	}
	savedDocument := deriveSourcePatientDocument(saved)
	if err := savedDocument.Validate(); err != nil {
		t.Fatalf("a saved derive-owned construction is not durable authoring state: %v", err)
	}

	cleared, err := service.constructionCandidateWithDeriveSources(context.Background(),
		deriveSourcePatientBase(snapshot, saved), deriveSourcePatientProposalRequest(deriveSourceStep, []ConstructionDeriveSourceSelection{}), saved)
	if err != nil {
		t.Fatalf("clearing the derive source selections failed: %v", err)
	}
	if len(cleared.SourceProjections) != 0 {
		t.Fatalf("cleared selection kept a derive-owned projection: %#v", cleared.SourceProjections)
	}
	for _, output := range cleared.Steps[0].Outputs {
		if output.ID == deriveSourceBirth {
			t.Fatalf("cleared selection left the projection column in the derive output: %#v", cleared.Steps[0].Outputs)
		}
	}
	dangling := authoringv2.Construction{
		Version: authoringv2.ConstructionVersion,
		Steps:   []authoringv2.ConstructionStep{deriveSourceAddStep(deriveSourceStep, deriveSourceBirth, outputs)},
	}
	danglingDocument := deriveSourcePatientDocument(dangling)
	if err := danglingDocument.Validate(); err == nil || !strings.Contains(err.Error(), deriveSourceBirth) {
		t.Fatalf("dangling derive validation error = %v, want the missing projection column named", err)
	}
	if _, impact, err := savedDocument.AnalyzeConstructionCandidate(cleared, deriveSourceStep, nil); err == nil && !impact.HasMissingInputs() {
		t.Fatal("analysis silently kept a derive whose source selection was removed")
	}
}

func TestConstructionDeriveSourceLeavesSelectedColumnBehaviorUnchanged(t *testing.T) {
	harness := newDeriveSourceHarness(t)
	snapshot := harness.snapshot
	service := harness.service
	construction := authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{
		deriveSourceAddStep(deriveSourceStep, "patient-id", deriveSourcePatientDerivedColumns()),
	}}
	document := deriveSourcePatientDocument(construction)
	if _, _, err := document.AnalyzeConstructionCandidate(construction, deriveSourceStep, nil); err == nil {
	}
	unchanged, err := service.constructionCandidateWithDeriveSources(context.Background(),
		deriveSourcePatientBase(snapshot, construction), deriveSourcePatientProposalRequest(deriveSourceStep, nil), construction)
	if err != nil {
		t.Fatalf("derive request without deriveSources failed: %v", err)
	}
	if !reflect.DeepEqual(unchanged, construction) {
		t.Fatalf("omitted deriveSources mutated the candidate: %#v", unchanged)
	}
	literalOnly := authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{
		deriveSourceLiteralOnlyStep(deriveSourceStep, deriveSourcePatientDerivedColumns()),
	}}
	literalDocument := deriveSourcePatientDocument(literalOnly)
	if _, impact, err := literalDocument.AnalyzeConstructionCandidate(literalOnly, deriveSourceStep, nil); err != nil || impact.HasMissingInputs() {
		t.Fatalf("literal-operands-only derive regressed: %v %#v", err, impact.MissingInputs)
	}
}

func TestConstructionDeriveSourceSelectionsRequireExactDeduplicatedFacts(t *testing.T) {
	duplicate := deriveSourceProposalRequest(deriveSourceStep, []ConstructionDeriveSourceSelection{
		{ChoiceID: "rc1.a.b", ColumnID: "c1"}, {ChoiceID: "rc1.a.b", ColumnID: "c2"},
	})
	if err := duplicate.Validate(); err == nil || !strings.Contains(err.Error(), "deriveSources") {
		t.Fatalf("duplicate deriveSources error = %v, want a deriveSources dedup refusal", err)
	}
	blank := deriveSourceProposalRequest(deriveSourceStep, []ConstructionDeriveSourceSelection{{ChoiceID: " ", ColumnID: "c1"}})
	if err := blank.Validate(); err == nil || !strings.Contains(err.Error(), "deriveSources[0].choiceId") {
		t.Fatalf("blank choiceId error = %v, want deriveSources[0].choiceId refusal", err)
	}
	blankColumn := deriveSourceProposalRequest(deriveSourceStep, []ConstructionDeriveSourceSelection{{ChoiceID: "rc1.a.b", ColumnID: " "}})
	if err := blankColumn.Validate(); err == nil || !strings.Contains(err.Error(), "deriveSources[0].columnId") {
		t.Fatalf("blank columnId error = %v, want deriveSources[0].columnId refusal", err)
	}
	oversized := deriveSourceProposalRequest(deriveSourceStep, []ConstructionDeriveSourceSelection{
		{ChoiceID: "rc1.a.b", ColumnID: "c1"}, {ChoiceID: "rc1.c.d", ColumnID: "c2"}, {ChoiceID: "rc1.e.f", ColumnID: "c3"},
	})
	if err := oversized.Validate(); err == nil || !strings.Contains(err.Error(), "operand") {
		t.Fatalf("oversized deriveSources error = %v, want the two-operand cap refusal", err)
	}
	encoded, err := json.Marshal(ConstructionDeriveSourceSelection{ChoiceID: "rc1.x.y", ColumnID: "c"})
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != `{"choiceId":"rc1.x.y","columnId":"c"}` {
		t.Fatalf("deriveSources selection wire shape = %s", encoded)
	}
}

func TestConstructionDeriveSourceChoiceWireShapeIsExactlySixFields(t *testing.T) {
	wantFields := map[string]bool{"choiceId": true, "fieldPath": true, "label": true, "logicalType": true, "isPopulated": true, "columnId": true}
	structFields := make(map[string]bool, reflect.TypeOf(ConstructionDeriveSourceChoice{}).NumField())
	for index := range reflect.TypeOf(ConstructionDeriveSourceChoice{}).NumField() {
		tag := strings.Split(reflect.TypeOf(ConstructionDeriveSourceChoice{}).Field(index).Tag.Get("json"), ",")[0]
		structFields[tag] = true
	}
	if !reflect.DeepEqual(structFields, wantFields) {
		t.Fatalf("ConstructionDeriveSourceChoice struct fields = %v, want exactly %v", structFields, wantFields)
	}
	for _, pivotExtra := range []string{"occurrenceId", "fhirType", "valueType", "isIdentifier", "isReference"} {
		if structFields[pivotExtra] {
			t.Fatalf("pivot-style field %q must never appear on a derive source choice: %#v", pivotExtra, structFields)
		}
	}
	encoded, err := json.Marshal(ConstructionDeriveSourceChoice{
		ChoiceID: "rc1.a.b", FieldPath: "multipleBirthInteger", Label: "Patient.multipleBirthInteger", LogicalType: "integer", IsPopulated: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded) != len(wantFields) {
		t.Fatalf("encoded choice has %d keys, want exactly %d: %s", len(decoded), len(wantFields), encoded)
	}
	for field := range wantFields {
		if _, present := decoded[field]; !present {
			t.Fatalf("encoded choice omitted the pinned field %q: %s", field, encoded)
		}
	}
}

func TestConstructionDeriveSourceCapabilityAdvertisesDurableColumnIdOnlyWhileSigned(t *testing.T) {
	harness := newDeriveSourceHarness(t)
	snapshot := harness.snapshot
	service := harness.service
	projection := authoringv2.ConstructionSourceProjection{
		ColumnID: deriveSourceBirth, OwnerStepID: deriveSourceStep, OccurrenceID: authoringv2.RootOccurrenceID,
		FieldPath: "multipleBirthInteger", FHIRType: "integer", LogicalType: "integer", Label: "Patient.multipleBirthInteger",
	}
	outputs := append(deriveSourcePatientIdentityColumns(), deriveSourcePatientProjectionColumn(), authoringv2.StageColumn{ID: "derived_total", Name: "derived_total", Label: "Derived total", Type: "integer"})

	choiceColumnID := func(t *testing.T, construction authoringv2.Construction, stageID string) string {
		t.Helper()
		base := deriveSourcePatientBase(snapshot, construction)
		if stageID != recipe.ConstructionSourceProjectionID {
			base.stages = append(base.stages, explorer.ReceiptConstructionStage{ID: stageID, InputStageID: recipe.ConstructionSourceProjectionID, Operation: "GROUP"})
		}
		result, err := service.constructionDeriveSourceCapability(context.Background(), base, "patients", stageID)
		if err != nil {
			t.Fatal(err)
		}
		for _, choice := range result.Choices {
			if choice.FieldPath == "multipleBirthInteger" {
				return choice.ColumnID
			}
		}
		return "<missing choice>"
	}

	live := authoringv2.Construction{
		Version:           authoringv2.ConstructionVersion,
		Steps:             []authoringv2.ConstructionStep{deriveSourceAddStep(deriveSourceStep, deriveSourceBirth, outputs)},
		SourceProjections: []authoringv2.ConstructionSourceProjection{projection},
	}
	if got := choiceColumnID(t, live, recipe.ConstructionSourceProjectionID); got != deriveSourceBirth {
		t.Fatalf("live durable materialization advertised columnId %q, want %q", got, deriveSourceBirth)
	}

	literalOperands := deriveSourceLiteralOnlyStep(deriveSourceStep, outputs)
	repointed := authoringv2.Construction{
		Version:           authoringv2.ConstructionVersion,
		Steps:             []authoringv2.ConstructionStep{literalOperands},
		SourceProjections: []authoringv2.ConstructionSourceProjection{projection},
	}
	if got := choiceColumnID(t, repointed, recipe.ConstructionSourceProjectionID); got != "" {
		t.Fatalf("saved operand no longer referencing the source column was still advertised as columnId %q", got)
	}

	foreignOwner := authoringv2.Construction{
		Version: authoringv2.ConstructionVersion,
		Steps:   []authoringv2.ConstructionStep{deriveSourceAddStep(deriveSourceStep, deriveSourceBirth, outputs)},
		SourceProjections: []authoringv2.ConstructionSourceProjection{{
			ColumnID: deriveSourceBirth, OwnerStepID: "another-step", OccurrenceID: authoringv2.RootOccurrenceID,
			FieldPath: "multipleBirthInteger", FHIRType: "integer", LogicalType: "integer", Label: "Patient.multipleBirthInteger",
		}},
	}
	if got := choiceColumnID(t, foreignOwner, recipe.ConstructionSourceProjectionID); got != "" {
		t.Fatalf("materialization not owned by this stage's derive was advertised as columnId %q", got)
	}
}

func TestGetConstructionCapabilitiesAssemblesDeriveSourceInputBesidePivotSourceInput(t *testing.T) {
	harness := newDeriveSourceHarness(t)
	capabilities, err := harness.service.GetConstructionCapabilities(context.Background(), ConstructionCapabilitiesRequest{
		Project: harness.store.created.Project, ExplorerID: harness.store.created.ExplorerID, SnapshotToken: harness.snapshot.Token,
		ExpectedDraftVersion: harness.store.created.DraftVersion, ExpectedDraftDigest: harness.store.created.DraftDigest,
		OutputID: "patients", StageID: recipe.ConstructionSourceProjectionID,
	})
	if err != nil {
		t.Fatalf("capabilities with derive source discovery: %v", err)
	}
	if capabilities.PivotSourceInput.StageID != recipe.ConstructionSourceProjectionID {
		t.Fatalf("pivot source input was displaced: %#v", capabilities.PivotSourceInput)
	}
	if capabilities.FilterSourceInput.StageID != recipe.ConstructionSourceProjectionID {
		t.Fatalf("filter source input was displaced: %#v", capabilities.FilterSourceInput)
	}
	result := capabilities.DeriveSourceInput
	if result.StageID != recipe.ConstructionSourceProjectionID {
		t.Fatalf("derive source input stage = %#v, want the selected source stage", result)
	}
	byPath := make(map[string]ConstructionDeriveSourceChoice, len(result.Choices))
	for _, choice := range result.Choices {
		byPath[choice.FieldPath] = choice
	}
	birth, found := byPath["multipleBirthInteger"]
	if !found {
		t.Fatalf("populated unselected root numeric scalar missing from capabilities: %#v", result.Choices)
	}
	if !birth.IsPopulated || birth.LogicalType != "integer" || birth.Label != "Birth count" {
		t.Fatalf("birth count choice = %#v, want populated integer with the generated label", birth)
	}
	if birth.ColumnID != "" {
		t.Fatalf("birth count choice advertised columnId %q before any derive source was materialized", birth.ColumnID)
	}
	identity, err := capability.DecodeRowChoiceID(birth.ChoiceID)
	if err != nil || identity.SnapshotToken != harness.snapshot.Token || identity.SchemaDigest != harness.snapshot.Identity.SchemaDigest {
		t.Fatalf("capability choice id is not pinned to the current snapshot: %v %#v", err, identity)
	}
	if _, offered := byPath["id"]; offered {
		t.Fatal("a non-numeric field was advertised as a derive source")
	}
	encoded, err := json.Marshal(capabilities)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"deriveSourceInput":{`) {
		t.Fatalf("capabilities response omitted the pinned deriveSourceInput field: %s", encoded)
	}
}

func TestProposeConstructionDeriveSourceDerivesFromUnselectedFieldWithRealCompiler(t *testing.T) {
	harness := newDeriveSourceHarness(t)
	harness.saveDraftDerive(t, authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{
		deriveSourceLiteralOnlyStep(deriveSourceStep, deriveSourcePatientDerivedColumns()),
	}})
	version, digest := harness.store.created.DraftVersion, harness.store.created.DraftDigest
	acceptedBefore := append([]byte(nil), harness.store.created.DraftConfig...)

	proposal, err := harness.service.ProposeConstruction(context.Background(), ConstructionProposalRequest{
		Project: harness.store.created.Project, ExplorerID: harness.store.created.ExplorerID, SnapshotToken: harness.snapshot.Token,
		ExpectedDraftVersion: version, ExpectedDraftDigest: digest, OutputID: "patients",
		ChangedStepID:         deriveSourceStep,
		CandidateConstruction: harness.candidateDeriveConstruction(),
		DeriveSources:         []ConstructionDeriveSourceSelection{{ChoiceID: harness.choice(t, "multipleBirthInteger").ChoiceID, ColumnID: deriveSourceBirth}},
	})
	if err != nil {
		t.Fatalf("ProposeConstruction with deriveSources: %v", err)
	}
	if proposal.PreviewStatus != "PREVIEW_PENDING" || proposal.ProposalID == "" {
		t.Fatalf("derive source proposal = %#v, want a compiled preview proposal", proposal)
	}
	if len(proposal.CandidateConstruction.SourceProjections) != 1 {
		t.Fatalf("proposal projections = %#v, want the durable derive-owned projection", proposal.CandidateConstruction.SourceProjections)
	}
	durable := proposal.CandidateConstruction.SourceProjections[0]
	if durable.OwnerStepID != deriveSourceStep || durable.ColumnID != deriveSourceBirth || durable.FieldPath != "multipleBirthInteger" ||
		durable.FHIRType != "integer" || durable.LogicalType != "integer" || durable.OccurrenceID != authoringv2.RootOccurrenceID {
		t.Fatalf("durable projection = %#v, want server-derived facts for the unselected field", durable)
	}
	if harness.store.saveDraftCalls != 0 || string(acceptedBefore) != string(harness.store.created.DraftConfig) {
		t.Fatalf("proposal changed the accepted draft: saves=%d", harness.store.saveDraftCalls)
	}

	receipt := harness.store.receipt
	if receipt == nil || len(receipt.Bundle.Outputs) != 1 {
		t.Fatalf("compiled receipt omits the candidate bundle: %#v", receipt)
	}
	output := receipt.Bundle.Outputs[0]
	name := authoringv2.ConstructionSourceProjectionName(deriveSourceBirth)
	var slotColumnID, slotFieldRef, slotSelect string
	for index := range output.Fields {
		if output.Fields[index].Name == name {
			slotColumnID, slotFieldRef, slotSelect = output.Fields[index].ColumnID, output.Fields[index].FieldRef, output.Fields[index].Expr.Select
		}
	}
	if slotColumnID != deriveSourceBirth || slotFieldRef != "multipleBirthInteger" || !strings.HasSuffix(slotSelect, ".multipleBirthInteger") {
		t.Fatalf("compiled slot for the unselected field = column %q fieldRef %q select %q, want it to read the unselected field", slotColumnID, slotFieldRef, slotSelect)
	}
	var deriveLeft string
	carried := false
	foundDerived := false
	for _, step := range output.Construction.Steps {
		if step.Operation.Kind != recipe.ConstructionDeriveOp || step.Operation.Derive == nil {
			continue
		}
		deriveLeft = step.Operation.Derive.Left.ColumnID
		for _, column := range step.Outputs {
			if column.ID == deriveSourceBirth {
				carried = true
			}
			if column.ID == "derived_total" {
				foundDerived = true
			}
		}
	}
	if deriveLeft != deriveSourceBirth {
		t.Fatalf("compiled DERIVE left operand = %q, want the signed unselected field slot", deriveLeft)
	}
	if !carried || !foundDerived {
		t.Fatalf("compiled DERIVE step did not carry the projection or declare the derived column: %#v", output.Construction.Steps)
	}
}

func TestProposeConstructionDeriveSourceCompilesChainedColumnOperands(t *testing.T) {
	harness := newDeriveSourceHarness(t)
	firstDerived := authoringv2.StageColumn{ID: "chained_base", Name: "chained_base", Label: "Chained base", Type: "integer"}
	first := deriveSourceLiteralOnlyStep("derive-base", append(deriveSourcePatientIdentityColumns(), firstDerived))
	first.Operation.Derive.OutputColumnID = "chained_base"
	second := authoringv2.ConstructionStep{
		ID:     deriveSourceStep,
		Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputStepOutput, StepID: "derive-base"}},
		Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationDerive, Derive: &authoringv2.ConstructionDerive{
			ConstructionID:     deriveSourceStep,
			OutputColumnID:     "derived_total",
			Operation:          authoringv2.ConstructionDerivedAdd,
			Left:               authoringv2.ConstructionOperand{Kind: authoringv2.ConstructionColumnOperand, ColumnID: "chained_base"},
			Right:              authoringv2.ConstructionOperand{Kind: authoringv2.ConstructionLiteralOperand, Literal: &authoringv2.ConstructionLiteral{Kind: authoringv2.ConstructionNumericInteger, Integer: deriveSourceInt(1)}},
			MissingInputPolicy: authoringv2.ConstructionMissingInputPropagateNull,
		}},
		Outputs: append(append([]authoringv2.StageColumn(nil), deriveSourcePatientIdentityColumns()...), firstDerived, authoringv2.StageColumn{ID: "derived_total", Name: "derived_total", Label: "Derived total", Type: "integer"}),
	}
	based := authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{first, second}}
	harness.saveDraftDerive(t, based)
	version, digest := harness.store.created.DraftVersion, harness.store.created.DraftDigest

	var candidate authoringv2.Construction
	encoded, encodeErr := json.Marshal(based)
	if encodeErr != nil {
		t.Fatal(encodeErr)
	}
	if decodeErr := json.Unmarshal(encoded, &candidate); decodeErr != nil {
		t.Fatal(decodeErr)
	}
	left := candidate.Steps[1].Operation.Derive.Left
	left.ColumnID = deriveSourceBirth
	candidate.Steps[1].Operation.Derive.Left = left
	right := candidate.Steps[1].Operation.Derive.Right
	right.Kind = authoringv2.ConstructionColumnOperand
	right.ColumnID = "chained_base"
	right.Literal = nil
	candidate.Steps[1].Operation.Derive.Right = right
	candidate.Steps[1].Outputs = append(append([]authoringv2.StageColumn(nil), candidate.Steps[0].Outputs...), deriveSourcePatientProjectionColumn(), authoringv2.StageColumn{ID: "derived_total", Name: "derived_total", Label: "Derived total", Type: "integer"})

	proposal, err := harness.service.ProposeConstruction(context.Background(), ConstructionProposalRequest{
		Project: harness.store.created.Project, ExplorerID: harness.store.created.ExplorerID, SnapshotToken: harness.snapshot.Token,
		ExpectedDraftVersion: version, ExpectedDraftDigest: digest, OutputID: "patients",
		ChangedStepID:         deriveSourceStep,
		CandidateConstruction: candidate,
		DeriveSources:         []ConstructionDeriveSourceSelection{{ChoiceID: harness.choice(t, "multipleBirthInteger").ChoiceID, ColumnID: deriveSourceBirth}},
	})
	if err != nil {
		t.Fatalf("ProposeConstruction with two column operands: %v", err)
	}
	if proposal.PreviewStatus != "PREVIEW_PENDING" {
		t.Fatalf("two-column-operand proposal = %#v, want a compiled preview", proposal)
	}
	receipt := harness.store.receipt
	if receipt == nil {
		t.Fatal("two-column-operand compile produced no receipt")
	}
	matched := false
	for _, step := range receipt.Bundle.Outputs[0].Construction.Steps {
		if step.Operation.Kind != recipe.ConstructionDeriveOp || step.Operation.Derive == nil || step.Operation.Derive.OutputColumnID != "derived_total" {
			continue
		}
		matched = true
		if step.Operation.Derive.Left.ColumnID != deriveSourceBirth || step.Operation.Derive.Right.ColumnID != "chained_base" {
			t.Fatalf("compiled two-operand derive = left %q right %q, want the signed slot and the public column", step.Operation.Derive.Left.ColumnID, step.Operation.Derive.Right.ColumnID)
		}
	}
	if !matched {
		t.Fatal("compiled bundle has no derived_total step")
	}
}

func TestApplyConstructionProposalRefusesStaleDeriveSourceBinding(t *testing.T) {
	for _, stale := range []bool{false, true} {
		name := "current"
		if stale {
			name = "stale"
		}
		t.Run(name, func(t *testing.T) {
			harness := newDeriveSourceHarness(t)
			harness.saveDraftDerive(t, authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{
				deriveSourceLiteralOnlyStep(deriveSourceStep, deriveSourcePatientDerivedColumns()),
			}})
			version, digest := harness.store.created.DraftVersion, harness.store.created.DraftDigest
			proposal, err := harness.service.ProposeConstruction(context.Background(), ConstructionProposalRequest{
				Project: harness.store.created.Project, ExplorerID: harness.store.created.ExplorerID, SnapshotToken: harness.snapshot.Token,
				ExpectedDraftVersion: version, ExpectedDraftDigest: digest, OutputID: "patients",
				ChangedStepID:         deriveSourceStep,
				CandidateConstruction: harness.candidateDeriveConstruction(),
				DeriveSources:         []ConstructionDeriveSourceSelection{{ChoiceID: harness.choice(t, "multipleBirthInteger").ChoiceID, ColumnID: deriveSourceBirth}},
			})
			if err != nil {
				t.Fatalf("ProposeConstruction: %v", err)
			}
			if stale {
				harness.unobserveBirthCount()
			}
			_, err = harness.service.ApplyCommands(context.Background(), harness.store.created.Project, harness.store.created.ExplorerID, authoringv2.ApplyCommandsRequest{
				CommandID: "derive-source-apply", SemanticsVersion: authoringv2.CurrentSemanticsVersion, SnapshotToken: harness.snapshot.Token,
				ExpectedDraftVersion: version, ExpectedDraftDigest: digest,
				Commands: []authoringv2.Command{{Type: authoringv2.CommandApplyConstructionProposal, OutputID: "patients", ProposalID: proposal.ProposalID}},
			}, "derive-source-actor")
			if stale {
				if err == nil || lifecycleErrorCode(err) != "STALE_DERIVE_SOURCE_BINDING" {
					t.Fatalf("stale derive source apply error = %v, want STALE_DERIVE_SOURCE_BINDING", err)
				}
				if harness.store.created.DraftVersion != version || harness.store.created.DraftDigest != digest || harness.store.saveDraftCalls != 0 {
					t.Fatalf("stale apply changed the accepted draft: version=%d digest=%q saves=%d", harness.store.created.DraftVersion, harness.store.created.DraftDigest, harness.store.saveDraftCalls)
				}
				return
			}
			if err != nil {
				t.Fatalf("ApplyCommands with a current derive source: %v", err)
			}
			applied := harness.store.created
			if applied == nil {
				t.Fatal("apply did not save the draft")
			}
			workspace, decodeErr := authoringv2.DecodeWorkspace(applied.DraftConfig)
			if decodeErr != nil {
				t.Fatal(decodeErr)
			}
			saved := workspace.Documents[0].Construction
			if saved == nil || len(saved.SourceProjections) != 1 || saved.SourceProjections[0].OwnerStepID != deriveSourceStep ||
				saved.SourceProjections[0].FieldPath != "multipleBirthInteger" {
				t.Fatalf("applied construction lost the durable derive projection: %#v", saved)
			}
			capabilities, capErr := harness.service.GetConstructionCapabilities(context.Background(), ConstructionCapabilitiesRequest{
				Project: applied.Project, ExplorerID: applied.ExplorerID, SnapshotToken: harness.snapshot.Token,
				ExpectedDraftVersion: applied.DraftVersion, ExpectedDraftDigest: applied.DraftDigest,
				OutputID: "patients", StageID: recipe.ConstructionSourceProjectionID,
			})
			if capErr != nil {
				t.Fatalf("capabilities after the durable derive source applied: %v", capErr)
			}
			restored := ""
			for _, choice := range capabilities.DeriveSourceInput.Choices {
				if choice.FieldPath == "multipleBirthInteger" {
					restored = choice.ColumnID
				}
			}
			if restored != deriveSourceBirth {
				t.Fatalf("durable materialization should advertise columnId %q for UI restore, got %q", deriveSourceBirth, restored)
			}
		})
	}
}

func TestProposeConstructionDeriveSourceRefusesVanishedFieldRestore(t *testing.T) {
	harness := newDeriveSourceHarness(t)
	durable := authoringv2.Construction{
		Version: authoringv2.ConstructionVersion,
		Steps: []authoringv2.ConstructionStep{
			deriveSourceAddStepWithLiteral(deriveSourceStep, deriveSourceBirth, 1, append(deriveSourcePatientIdentityColumns(), deriveSourcePatientProjectionColumn(), authoringv2.StageColumn{ID: "derived_total", Name: "derived_total", Label: "Derived total", Type: "integer"})),
		},
		SourceProjections: []authoringv2.ConstructionSourceProjection{{
			ColumnID: deriveSourceBirth, OwnerStepID: deriveSourceStep, OccurrenceID: authoringv2.RootOccurrenceID,
			FieldPath: "multipleBirthInteger", FHIRType: "integer", LogicalType: "integer", Label: "Patient.multipleBirthInteger",
		}},
	}
	harness.saveDraftDurableDerive(t, durable)
	restoreChoice := harness.choice(t, "multipleBirthInteger").ChoiceID
	capabilities, err := harness.service.GetConstructionCapabilities(context.Background(), ConstructionCapabilitiesRequest{
		Project: harness.store.created.Project, ExplorerID: harness.store.created.ExplorerID, SnapshotToken: harness.snapshot.Token,
		ExpectedDraftVersion: harness.store.created.DraftVersion, ExpectedDraftDigest: harness.store.created.DraftDigest,
		OutputID: "patients", StageID: recipe.ConstructionSourceProjectionID,
	})
	if err != nil {
		t.Fatalf("capabilities before the field vanished: %v", err)
	}
	advertised := ""
	for _, choice := range capabilities.DeriveSourceInput.Choices {
		if choice.FieldPath == "multipleBirthInteger" {
			advertised = choice.ColumnID
		}
	}
	if advertised != deriveSourceBirth {
		t.Fatalf("capabilities should offer the durable materialization columnId %q for restore, got %q", deriveSourceBirth, advertised)
	}

	harness.unobserveBirthCount()
	acceptedBefore := append([]byte(nil), harness.store.created.DraftConfig...)
	_, err = harness.service.ProposeConstruction(context.Background(), ConstructionProposalRequest{
		Project: harness.store.created.Project, ExplorerID: harness.store.created.ExplorerID, SnapshotToken: harness.snapshot.Token,
		ExpectedDraftVersion: harness.store.created.DraftVersion, ExpectedDraftDigest: harness.store.created.DraftDigest,
		OutputID: "patients", ChangedStepID: deriveSourceStep,
		CandidateConstruction: durable,
		DeriveSources:         []ConstructionDeriveSourceSelection{{ChoiceID: restoreChoice, ColumnID: deriveSourceBirth}},
	})
	if err == nil {
		t.Fatal("a restore of a vanished field via its durable columnId was accepted")
	}
	if code := lifecycleErrorCode(err); code != "UNSUPPORTED_DERIVE_SOURCE_FIELD" && code != "INVALID_DERIVE_SOURCE_CHOICE" {
		t.Fatalf("vanished restore error = %v, want a forged-token-style refusal at the proposal boundary", err)
	}
	if harness.store.saveDraftCalls != 0 || string(acceptedBefore) != string(harness.store.created.DraftConfig) {
		t.Fatalf("refused restore changed the accepted draft: saves=%d", harness.store.saveDraftCalls)
	}
}

func deriveSourceAddStepWithLiteral(stepID, leftID string, literal int64, outputs []authoringv2.StageColumn) authoringv2.ConstructionStep {
	step := deriveSourceAddStep(stepID, leftID, outputs)
	step.Operation.Derive.Right.Literal.Integer = deriveSourceInt(literal)
	return step
}

func (h *deriveSourcePatientHarness) saveDraftDerive(t *testing.T, construction authoringv2.Construction) {
	t.Helper()
	h.saveDraft(t, construction)
}

func (h *deriveSourcePatientHarness) saveDraftDurableDerive(t *testing.T, construction authoringv2.Construction) {
	t.Helper()
	h.saveDraft(t, construction)
}

func (h *deriveSourcePatientHarness) candidateDeriveConstruction() authoringv2.Construction {
	candidate := authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{
		deriveSourceAddStepWithLiteral(deriveSourceStep, deriveSourceBirth, 1, append(deriveSourcePatientIdentityColumns(), deriveSourcePatientProjectionColumn(), authoringv2.StageColumn{ID: "derived_total", Name: "derived_total", Label: "Derived total", Type: "integer"})),
	}}
	return candidate
}

func TestConstructionDeriveSourceCapabilityReportsPreciseSkipClassReasonCodes(t *testing.T) {
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
		return rowSourceLaneCandidate(deriveSourceRootNode, deriveSourceRootType, id, path, logicalType, cardinality, populated)
	}
	candidatesSnapshot := func(token string, candidates ...capability.Candidate) capability.Snapshot {
		snapshot := deriveSourceSnapshot(token)
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
			name: "every numeric-eligible field has no observed values",
			snapshot: candidatesSnapshot("derive-source-precise-empty",
				bs("bs-id", "id", "string", "optional_one", false),
				bs("bs-active", deriveSourceField, "boolean", "optional_one", false)),
			service: deriveSourceService(t), construction: empty,
			stageID:  recipe.ConstructionSourceProjectionID,
			wantCode: "NO_OBSERVED_VALUES_DERIVE_SOURCE_FIELDS",
		},
		{
			name: "every populated field is not numeric",
			snapshot: candidatesSnapshot("derive-source-precise-type",
				bs("bs-id", "id", "string", "optional_one", true),
				bs("bs-active", deriveSourceField, "boolean", "optional_one", true)),
			service: deriveSourceService(t), construction: empty,
			stageID:  recipe.ConstructionSourceProjectionID,
			wantCode: "NO_TYPE_ELIGIBLE_DERIVE_SOURCE_FIELDS",
		},
		{
			name:     "every field is repeated at the row root",
			snapshot: candidatesSnapshot("derive-source-precise-route", bs("bs-active", deriveSourceField, "boolean", "many", true)),
			service:  deriveSourceService(t), construction: empty,
			stageID:  recipe.ConstructionSourceProjectionID,
			wantCode: "NO_ROUTE_ELIGIBLE_DERIVE_SOURCE_FIELDS",
		},
		{
			name:     "choices were issued for a previous dataset snapshot",
			snapshot: deriveSourceSnapshot("derive-source-precise-stale"),
			service: rowSourceServiceWithPlanner(staleRowSourceChoicePlanner{
				inner:  resolver,
				issued: rowSourceSnapshotWithGeneration(deriveSourceSnapshot("derive-source-precise-stale"), "derive-source-precise-stale-issued"),
			}),
			construction: empty, stageID: recipe.ConstructionSourceProjectionID,
			wantCode: "STALE_DERIVE_SOURCE_SNAPSHOT",
		},
		{
			name:     "the construction prefix cannot carry root fields to the stage",
			snapshot: deriveSourceSnapshot("derive-source-precise-reach"),
			service:  deriveSourceService(t), construction: rowChanging, stageID: "group-first",
			stages:   blockedStages,
			wantCode: "ROOT_SOURCE_PROJECTION_UNAVAILABLE",
		},
		{
			name:     "the authorized choice planner is not configured",
			snapshot: deriveSourceSnapshot("derive-source-precise-planner"),
			service:  rowSourceServiceWithPlanner(nil), construction: empty,
			stageID:  recipe.ConstructionSourceProjectionID,
			wantCode: "ROW_CHOICE_UNAVAILABLE",
		},
		{
			name:     "the planner enumerates no fields at all",
			snapshot: deriveSourceSnapshot("derive-source-precise-none"),
			service:  rowSourceServiceWithPlanner(emptyRowSourceChoicePlanner{}), construction: empty,
			stageID:  recipe.ConstructionSourceProjectionID,
			wantCode: "NO_ELIGIBLE_POPULATED_DERIVE_SOURCE_FIELDS",
		},
		{
			name: "distinct skipped classes stay on the generic fallback",
			snapshot: candidatesSnapshot("derive-source-precise-mixed",
				bs("bs-id", "id", "string", "optional_one", false),
				bs("bs-active", deriveSourceField, "boolean", "optional_one", true)),
			service: deriveSourceService(t), construction: empty,
			stageID:  recipe.ConstructionSourceProjectionID,
			wantCode: "NO_ELIGIBLE_POPULATED_DERIVE_SOURCE_FIELDS",
		},
	}
	genericCode := "NO_ELIGIBLE_POPULATED_DERIVE_SOURCE_FIELDS"
	distinct := map[string]string{}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			base := deriveSourceBase(row.snapshot, row.construction)
			base.stages = append(base.stages, row.stages...)
			result, err := row.service.constructionDeriveSourceCapability(context.Background(), base, deriveSourceOutput, row.stageID)
			if err != nil {
				t.Fatalf("derive source capability: %v", err)
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

func TestConstructionDeriveSourceCapabilityNamesInventoryFailureDistinctly(t *testing.T) {
	const internalDetail = "capability-store-dsn=secret-token"
	inventoryErr := errors.New("row-choice inventory store unreachable: " + internalDetail)
	empty := authoringv2.Construction{Version: authoringv2.ConstructionVersion}
	observedSnapshot := deriveSourceSnapshot("derive-source-inventory-observed")
	observedSnapshot.Candidates = []capability.Candidate{
		rowSourceLaneCandidate(deriveSourceRootNode, deriveSourceRootType, "bs-active", deriveSourceField, "boolean", "optional_one", false),
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
			snapshot: deriveSourceSnapshot("derive-source-inventory-failure"),
			wantCode: "DERIVE_SOURCE_INVENTORY_UNAVAILABLE", wantFail: true},
		{name: "a planner that enumerates nothing stays generic",
			service:  rowSourceServiceWithPlanner(emptyRowSourceChoicePlanner{}),
			snapshot: deriveSourceSnapshot("derive-source-inventory-empty"),
			wantCode: "NO_ELIGIBLE_POPULATED_DERIVE_SOURCE_FIELDS"},
		{name: "truly empty data names the observed class, not the inventory class",
			service: deriveSourceService(t), snapshot: observedSnapshot,
			wantCode: "NO_OBSERVED_VALUES_DERIVE_SOURCE_FIELDS"},
	}
	distinct := map[string]bool{}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			result, err := row.service.constructionDeriveSourceCapability(context.Background(),
				deriveSourceBase(row.snapshot, empty), deriveSourceOutput, recipe.ConstructionSourceProjectionID)
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
