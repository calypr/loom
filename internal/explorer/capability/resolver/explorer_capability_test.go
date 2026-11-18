package resolver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	loomapi "github.com/calypr/loom/api/loomapi"
	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
	compilerprobe "github.com/calypr/loom/internal/dataframe/compiler/capability"
	"github.com/calypr/loom/internal/dataframe/unit"
	"github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/testkit/capabilityfixtures"
)

type staticCapabilityEvidence = capabilityfixtures.StaticEvidence

type staticActiveManifest = capabilityfixtures.StaticActiveManifest

func testAuthorizedCapabilitySnapshot(t *testing.T, generation string, scope authscope.ReadScope) capability.Snapshot {
	t.Helper()
	return capability.NewSnapshot(
		capability.SnapshotIdentity{
			Project: "project-a", Generation: generation, AuthorizationScopeDigest: ScopeDigest(scope),
			SchemaDigest: strings.Repeat("a", 64), ResourceInventoryDigest: "inventory", RelationshipDigest: "relationships", FieldDigest: "fields",
			ProtocolVersion: ProtocolVersion, CompilerVersion: CompilerVersion,
			TraversalPolicyVersion: TraversalPolicyVersion, ProjectionPolicyVersion: ProjectionPolicyVersion,
		},
		capability.Policy{Route: capability.RoutePolicy{Version: TraversalPolicyVersion, AllowsRepeatedEdges: true, AllowsSelfLoops: true}, Projection: capability.ProjectionPolicy{Version: ProjectionPolicyVersion}},
		capability.StatusReady, true, false,
		[]capability.Node{{ID: "n_patient", ResourceType: "Patient", RowRootEligible: true, RowGrain: "RESOURCE", Populated: true, SupportedOperations: []capability.Operation{capability.OperationSelect}}},
		nil, nil, nil,
	)
}

func TestAuthoringV2CatalogMatchesCompilerCatalog(t *testing.T) {
	snapshot := capability.NewSnapshot(
		capability.SnapshotIdentity{Project: "project-a", Generation: "generation-a", AuthorizationScopeDigest: "scope", SchemaDigest: "schema"},
		capability.Policy{}, capability.StatusReady, true, false,
		[]capability.Node{{ID: "n_patient", ResourceType: "Patient", RowRootEligible: true}}, nil,
		[]capability.Candidate{{
			ID: "c_patient_birth_date", NodeID: "n_patient", ResourceType: "Patient", FieldPath: "birthDate", Label: "Birth date", LogicalType: "date", Cardinality: "optional_one",
			ProjectionModes:     []capability.ProjectionMode{capability.ProjectionScalar},
			SupportedOperations: []capability.Operation{capability.OperationFilter, capability.OperationChart},
		}}, nil,
	)
	want := authoringv2.CatalogFromCapability(snapshot, "default")
	got := authoringv2.CatalogFromCapability(snapshot, "default")
	digest, err := want.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if digest != "sha256:175bc5aa3881633ccb5e1830d64524827c3d24e1f2c14ac08cb10270cc42ac5d" {
		t.Fatalf("server catalog digest changed: %s", digest)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("compiler catalog differs from server catalog:\ncompiler: %#v\nserver:   %#v", got, want)
	}
}

type capabilityTestResourceAccess struct{ resources []string }

func (c capabilityTestResourceAccess) GetAllowedResources(context.Context, string, string, string) ([]string, error) {
	return append([]string(nil), c.resources...), nil
}

func testCapabilityScopeResolver(paths []string) *authscope.ScopeResolver {
	return authscope.NewScopeResolver(authscope.ScopeResolverConfig{
		ResourceAccess: capabilityTestResourceAccess{resources: []string{"/programs/example/projects/allowed"}},
		ListExistingAuthResourcePaths: func(context.Context, catalog.AuthResourcePathOptions) ([]string, error) {
			return append([]string(nil), paths...), nil
		},
	})
}

func TestAuthoringV2CatalogExposesCandidateFieldPath(t *testing.T) {
	snapshot := capability.NewSnapshot(
		capability.SnapshotIdentity{Project: "project-a", Generation: "generation-a"},
		capability.Policy{}, capability.StatusReady, true, false,
		[]capability.Node{{ID: "n_patient", ResourceType: "Patient", RowRootEligible: true}},
		nil,
		[]capability.Candidate{{
			ID: "c_patient_birth_date", NodeID: "n_patient", ResourceType: "Patient",
			FieldPath: "birthDate", Label: "Birth date", LogicalType: "date",
			Cardinality: "optional_one", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar},
			AggregateOperations: []capability.AggregateOperationCapability{{
				Operation: capability.AggregateSum, RowContext: capability.AggregateRowsRecords, Supported: false,
				ReasonCode: "NUMERIC_INPUT_REQUIRED", Reason: "SUM requires an integer or decimal input",
			}},
		}},
		nil,
	)

	wire := authoringv2.CatalogFromCapability(snapshot, "default")
	if len(wire.Candidates) != 1 || wire.Candidates[0].FieldPath != "birthDate" {
		t.Fatalf("catalog candidates = %#v", wire.Candidates)
	}
	choice := wire.Candidates[0].ConstructionChoice
	if choice == nil || choice.ChoiceID == "" || len(choice.Options) != 1 || choice.Options[0].Form != capability.ConstructionChoiceValue || choice.Options[0].Decision != capability.ConstructionChoiceDefault {
		t.Fatalf("field construction choice = %#v", choice)
	}
	encoded, err := json.Marshal(wire.Candidates[0])
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &decoded); err != nil || len(decoded["constructionChoice"]) == 0 {
		t.Fatalf("construction choice missing from candidate JSON %s: %v", encoded, err)
	}
	var aggregateChoices []capability.AggregateOperationCapability
	if err := json.Unmarshal(decoded["aggregateOperations"], &aggregateChoices); err != nil || len(aggregateChoices) != 1 || aggregateChoices[0].ReasonCode != "NUMERIC_INPUT_REQUIRED" || aggregateChoices[0].RowContext != capability.AggregateRowsRecords {
		t.Fatalf("typed aggregate choices missing or inaccurate in candidate JSON %s: choices=%#v err=%v", encoded, aggregateChoices, err)
	}
	var generated loomapi.CatalogCandidate
	if err := json.Unmarshal(encoded, &generated); err != nil {
		t.Fatalf("generated candidate boundary rejected choice: %v", err)
	}
	if len(generated.AggregateOperations) != 1 || generated.AggregateOperations[0].ReasonCode == nil || *generated.AggregateOperations[0].ReasonCode != "NUMERIC_INPUT_REQUIRED" || generated.Transformations.TemporalReduction.TimestampFields == nil || generated.Transformations.UnitNormalization.Presets == nil {
		t.Fatalf("generated candidate boundary lost typed compiler capabilities: %#v", generated)
	}
	fieldSource, err := generated.ConstructionChoice.Source.AsFieldChoiceSource()
	if err != nil || fieldSource.Kind != loomapi.FieldChoiceSourceKindFIELD || fieldSource.CandidateId != "c_patient_birth_date" || fieldSource.Path != "birthDate" {
		t.Fatalf("generated field choice source=%#v err=%v", fieldSource, err)
	}
}

func TestAuthoringV2CatalogExposesCandidatePopulationWithoutChangingSnapshotIdentity(t *testing.T) {
	snapshot := capability.NewSnapshot(
		capability.SnapshotIdentity{Project: "project-a", Generation: "generation-a"},
		capability.Policy{}, capability.StatusReady, true, false,
		[]capability.Node{{ID: "n_patient", ResourceType: "Patient", RowRootEligible: true}},
		nil,
		[]capability.Candidate{
			{
				ID: "c_patient_birth_date", NodeID: "n_patient", ResourceType: "Patient",
				FieldPath: "birthDate", Label: "Birth date", LogicalType: "date",
				Cardinality: "optional_one", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar}, Populated: true,
			},
			{
				ID: "c_patient_deceased", NodeID: "n_patient", ResourceType: "Patient",
				FieldPath: "deceasedBoolean", Label: "Deceased", LogicalType: "boolean",
				Cardinality: "optional_one", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar}, Populated: false,
			},
		},
		nil,
	)
	beforeToken := snapshot.Token
	beforeCanonicalPayload := append([]byte(nil), snapshot.CanonicalPayload()...)

	wire := authoringv2.CatalogFromCapability(snapshot, "default")
	if len(wire.Candidates) != 2 {
		t.Fatalf("catalog candidates = %#v", wire.Candidates)
	}
	if snapshot.Token != beforeToken || !bytes.Equal(snapshot.CanonicalPayload(), beforeCanonicalPayload) {
		t.Fatal("public candidate population projection changed the immutable capability snapshot identity")
	}

	populatedByID := map[string]bool{}
	for _, candidate := range wire.Candidates {
		encoded, err := json.Marshal(candidate)
		if err != nil {
			t.Fatal(err)
		}
		var payload map[string]json.RawMessage
		if err := json.Unmarshal(encoded, &payload); err != nil {
			t.Fatal(err)
		}
		var populated bool
		if err := json.Unmarshal(payload["populated"], &populated); err != nil {
			t.Fatalf("candidate %q omitted its population flag: %s (err=%v)", candidate.ID, encoded, err)
		}
		populatedByID[candidate.ID] = populated

		var generated loomapi.CatalogCandidate
		if err := json.Unmarshal(encoded, &generated); err != nil {
			t.Fatalf("generated candidate boundary rejected population field: %v", err)
		}
		if generated.Populated != populated {
			t.Errorf("generated candidate %q populated=%v, want %v", candidate.ID, generated.Populated, populated)
		}
	}
	if populatedByID["c_patient_birth_date"] != true || populatedByID["c_patient_deceased"] != false {
		t.Fatalf("wire candidate population flags = %#v", populatedByID)
	}

	embeddedSpec, err := loomapi.GetSpecJSON()
	if err != nil {
		t.Fatal(err)
	}
	var specification struct {
		Components struct {
			Schemas map[string]struct {
				Required   []string                   `json:"required"`
				Properties map[string]json.RawMessage `json:"properties"`
			} `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(embeddedSpec, &specification); err != nil {
		t.Fatal(err)
	}
	candidateSchema := specification.Components.Schemas["CatalogCandidate"]
	if _, ok := candidateSchema.Properties["populated"]; !ok {
		t.Fatal("embedded CatalogCandidate schema omits populated")
	}
	if !slices.Contains(candidateSchema.Required, "populated") {
		t.Fatalf("embedded CatalogCandidate required fields omit populated: %#v", candidateSchema.Required)
	}
}

func TestBuilderStateGeneratedContractPreservesCandidateCapabilities(t *testing.T) {
	state := authoringv2.BuilderState{
		APIVersion:     authoringv2.APIVersion,
		Kind:           authoringv2.StateKind,
		LifecycleState: "NEW",
		Catalog: authoringv2.CatalogSnapshot{
			SourceGeneration:         "generation-a",
			AuthorizationScopeDigest: "scope-a",
			SnapshotToken:            "snapshot-a",
			Complete:                 true,
			Nodes:                    []authoringv2.CatalogNode{},
			Edges:                    []authoringv2.CatalogEdge{},
			Candidates: []authoringv2.CatalogCandidate{{
				ID:                    "c_height_value",
				NodeID:                "n_observation",
				FieldPath:             "valueQuantity.value",
				Label:                 "Height",
				LogicalType:           "decimal",
				Cardinality:           "optional_one",
				Filterable:            true,
				Chartable:             true,
				ProjectionModes:       []string{"VALUE"},
				DefaultProjectionMode: "VALUE",
				AggregateOperations: []capability.AggregateOperationCapability{{
					Operation:  capability.AggregateSum,
					RowContext: capability.AggregateRowsRecords,
					Supported:  true,
				}},
				Transformations: authoringv2.AggregateTransformationCapabilities{
					Temporal: authoringv2.TemporalReductionCapabilities{
						Available: true,
						TimestampFields: []authoringv2.TemporalFieldChoice{{
							CandidateID:  "c_observation_issued",
							NodeID:       "n_observation",
							ResourceType: "Observation",
							FieldPath:    "issued",
							Label:        "Issued",
						}},
						AnchorFields: []authoringv2.TemporalFieldChoice{{
							CandidateID:  "c_patient_birth_date",
							NodeID:       "n_patient",
							ResourceType: "Patient",
							FieldPath:    "birthDate",
							Label:        "Birth date",
						}},
					},
					UnitNormalization: authoringv2.UnitNormalizationCapabilities{
						Available: true,
						Presets: []authoringv2.UnitNormalizationPresetCapability{{
							PolicyID:  "ucum-pressure",
							Version:   "1",
							Target:    unit.UnitIdentity{System: "http://unitsofmeasure.org", Code: "mm[Hg]"},
							Available: true,
						}},
					},
				},
			}},
			RoutePolicy: authoringv2.RoutePolicy{AllowRepeatedEdges: true, AllowSelfLoops: true},
		},
	}
	noOperationSnapshot := capability.NewSnapshot(
		capability.SnapshotIdentity{Project: "project-a", Generation: "generation-no-operations"},
		capability.Policy{}, capability.StatusReady, true, false,
		[]capability.Node{{ID: "n_patient", ResourceType: "Patient", RowRootEligible: true}},
		nil,
		[]capability.Candidate{{
			ID: "c_patient_id_no_aggregates", NodeID: "n_patient", ResourceType: "Patient", FieldPath: "id", Label: "Patient ID", LogicalType: "string", Cardinality: "optional_one",
			ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar},
		}},
		nil,
	)
	noOperationCatalog := authoringv2.CatalogFromCapability(noOperationSnapshot, "default")
	if len(noOperationCatalog.Candidates) != 1 {
		t.Fatalf("no-operation catalog candidates = %#v, want one candidate", noOperationCatalog.Candidates)
	}
	if operations := noOperationCatalog.Candidates[0].AggregateOperations; operations == nil || len(operations) != 0 {
		t.Fatalf("no-operation candidate aggregate operations = %#v, want non-nil empty slice", operations)
	}
	state.Catalog.Candidates = append(state.Catalog.Candidates, noOperationCatalog.Candidates[0])

	generated, err := roundTripGeneratedContract[loomapi.BuilderState](state)
	if err != nil {
		t.Fatalf("convert Builder state through generated contract: %v", err)
	}
	encoded, err := json.Marshal(generated)
	if err != nil {
		t.Fatalf("marshal generated Builder state: %v", err)
	}
	if len(generated.Catalog.Candidates) != 2 {
		t.Fatalf("generated Builder candidates = %#v, want two candidates", generated.Catalog.Candidates)
	}
	candidate := generated.Catalog.Candidates[0]
	if len(candidate.AggregateOperations) != 1 {
		t.Fatalf("generated aggregate operations = %#v, want one supported SUM for RECORDS", candidate.AggregateOperations)
	}
	aggregateOperation := candidate.AggregateOperations[0]
	if string(aggregateOperation.Operation) != "SUM" || string(aggregateOperation.RowContext) != "RECORDS" || !aggregateOperation.Supported {
		t.Fatalf("generated aggregate operations = %#v, want supported SUM for RECORDS", candidate.AggregateOperations)
	}
	temporal := candidate.Transformations.TemporalReduction
	if !temporal.Available || len(temporal.TimestampFields) != 1 || len(temporal.AnchorFields) != 1 {
		t.Fatalf("generated temporal capabilities = %#v, want issued timestamp and birthDate anchor", temporal)
	}
	if got, want := temporal.TimestampFields[0], (authoringv2.TemporalFieldChoice{
		CandidateID:  "c_observation_issued",
		NodeID:       "n_observation",
		ResourceType: "Observation",
		FieldPath:    "issued",
		Label:        "Issued",
	}); got.CandidateId != want.CandidateID || got.NodeId != want.NodeID || got.ResourceType != want.ResourceType || got.FieldPath != want.FieldPath || got.Label != want.Label {
		t.Fatalf("generated temporal timestamp = %#v, want %#v", got, want)
	}
	if got, want := temporal.AnchorFields[0], (authoringv2.TemporalFieldChoice{
		CandidateID:  "c_patient_birth_date",
		NodeID:       "n_patient",
		ResourceType: "Patient",
		FieldPath:    "birthDate",
		Label:        "Birth date",
	}); got.CandidateId != want.CandidateID || got.NodeId != want.NodeID || got.ResourceType != want.ResourceType || got.FieldPath != want.FieldPath || got.Label != want.Label {
		t.Fatalf("generated temporal anchor = %#v, want %#v", got, want)
	}
	unitCapabilities := candidate.Transformations.UnitNormalization
	if !unitCapabilities.Available || len(unitCapabilities.Presets) != 1 {
		t.Fatalf("generated unit capabilities = %#v, want mm[Hg] target", unitCapabilities)
	}
	if got, want := unitCapabilities.Presets[0], (loomapi.UnitNormalizationPresetCapability{
		PolicyId:  "ucum-pressure",
		Version:   "1",
		Target:    loomapi.UnitIdentity{System: "http://unitsofmeasure.org", Code: "mm[Hg]"},
		Available: true,
	}); got != want {
		t.Fatalf("generated unit preset = %#v, want %#v", got, want)
	}
	if operations := generated.Catalog.Candidates[1].AggregateOperations; operations == nil || len(operations) != 0 {
		t.Fatalf("generated no-operation candidate aggregate operations = %#v, want non-nil empty slice", operations)
	}
	var rawState struct {
		Catalog struct {
			Candidates []struct {
				AggregateOperations json.RawMessage `json:"aggregateOperations"`
			} `json:"candidates"`
		} `json:"catalog"`
	}
	if err := json.Unmarshal(encoded, &rawState); err != nil {
		t.Fatalf("decode generated Builder candidate JSON: %v", err)
	}
	if got := string(rawState.Catalog.Candidates[1].AggregateOperations); got != "[]" {
		t.Fatalf("generated no-operation aggregateOperations JSON = %q, want []", got)
	}
}

func TestAuthoringV2CatalogPreservesDistinctArrayProjectionMode(t *testing.T) {
	snapshot := capability.NewSnapshot(
		capability.SnapshotIdentity{Project: "project-a", Generation: "generation-a"},
		capability.Policy{}, capability.StatusReady, true, false,
		[]capability.Node{{ID: "n_patient", ResourceType: "Patient", RowRootEligible: true}},
		nil,
		[]capability.Candidate{{
			ID: "c_patient_name", NodeID: "n_patient", ResourceType: "Patient", FieldPath: "name[]", Label: "Patient name", LogicalType: "string", Cardinality: "many",
			ProjectionModes: []capability.ProjectionMode{capability.ProjectionArray, capability.ProjectionDistinctArray},
		}},
		nil,
	)

	wire := authoringv2.CatalogFromCapability(snapshot, "default")
	if len(wire.Candidates) != 1 || len(wire.Candidates[0].ProjectionModes) != 2 {
		t.Fatalf("catalog candidates = %#v", wire.Candidates)
	}
	if wire.Candidates[0].ProjectionModes[0] != "ALL" || wire.Candidates[0].ProjectionModes[1] != "DISTINCT" {
		t.Fatalf("projection modes = %#v, want [ALL DISTINCT]", wire.Candidates[0].ProjectionModes)
	}
	encoded, err := json.Marshal(wire.Candidates[0])
	if err != nil {
		t.Fatal(err)
	}
	var generated loomapi.CatalogCandidate
	if err := json.Unmarshal(encoded, &generated); err != nil {
		t.Fatalf("generated candidate boundary rejected empty capabilities: %v", err)
	}
	if generated.AggregateOperations == nil || generated.Transformations.TemporalReduction.TimestampFields == nil || generated.Transformations.UnitNormalization.Presets == nil {
		t.Fatalf("generated candidate boundary lost empty capability arrays: %#v", generated)
	}
	choice := wire.Candidates[0].ConstructionChoice
	if choice == nil || len(choice.Options) != 2 || choice.Options[0].Form != capability.ConstructionChoiceAll || choice.Options[0].Decision != capability.ConstructionChoiceDefault {
		t.Fatalf("repeated field construction choice = %#v", choice)
	}
}

func TestAuthoringV2CatalogAdvertisesScalarRecodingAndCodedIdentityRefusal(t *testing.T) {
	snapshot := capability.NewSnapshot(
		capability.SnapshotIdentity{Project: "project-a", Generation: "generation-a"},
		capability.Policy{}, capability.StatusReady, true, false,
		[]capability.Node{{ID: "n_patient", ResourceType: "Patient", RowRootEligible: true}},
		nil,
		[]capability.Candidate{{
			ID: "c_patient_status", NodeID: "n_patient", ResourceType: "Patient", FieldPath: "status", Label: "Patient status", LogicalType: "string", Cardinality: "optional_one",
			ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar},
		}},
		nil,
	)

	wire := authoringv2.CatalogFromCapability(snapshot, "default")
	if len(wire.Candidates) != 1 || !wire.Candidates[0].ValueTransformations.ExactCategoryRecode.Available {
		t.Fatalf("scalar recoding capability = %#v", wire.Candidates)
	}
	coded := wire.Candidates[0].ValueTransformations.CodedValueRecoding
	if coded.Available || coded.ReasonCode != "CODED_VALUE_RECODE_UNAVAILABLE" || !strings.Contains(coded.Reason, "Coding.system and Coding.code") {
		t.Fatalf("coded recoding capability = %#v", coded)
	}
	encoded, err := json.Marshal(wire.Candidates[0])
	if err != nil {
		t.Fatal(err)
	}
	var generated loomapi.CatalogCandidate
	if err := json.Unmarshal(encoded, &generated); err != nil {
		t.Fatalf("generated candidate boundary rejected value transformation capabilities: %v", err)
	}
	if !generated.ValueTransformations.ExactCategoryRecode.Available || generated.ValueTransformations.CodedValueRecoding.ReasonCode == nil || *generated.ValueTransformations.CodedValueRecoding.ReasonCode != "CODED_VALUE_RECODE_UNAVAILABLE" {
		t.Fatalf("generated candidate capability = %#v", generated.ValueTransformations)
	}
}

func TestGeneratedAuthoringCommandPreservesColumnTransformationUnion(t *testing.T) {
	const requestJSON = `{"commandId":"recode-status","semanticsVersion":7,"snapshotToken":"snapshot","expectedDraftVersion":3,"commands":[{"type":"UPDATE_COLUMN_TRANSFORMATION","outputId":"patients","column":"status","transformationChange":{"kind":"SET","transformation":{"kind":"EXACT_CATEGORY_RECODE","exactCategoryRecode":{"mappings":[{"from":"recorded-A","to":"group-1"}],"unknownPolicy":"ERROR"}}}}]}`
	var generated loomapi.ApplyCommandsRequest
	if err := json.Unmarshal([]byte(requestJSON), &generated); err != nil {
		t.Fatalf("generated OpenAPI command decoder rejected transformation union: %v", err)
	}
	request, err := roundTripGeneratedContract[authoringv2.ApplyCommandsRequest](generated)
	if err != nil {
		t.Fatalf("generated command conversion failed: %v", err)
	}
	if len(request.Commands) != 1 || request.Commands[0].Type != authoringv2.CommandUpdateColumnTransformation || request.Commands[0].TransformationChange == nil || request.Commands[0].TransformationChange.Transformation == nil {
		t.Fatalf("converted command = %#v", request.Commands)
	}
	transformation := request.Commands[0].TransformationChange.Transformation
	if transformation.Kind != "EXACT_CATEGORY_RECODE" || transformation.ExactCategoryRecode == nil || transformation.ExactCategoryRecode.UnknownPolicy != "ERROR" || len(transformation.ExactCategoryRecode.Mappings) != 1 || transformation.ExactCategoryRecode.Mappings[0].From != "recorded-A" || transformation.ExactCategoryRecode.Mappings[0].To != "group-1" {
		t.Fatalf("converted transformation = %#v", transformation)
	}
}

func TestAuthoringV2CatalogOmitsCandidateWithoutExecutableConstructionChoice(t *testing.T) {
	snapshot := capability.NewSnapshot(
		capability.SnapshotIdentity{Project: "project-a", Generation: "generation-a"},
		capability.Policy{}, capability.StatusReady, true, false,
		[]capability.Node{{ID: "n_patient", ResourceType: "Patient", RowRootEligible: true}},
		nil,
		[]capability.Candidate{{
			ID: "c_patient_name", NodeID: "n_patient", ResourceType: "Patient", FieldPath: "name[]", Label: "Patient name", LogicalType: "string", Cardinality: "many",
			ProjectionModes: []capability.ProjectionMode{capability.ProjectionIndexed},
		}},
		nil,
	)

	wire := authoringv2.CatalogFromCapability(snapshot, "default")
	if len(wire.Candidates) != 0 || len(wire.Diagnostics) != 1 || wire.Diagnostics[0].Code != "CONSTRUCTION_CHOICE_UNAVAILABLE" {
		t.Fatalf("catalog=%#v, want candidate omitted with diagnostic", wire)
	}
}

func TestAuthoringV2CatalogBlocksObjectProjectionWithTypedChildHint(t *testing.T) {
	snapshot := capability.NewSnapshot(
		capability.SnapshotIdentity{Project: "project-a", Generation: "generation-a"},
		capability.Policy{}, capability.StatusReady, true, false,
		[]capability.Node{{ID: "n_specimen", ResourceType: "Specimen", RowRootEligible: true}}, nil,
		[]capability.Candidate{
			{ID: "c_specimen_coding", NodeID: "n_specimen", ResourceType: "Specimen", FieldPath: "type.coding[]", LogicalType: "unknown", Cardinality: "many", ProjectionModes: []capability.ProjectionMode{capability.ProjectionArray}},
			{ID: "c_specimen_coding_system", NodeID: "n_specimen", ResourceType: "Specimen", FieldPath: "type.coding[].system", LogicalType: "string", Cardinality: "optional_one", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar}},
		}, nil,
	)

	wire := authoringv2.CatalogFromCapability(snapshot, "default")
	if len(wire.Candidates) != 1 || wire.Candidates[0].FieldPath != "type.coding[].system" {
		t.Fatalf("catalog candidates = %#v, want only the typed child field", wire.Candidates)
	}
	if len(wire.Diagnostics) != 1 || wire.Diagnostics[0].Code != "UNSUPPORTED_CONSTRUCTION_SOURCE_TYPE" ||
		!strings.Contains(wire.Diagnostics[0].Message, "Specimen.type.coding[]") ||
		!strings.Contains(wire.Diagnostics[0].Message, "Specimen.type.coding[].system") {
		t.Fatalf("catalog diagnostics = %#v, want unsupported field and typed child guidance", wire.Diagnostics)
	}
}

func TestExplorerCapabilityResolverAuthorizedCompilationRequiresActiveGeneration(t *testing.T) {
	manifest := testCapabilityManifest(t)
	store := capabilityfixtures.NewStore()
	snapshot := testAuthorizedCapabilitySnapshot(t, "generation-a", authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted})
	if _, err := store.Put(context.Background(), snapshot); err != nil {
		t.Fatal(err)
	}
	resolver, err := NewResolver(staticCapabilityEvidence{}, nil, staticActiveManifest{Manifest: manifest}, store)
	if err != nil {
		t.Fatal(err)
	}
	authorized, err := resolver.ResolveForCompilation(context.Background(), "project-a", snapshot.Token)
	if err != nil {
		t.Fatal(err)
	}
	if authorized.Snapshot.Token != snapshot.Token || authorized.Scope.Mode != authscope.ReadScopeUnrestricted {
		t.Fatalf("authorized compilation capability = %#v", authorized)
	}

	inactive := testAuthorizedCapabilitySnapshot(t, "generation-old", authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted})
	if _, err := store.Put(context.Background(), inactive); err != nil {
		t.Fatal(err)
	}
	if _, err := resolver.ResolveForCompilation(context.Background(), "project-a", inactive.Token); !errors.Is(err, capability.ErrStaleSnapshot) {
		t.Fatalf("inactive compilation token error = %v, want stale snapshot", err)
	}
}

func TestExplorerCapabilityResolverAuthorizedExecutionRetainsInactiveGeneration(t *testing.T) {
	manifest := testCapabilityManifest(t)
	store := capabilityfixtures.NewStore()
	snapshot := testAuthorizedCapabilitySnapshot(t, "generation-old", authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted})
	if _, err := store.Put(context.Background(), snapshot); err != nil {
		t.Fatal(err)
	}
	resolver, err := NewResolver(staticCapabilityEvidence{}, nil, staticActiveManifest{Manifest: manifest}, store)
	if err != nil {
		t.Fatal(err)
	}
	authorized, err := resolver.ResolveForExecution(context.Background(), "project-a", snapshot.Token)
	if err != nil {
		t.Fatal(err)
	}
	if authorized.Snapshot.Identity.Generation != "generation-old" {
		t.Fatalf("execution generation = %q", authorized.Snapshot.Identity.Generation)
	}
}

func TestExplorerCapabilityResolverEnforcesProjectAuthorizationWithoutScopeResolver(t *testing.T) {
	manifest := testCapabilityManifest(t)
	store := capabilityfixtures.NewStore()
	snapshot := testAuthorizedCapabilitySnapshot(t, "generation-a", authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted})
	if _, err := store.Put(context.Background(), snapshot); err != nil {
		t.Fatal(err)
	}
	resolver, err := NewResolver(staticCapabilityEvidence{}, nil, staticActiveManifest{Manifest: manifest}, store)
	if err != nil {
		t.Fatal(err)
	}
	ctx := authscope.ContextWithPrincipal(context.Background(), &authscope.Principal{Projects: []string{"another-project"}})
	if _, err := resolver.ResolveForExecution(ctx, "project-a", snapshot.Token); !errors.Is(err, authscope.ErrForbidden) {
		t.Fatalf("project authorization error = %v, want forbidden", err)
	}
}

func TestExplorerCapabilityResolverRejectsChangedScopeAndPreservesRestrictedEmpty(t *testing.T) {
	manifest := testCapabilityManifest(t)
	store := capabilityfixtures.NewStore()
	unrestricted := testAuthorizedCapabilitySnapshot(t, "generation-a", authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted})
	if _, err := store.Put(context.Background(), unrestricted); err != nil {
		t.Fatal(err)
	}
	scopedResolver, err := NewResolver(staticCapabilityEvidence{}, testCapabilityScopeResolver([]string{"example-other"}), staticActiveManifest{Manifest: manifest}, store)
	if err != nil {
		t.Fatal(err)
	}
	ctx := authscope.ContextWithPrincipal(context.Background(), &authscope.Principal{AuthorizationHeader: "Bearer token"})
	if _, err := scopedResolver.ResolveForExecution(ctx, "project-a", unrestricted.Token); !errors.Is(err, capability.ErrStaleSnapshot) {
		t.Fatalf("changed scope error = %v, want stale snapshot", err)
	}

	emptyScope := authscope.ReadScope{Mode: authscope.ReadScopeRestricted}
	empty := testAuthorizedCapabilitySnapshot(t, "generation-a", emptyScope)
	if _, err := store.Put(context.Background(), empty); err != nil {
		t.Fatal(err)
	}
	authorized, err := scopedResolver.ResolveForExecution(ctx, "project-a", empty.Token)
	if err != nil {
		t.Fatal(err)
	}
	if authorized.Scope.Unrestricted() || len(authorized.Scope.AuthResourcePaths) != 0 {
		t.Fatalf("restricted-empty scope = %#v", authorized.Scope)
	}
	if authorized.Scope.Mode != emptyScope.Mode {
		t.Fatalf("restricted-empty mode = %q, want %q", authorized.Scope.Mode, emptyScope.Mode)
	}

	authorized.Scope.AuthResourcePaths = append(authorized.Scope.AuthResourcePaths, "mutated")
	again, err := scopedResolver.ResolveForExecution(ctx, "project-a", empty.Token)
	if err != nil || len(again.Scope.AuthResourcePaths) != 0 || again.Scope.Unrestricted() {
		t.Fatalf("scope alias leaked after mutation: %#v, %v", again.Scope, err)
	}
}

func TestExplorerCapabilityResolverBuildsAndReusesCompilerProvenSnapshot(t *testing.T) {
	manifest := testCapabilityManifest(t)
	inventory := []catalog.ResourceInventoryObservation{{Project: "project-a", DatasetGeneration: "generation-a", ResourceType: "Patient", DocumentCount: 2}}
	fields := []catalog.FieldEnrichmentObservation{{Project: "project-a", DatasetGeneration: "generation-a", ResourceType: "Patient", Path: "id", Kind: "scalar", DocCount: 2}}
	inventoryDigest, _ := catalog.ResourceInventoryDigest(inventory)
	relationshipDigest, _ := catalog.RelationshipObservationDigest(nil)
	fieldDigest, _ := catalog.FieldEnrichmentDigest(fields, true, true, false)
	evidence := staticCapabilityEvidence{
		Inventory:     catalog.ResourceInventoryResult{Values: inventory, Available: true, Complete: true, Status: catalog.EvidenceAvailable, Digest: inventoryDigest},
		Relationships: catalog.RelationshipObservationResult{Values: []catalog.RelationshipObservation{}, Available: true, Complete: true, Status: catalog.EvidenceEmpty, Digest: relationshipDigest},
		Fields:        catalog.FieldEnrichmentResult{Values: fields, Available: true, Complete: true, FieldCoverageKnown: true, Status: catalog.EvidenceAvailable, Digest: fieldDigest},
	}
	repository := capabilityfixtures.NewStore()
	resolver, err := NewResolver(evidence, nil, staticActiveManifest{Manifest: manifest}, repository)
	if err != nil {
		t.Fatal(err)
	}
	first, err := resolver.Resolve(context.Background(), "project-a", "")
	if err != nil {
		t.Fatal(err)
	}
	if !first.Usable() || len(first.Nodes) != 1 || first.Nodes[0].ResourceType != "Patient" || len(first.Candidates) != 1 || first.Candidates[0].FieldPath != "id" {
		t.Fatalf("snapshot=%#v", first)
	}
	if first.Availability == nil || first.Availability.Readiness.Status != "COMPLETE" || first.Availability.Readiness.FieldProfiles.Status != "COMPLETE" || len(first.Availability.Resources) != 1 || len(first.Availability.Fields) != 1 || first.Availability.Fields[0].PathObservedCount != 2 {
		t.Fatalf("availability=%#v", first.Availability)
	}
	if first.Identity.ResourceInventoryDigest != inventoryDigest || first.Identity.FieldDigest != fieldDigest {
		t.Fatalf("identity=%#v", first.Identity)
	}
	second, err := resolver.Resolve(context.Background(), "project-a", "generation-a")
	if err != nil {
		t.Fatal(err)
	}
	if second.Token != first.Token {
		t.Fatalf("immutable snapshot was rebuilt with a different token: %q != %q", second.Token, first.Token)
	}
	loaded, err := resolver.ResolveToken(context.Background(), "project-a", first.Token)
	if err != nil || loaded.Token != first.Token {
		t.Fatalf("ResolveToken() = %#v, %v", loaded, err)
	}
}

func TestExplorerCapabilityResolverRebuildsLegacyIdentityAndRetainsOldToken(t *testing.T) {
	manifest := testCapabilityManifest(t)
	inventory := []catalog.ResourceInventoryObservation{{Project: "project-a", DatasetGeneration: "generation-a", ResourceType: "Patient", DocumentCount: 2}}
	fields := []catalog.FieldEnrichmentObservation{{Project: "project-a", DatasetGeneration: "generation-a", ResourceType: "Patient", Path: "id", Kind: "scalar", DocCount: 2}}
	inventoryDigest, _ := catalog.ResourceInventoryDigest(inventory)
	relationshipDigest, _ := catalog.RelationshipObservationDigest(nil)
	fieldDigest, _ := catalog.FieldEnrichmentDigest(fields, false, true, false)
	evidence := staticCapabilityEvidence{
		Inventory:     catalog.ResourceInventoryResult{Values: inventory, Available: true, Complete: true, Status: catalog.EvidenceAvailable, Digest: inventoryDigest},
		Relationships: catalog.RelationshipObservationResult{Values: []catalog.RelationshipObservation{}, Available: true, Complete: true, Status: catalog.EvidenceEmpty, Digest: relationshipDigest},
		Fields:        catalog.FieldEnrichmentResult{Values: fields, Available: true, Complete: true, FieldCoverageKnown: false, Status: catalog.EvidenceAvailable, Digest: fieldDigest},
	}
	repository := capabilityfixtures.NewStore()
	resolver, err := NewResolver(evidence, nil, staticActiveManifest{Manifest: manifest}, repository)
	if err != nil {
		t.Fatal(err)
	}
	current, err := resolver.Resolve(context.Background(), "project-a", "")
	if err != nil {
		t.Fatal(err)
	}
	if !current.Usable() || len(current.Candidates) != 1 || current.Availability == nil || current.Availability.Readiness.Status != "UNKNOWN" || current.Availability.Readiness.FieldProfiles.Status != "UNKNOWN" {
		t.Fatalf("legacy field coverage changed compiler behavior or readiness: %#v", current)
	}
	legacyIdentity := current.Identity
	legacyIdentity.CacheVersion = ""
	legacy := capability.NewSnapshot(legacyIdentity, current.Policy, current.Status, current.Complete, current.Truncated, current.Nodes, current.Edges, current.Candidates, current.Diagnostics)
	if legacy.Availability != nil {
		t.Fatal("legacy fixture unexpectedly has availability metadata")
	}
	if _, err := repository.Put(context.Background(), legacy); err != nil {
		t.Fatal(err)
	}
	repository.DeleteIdentity(current.Identity)

	rebuilt, err := resolver.Resolve(context.Background(), "project-a", "")
	if err != nil {
		t.Fatal(err)
	}
	if rebuilt.Identity.CacheVersion != cacheVersion || rebuilt.Availability == nil || rebuilt.Token == legacy.Token {
		t.Fatalf("current identity did not rebuild metadata: %#v", rebuilt)
	}
	retained, err := resolver.ResolveToken(context.Background(), "project-a", legacy.Token)
	if err != nil || retained.Token != legacy.Token || retained.Availability != nil {
		t.Fatalf("legacy retained token changed: %#v, %v", retained, err)
	}
}

func TestExplorerCapabilityCacheSeparatesFieldCoverageReadiness(t *testing.T) {
	manifest := testCapabilityManifest(t)
	inventory := []catalog.ResourceInventoryObservation{
		{Project: "project-a", DatasetGeneration: "generation-a", ResourceType: "Observation", DocumentCount: 3},
		{Project: "project-a", DatasetGeneration: "generation-a", ResourceType: "Patient", DocumentCount: 2},
	}
	fields := []catalog.FieldEnrichmentObservation{
		{Project: "project-a", DatasetGeneration: "generation-a", ResourceType: "Observation", Path: "id", Kind: "scalar", DocCount: 3},
		{Project: "project-a", DatasetGeneration: "generation-a", ResourceType: "Patient", Path: "id", Kind: "scalar", DocCount: 2},
	}
	relationships := []catalog.RelationshipObservation{{
		Project: "project-a", DatasetGeneration: "generation-a", Label: "subject_Patient", EdgeCount: 2,
		StorageFromType: "Observation", StorageToType: "Patient", StorageDirection: "OUTBOUND",
		BuilderFromType: "Patient", BuilderToType: "Observation", BuilderDirection: "INBOUND",
		FromType: "Patient", ToType: "Observation",
	}}
	inventoryDigest, _ := catalog.ResourceInventoryDigest(inventory)
	relationshipDigest, _ := catalog.RelationshipObservationDigest(relationships)
	makeEvidence := func(coverageKnown bool) staticCapabilityEvidence {
		fieldDigest, err := catalog.FieldEnrichmentDigest(fields, coverageKnown, true, false)
		if err != nil {
			t.Fatal(err)
		}
		return staticCapabilityEvidence{
			Inventory:     catalog.ResourceInventoryResult{Values: inventory, Available: true, Complete: true, Status: catalog.EvidenceAvailable, Digest: inventoryDigest},
			Relationships: catalog.RelationshipObservationResult{Values: relationships, Available: true, Complete: true, Status: catalog.EvidenceAvailable, Digest: relationshipDigest},
			Fields:        catalog.FieldEnrichmentResult{Values: fields, Available: true, Complete: true, FieldCoverageKnown: coverageKnown, Status: catalog.EvidenceAvailable, Digest: fieldDigest},
		}
	}
	repository := capabilityfixtures.NewStore()
	knownResolver, err := NewResolver(makeEvidence(true), nil, staticActiveManifest{Manifest: manifest}, repository)
	if err != nil {
		t.Fatal(err)
	}
	known, err := knownResolver.Resolve(context.Background(), "project-a", "")
	if err != nil || !known.Usable() || known.Availability.Readiness.Status != "COMPLETE" {
		t.Fatalf("known profile readiness = %#v, %v", known.Availability, err)
	}
	legacyResolver, err := NewResolver(makeEvidence(false), nil, staticActiveManifest{Manifest: manifest}, repository)
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := legacyResolver.Resolve(context.Background(), "project-a", "")
	if err != nil || !legacy.Usable() || legacy.Availability.Readiness.Status != "UNKNOWN" || legacy.Availability.Readiness.FieldProfiles.Status != "UNKNOWN" || len(legacy.Candidates) != len(known.Candidates) {
		t.Fatalf("legacy profile readiness changed compiler behavior: %#v, %v", legacy, err)
	}
	if legacy.Token == known.Token {
		t.Fatal("field coverage readiness change reused a prior snapshot token")
	}
	if len(known.Edges) == 0 || !sameCapabilityEntityIDs(known, legacy) {
		t.Fatalf("availability readiness changed compiler graph sizes: known=%#v legacy=%#v", known, legacy)
	}

	preAvailabilityIdentity := known.Identity
	preAvailabilityIdentity.CacheVersion = ""
	preAvailabilityIdentity.FieldDigest, err = catalog.FieldEnrichmentDigest(fields)
	if err != nil {
		t.Fatal(err)
	}
	compiler := explorerCapabilityCompiler{scope: compilerprobe.Scope{
		Project: "project-a", DatasetGeneration: "generation-a", AuthScopeMode: authscope.ReadScopeUnrestricted,
	}}
	baselineEvidence := makeEvidence(true)
	baselineBuilder := capability.NewBuilder(preAvailabilityIdentity, capabilityEvidenceFromCatalog(catalog.CapabilityEvidence{
		ResourceInventory: baselineEvidence.Inventory,
		Relationships:     baselineEvidence.Relationships,
		FieldEnrichment:   baselineEvidence.Fields,
	}), capability.CompilerCallbacks{Node: compiler.ProbeNode, Edge: compiler.ProbeEdge, Candidate: compiler.ProbeCandidate})
	baselineBuilder.Policy = capability.Policy{
		Route:      capability.RoutePolicy{Version: TraversalPolicyVersion, MaxHops: 0, AllowsRepeatedEdges: true, AllowsSelfLoops: true},
		Projection: capability.ProjectionPolicy{Version: ProjectionPolicyVersion, SuggestionLimit: capability.DefaultSuggestionLimit},
	}
	preAvailability, err := baselineBuilder.Build(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !sameCapabilityEntityIDs(known, preAvailability) {
		t.Fatalf("availability metadata changed IDs from the pre-availability identity: current=%#v baseline=%#v", known, preAvailability)
	}
	retained, err := legacyResolver.ResolveToken(context.Background(), "project-a", known.Token)
	if err != nil || retained.Token != known.Token || retained.Availability.Readiness.Status != "COMPLETE" {
		t.Fatalf("retained known token was mutated: %#v, %v", retained.Availability, err)
	}
}

func sameCapabilityEntityIDs(a, b capability.Snapshot) bool {
	if len(a.Nodes) != len(b.Nodes) || len(a.Edges) != len(b.Edges) || len(a.Candidates) != len(b.Candidates) {
		return false
	}
	for i := range a.Nodes {
		if a.Nodes[i].ID != b.Nodes[i].ID {
			return false
		}
	}
	for i := range a.Edges {
		if a.Edges[i].ID != b.Edges[i].ID {
			return false
		}
	}
	for i := range a.Candidates {
		if a.Candidates[i].ID != b.Candidates[i].ID {
			return false
		}
	}
	return true
}

func TestExplorerCapabilityResolverFailsClosedOnIncompleteFieldEnrichment(t *testing.T) {
	manifest := testCapabilityManifest(t)
	inventory := []catalog.ResourceInventoryObservation{{Project: "project-a", DatasetGeneration: "generation-a", ResourceType: "Patient", DocumentCount: 1}}
	digest, _ := catalog.ResourceInventoryDigest(inventory)
	evidence := staticCapabilityEvidence{
		Inventory:     catalog.ResourceInventoryResult{Values: inventory, Available: true, Complete: true, Status: catalog.EvidenceAvailable, Digest: digest},
		Relationships: catalog.RelationshipObservationResult{Values: []catalog.RelationshipObservation{}, Available: true, Complete: true, Status: catalog.EvidenceEmpty, Digest: "sha256:relationships"},
		Fields:        catalog.FieldEnrichmentResult{Values: []catalog.FieldEnrichmentObservation{}, Available: true, Complete: false, Status: catalog.EvidenceIncomplete, Digest: "sha256:fields"},
	}
	resolver, err := NewResolver(evidence, nil, staticActiveManifest{Manifest: manifest}, capabilityfixtures.NewStore())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := resolver.Resolve(context.Background(), "project-a", ""); err == nil || !strings.Contains(err.Error(), "field profile enrichment") {
		t.Fatalf("Resolve() error=%v", err)
	}
}

func TestCapabilityEvidencePublishesBothStoredRelationshipDirections(t *testing.T) {
	evidence := capabilityEvidenceFromCatalog(catalog.CapabilityEvidence{
		Relationships: catalog.RelationshipObservationResult{Values: []catalog.RelationshipObservation{{
			Label: "subject_Patient", EdgeCount: 7,
			StorageFromType: "Specimen", StorageToType: "Patient", StorageDirection: "OUTBOUND",
			BuilderFromType: "Patient", BuilderToType: "Specimen", BuilderDirection: "INBOUND",
		}}},
	})
	got := map[string]capability.RelationshipObservation{}
	for _, relationship := range evidence.Relationships {
		got[relationship.SourceResourceType+"->"+relationship.TargetResourceType] = relationship
	}
	if outbound := got["Specimen->Patient"]; outbound.StorageDirection != "OUTBOUND" || outbound.ObservedEdgeCount != 7 {
		t.Fatalf("outbound relationship = %#v", outbound)
	}
	if inbound := got["Patient->Specimen"]; inbound.StorageDirection != "INBOUND" || inbound.ObservedEdgeCount != 7 {
		t.Fatalf("inbound relationship = %#v", inbound)
	}
}

func TestCapabilityEvidenceAggregatesObservedRelationshipsAcrossAuthPaths(t *testing.T) {
	evidence := capabilityEvidenceFromCatalog(catalog.CapabilityEvidence{
		Relationships: catalog.RelationshipObservationResult{Values: []catalog.RelationshipObservation{
			{
				AuthResourcePath: "subject", Label: "subject_Patient", EdgeCount: 3,
				StorageFromType: "Observation", StorageToType: "Patient", StorageDirection: "OUTBOUND",
				BuilderFromType: "Patient", BuilderToType: "Observation", BuilderDirection: "INBOUND",
			},
			{
				AuthResourcePath: "focus", Label: "subject_Patient", EdgeCount: 4,
				StorageFromType: "Observation", StorageToType: "Patient", StorageDirection: "OUTBOUND",
				BuilderFromType: "Patient", BuilderToType: "Observation", BuilderDirection: "INBOUND",
			},
		}},
	})
	if len(evidence.ObservedRelationships) != 1 {
		t.Fatalf("observed relationships = %#v, want one identity aggregated across auth paths", evidence.ObservedRelationships)
	}
	relationship := evidence.ObservedRelationships[0]
	if relationship.SourceResourceType != "Patient" || relationship.TargetResourceType != "Observation" || relationship.Label != "subject_Patient" || relationship.ObservedEdgeCount != 7 {
		t.Fatalf("aggregated relationship = %#v, want edge count 7", relationship)
	}
}

func TestCapabilityEvidenceRetainsObservedOwnerFieldsAndSemanticSummary(t *testing.T) {
	value := catalog.CapabilityEvidence{
		ResourceInventory: catalog.ResourceInventoryResult{Values: []catalog.ResourceInventoryObservation{
			{Project: "project-a", ResourceType: "Patient", DocumentCount: 3},
		}},
		Relationships: catalog.RelationshipObservationResult{Values: []catalog.RelationshipObservation{{
			Label: "subject_Patient", EdgeCount: 2, StorageFromType: "Observation", StorageToType: "Patient", StorageDirection: "OUTBOUND",
			BuilderFromType: "Patient", BuilderToType: "Observation", BuilderDirection: "INBOUND",
		}}},
		FieldEnrichment: catalog.FieldEnrichmentResult{Values: []catalog.FieldEnrichmentObservation{{
			ResourceType: "Patient", Path: "identifier", Kind: "object", DocCount: 3, DistinctValues: []string{"urn:mrn"},
			SemanticObservations: []catalog.SemanticObservation{{
				SchemaVersion: catalog.SemanticObservationSchemaVersion,
				Source:        catalog.SemanticObservationSource{Type: "Patient", Path: "identifier"},
				Key:           catalog.SemanticObservationKey{Selector: "system", System: "urn:mrn", Code: "MRN"},
				Value:         catalog.SemanticObservationValue{Selector: "value", Type: "string"},
				OwningScope:   "identifier", Completeness: catalog.SemanticComplete, Status: "SUPPORTED", Population: 3,
			}},
		}}},
	}
	evidence := capabilityEvidenceFromCatalog(value)
	var owner *capability.ObservedField
	for index := range evidence.ObservedFields {
		field := &evidence.ObservedFields[index]
		if field.ResourceType == "Patient" && field.Path == "identifier" {
			owner = field
			break
		}
	}
	if owner == nil || owner.ShapeKind != "object" || owner.PathObservedCount != 3 || len(owner.Examples) != 1 || len(owner.SemanticObservations) != 1 {
		t.Fatalf("observed owner field = %#v", owner)
	}
	if owner.SemanticObservations[0].Code != "MRN" || owner.SemanticObservations[0].SourceResourceType != "Patient" || owner.SemanticObservations[0].Status != "SUPPORTED" {
		t.Fatalf("semantic summary = %#v", owner.SemanticObservations[0])
	}
	for _, field := range evidence.Fields {
		if field.Path == "identifier" {
			t.Fatal("object semantic owner became a compiler field candidate")
		}
	}
	if len(evidence.ObservedRelationships) != 1 || evidence.ObservedRelationships[0].SourceResourceType != "Patient" || evidence.ObservedRelationships[0].TargetResourceType != "Observation" || evidence.ObservedRelationships[0].StorageDirection != "OUTBOUND" || evidence.ObservedRelationships[0].BuilderDirection != "INBOUND" {
		t.Fatalf("observed relationship = %#v", evidence.ObservedRelationships)
	}
}

func TestAuthoringV2CatalogCarriesAdditiveObservedAvailability(t *testing.T) {
	snapshot := capability.NewSnapshot(
		capability.SnapshotIdentity{Project: "project-a", Generation: "generation-a"}, capability.Policy{}, capability.StatusReady, true, false,
		[]capability.Node{{ID: "n_patient", ResourceType: "Patient", RowRootEligible: true}}, nil, nil, nil,
	)
	snapshot.Availability = &capability.Availability{
		Readiness: capability.AvailabilityReadiness{
			Status: "UNKNOWN", Reason: "field profile coverage is not proven",
			ResourceInventory: capability.AvailabilitySourceReadiness{Status: "COMPLETE"},
			Relationships:     capability.AvailabilitySourceReadiness{Status: "COMPLETE"},
			FieldProfiles:     capability.AvailabilitySourceReadiness{Status: "UNKNOWN", Reason: "field profile coverage is not proven"},
		},
		Resources:     []capability.ObservedResource{{ResourceType: "Patient", DocumentCount: 3, Populated: true}},
		Fields:        []capability.ObservedField{{ResourceType: "Organization", Path: "name", ShapeKind: "string-ish", PathObservedCount: 1, Examples: []string{"Clinic"}, ExamplesTruncated: true, SemanticObservations: []capability.ConceptCandidate{}}},
		Relationships: []capability.ObservedRelationship{{SourceResourceType: "Patient", TargetResourceType: "Organization", Label: "managingOrganization", StorageDirection: "OUTBOUND", BuilderDirection: "INBOUND", ObservedEdgeCount: 2}},
	}
	wire := authoringv2.CatalogFromCapability(snapshot, "default")
	if len(wire.Nodes) != 1 || len(wire.Candidates) != 0 || wire.Availability == nil || len(wire.Availability.Fields) != 1 || wire.Availability.Fields[0].ObservedShapeKind != "string-ish" {
		t.Fatalf("wire catalog changed compiler graph or lost observations: %#v", wire)
	}
	encoded, err := json.Marshal(wire)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &payload); err != nil {
		t.Fatal(err)
	}
	var availability map[string]json.RawMessage
	if err := json.Unmarshal(payload["availability"], &availability); err != nil || len(availability["readiness"]) == 0 || len(availability["resources"]) == 0 || len(availability["fields"]) == 0 || len(availability["relationships"]) == 0 {
		t.Fatalf("availability contract missing: %s err=%v", payload["availability"], err)
	}
	var fields []map[string]json.RawMessage
	if err := json.Unmarshal(availability["fields"], &fields); err != nil || len(fields) != 1 {
		t.Fatalf("availability fields = %s err=%v", availability["fields"], err)
	}
	for _, name := range []string{"observedShapeKind", "pathObservedDocumentCount", "examples", "examplesTruncated", "semanticObservations"} {
		if len(fields[0][name]) == 0 {
			t.Errorf("availability field omitted %q: %s", name, availability["fields"])
		}
	}
	var generated loomapi.Catalog
	if err := json.Unmarshal(encoded, &generated); err != nil || generated.Availability == nil || len(generated.Availability.Fields) != 1 || generated.Availability.Fields[0].ObservedShapeKind != "string-ish" || generated.Availability.Readiness.Status != loomapi.CatalogAvailabilityReadinessStatusUNKNOWN {
		t.Fatalf("generated Catalog contract lost availability: %#v err=%v", generated.Availability, err)
	}
	embeddedSpec, err := loomapi.GetSpecJSON()
	if err != nil {
		t.Fatal(err)
	}
	var specification struct {
		Components struct {
			Schemas map[string]struct {
				Properties map[string]json.RawMessage `json:"properties"`
			} `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(embeddedSpec, &specification); err != nil || len(specification.Components.Schemas["Catalog"].Properties["availability"]) == 0 || len(specification.Components.Schemas["CatalogObservedField"].Properties["pathObservedDocumentCount"]) == 0 {
		t.Fatalf("embedded OpenAPI spec is missing the additive availability schema: err=%v", err)
	}
}

func testCapabilityManifest(t *testing.T) dataset.Manifest {
	t.Helper()
	ref, err := dataset.NewRef("project-a", "generation-a")
	if err != nil {
		t.Fatal(err)
	}
	schema, err := dataset.NewSchemaSnapshot("urn:test", "R5", strings.Repeat("a", 64), []string{"Patient"})
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := dataset.NewManifest(ref, schema)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err = manifest.Transition(dataset.StateStaged)
	if err != nil {
		t.Fatal(err)
	}
	return manifest
}

func roundTripGeneratedContract[T any](value any) (T, error) {
	var result T
	payload, err := json.Marshal(value)
	if err != nil {
		return result, err
	}
	err = json.Unmarshal(payload, &result)
	return result, err
}
