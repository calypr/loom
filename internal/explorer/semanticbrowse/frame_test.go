package semanticbrowse

import (
	"crypto/sha256"
	"encoding/hex"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/explorer/capability"
	ws "github.com/calypr/loom/internal/explorer/workspace"
	"github.com/calypr/loom/internal/semantic"
)

func TestCodedPivotSavedSourceAnchorKeepsCurrentCodeableConceptFrameFamily(t *testing.T) {
	observation := semanticFrameCompatibilityObservation("codeable concept")
	observation.Key.System = "urn:stage"
	observation.Key.Code = "stage-one"
	observation.Key.Display = "Stage I"
	anchor := semantic.Entry{ConceptID: "concept-stage-one", BindingID: "stage-binding", Observation: observation}

	families := frameSourceFamilies(codedPivotFrameSnapshot(), []semantic.Entry{anchor})
	if len(families) != 1 {
		t.Fatalf("current CodeableConcept text + coding.display family count = %d, want 1", len(families))
	}
	family := families[0]
	if family.Family.SchemaVersion != semantic.ObservationSchemaVersion || family.Family.RuleVersion != strconv.Itoa(semantic.SemanticObservationRuleVersion) ||
		family.Family.ResourceType != "Observation" || family.Family.SourcePath != "code" ||
		family.Family.KeyPath != "code.coding[]" || family.Family.ValuePath != "valueCodeableConcept.text" ||
		family.Family.LogicalType != "string" {
		t.Fatalf("current semantic family = %#v", family.Family)
	}
	if !reflect.DeepEqual(family.Family.ValueFallbacks, []string{"valueCodeableConcept.coding[].display"}) || !family.Family.FallbackOnBlankString {
		t.Fatalf("current semantic family must include the opted-in coding.display fallback: %#v", family.Family)
	}
}

func codedPivotFrameSnapshot() capability.Snapshot {
	snapshot := readySnapshot("project-a", "generation-a", "coded-pivot-frame-snapshot", authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted})
	snapshot.Nodes = []capability.Node{{ID: "observation", ResourceType: "Observation", RowRootEligible: true}}
	snapshot.Candidates = []capability.Candidate{
		{
			ID: "observation-value-text", NodeID: "observation", ResourceType: "Observation",
			FieldPath: "valueCodeableConcept.text", Label: "Value text", LogicalType: "string",
			Cardinality: "optional_one", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar},
		},
		{
			ID: "observation-value-display", NodeID: "observation", ResourceType: "Observation",
			FieldPath: "valueCodeableConcept.coding[].display", Label: "Value coding display", LogicalType: "string",
			Cardinality: "many", ProjectionModes: []capability.ProjectionMode{capability.ProjectionArray, capability.ProjectionFirst},
		},
	}
	return snapshot
}

func TestStandaloneCodedConceptStaysOutOfPairedPivotFrames(t *testing.T) {
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	snapshot := readySnapshot("project-a", "generation-a", "snapshot-a", scope)
	snapshot.Nodes = []capability.Node{{ID: "condition", ResourceType: "Condition", RowRootEligible: true}}
	observation := semantic.Observation{
		SchemaVersion: semantic.ObservationSchemaVersion,
		Source:        semantic.Source{Type: "Condition", Path: "code"},
		Key:           semantic.Key{Selector: "code.coding[]", System: "urn:example:condition", Version: "2024", Code: "C1", Display: "Condition one"},
		Value: semantic.Value{
			Selector: "code.coding[].display", Type: "string",
			FallbackSelectors: []string{"code.coding[].code"}, FallbackOnBlankString: true,
		},
		LogicalType: "string", Completeness: semantic.CompletenessComplete, Status: "SUPPORTED",
		RuleHint: semantic.RuleHintCodedConceptV1, RuleVersion: strconv.Itoa(semantic.SemanticObservationRuleVersion),
	}
	entry := semantic.Entry{ConceptID: "concept-c1", BindingID: "binding-condition-code", Observation: observation}
	snapshot.Candidates = []capability.Candidate{{
		ID: "condition-code-display", NodeID: "condition", ResourceType: "Condition",
		FieldPath: "code.coding[].display", Label: "Condition code display", LogicalType: "string", Cardinality: "many",
		RepeatedBoundaries: []capability.RepeatedBoundary{{Path: "code.coding[]", MaxItems: 2}},
		ProjectionModes:    []capability.ProjectionMode{capability.ProjectionFirst, capability.ProjectionArray},
	}}
	if families := frameSourceFamilies(snapshot, []semantic.Entry{entry}); len(families) != 0 {
		t.Fatalf("standalone Coding unexpectedly became a paired coded-Pivot frame: %#v", families)
	}
}

func TestFrameSourceFormsExposeOnlyCompilerSupportedForms(t *testing.T) {
	forms := supportedFrameForms([]capability.ConstructionChoiceOption{
		{Form: capability.ConstructionChoiceValue, Decision: capability.ConstructionChoiceDefault, Support: capability.ConstructionChoiceSupported},
		{Form: capability.ConstructionChoiceFirst, Support: capability.ConstructionChoiceSupport("UNSUPPORTED")},
		{Form: capability.ConstructionChoiceAll, Decision: capability.ConstructionChoiceDefault, Support: capability.ConstructionChoiceSupported},
		{Form: capability.ConstructionChoiceDistinct, Decision: capability.ConstructionChoiceRequiresDecision, Support: capability.ConstructionChoiceSupported},
	})
	if len(forms) != 3 || forms[0].Form != capability.ConstructionChoiceValue || forms[1].Form != capability.ConstructionChoiceAll || forms[2].Form != capability.ConstructionChoiceDistinct {
		t.Fatalf("forms = %#v, want only compiler-supported VALUE, ALL, and DISTINCT", forms)
	}
	if form, ok := preservingFrameDefault(forms); !ok || form != capability.ConstructionChoiceValue {
		t.Fatalf("compiler-marked VALUE form was not recognized as the preserving default: form=%q forms=%#v", form, forms)
	}
	if form, ok := preservingFrameDefault([]FrameSourceForm{{Form: capability.ConstructionChoiceAll, ZeroPolicy: ws.FrameZeroEmptyList, ManyPolicy: ws.FrameManyAll, Decision: string(capability.ConstructionChoiceDefault)}}); !ok || form != capability.ConstructionChoiceAll {
		t.Fatalf("compiler-marked ALL was not accepted as a preserving default: form=%q ok=%t", form, ok)
	}
	if _, ok := preservingFrameDefault([]FrameSourceForm{{Form: capability.ConstructionChoiceFirst, ZeroPolicy: ws.FrameZeroNull, ManyPolicy: ws.FrameManyFirst, Decision: string(capability.ConstructionChoiceDefault)}}); ok {
		t.Fatal("lossy FIRST must not be accepted as a preserving default")
	}
}

func readySnapshot(project, generation, token string, scope authscope.ReadScope) capability.Snapshot {
	paths := append([]string(nil), scope.AuthResourcePaths...)
	sort.Strings(paths)
	sum := sha256.Sum256([]byte(string(scope.Mode) + "\x00" + strings.Join(paths, "\x00")))
	return capability.Snapshot{Identity: capability.SnapshotIdentity{Project: project, Generation: generation, AuthorizationScopeDigest: hex.EncodeToString(sum[:]), SchemaDigest: "schema", ShapeDigest: "shape"}, Status: capability.StatusReady, Complete: true, Token: token}
}

func semanticFrameCompatibilityObservation(kind string) semantic.Observation {
	observation := semantic.Observation{
		SchemaVersion: semantic.ObservationSchemaVersion,
		Source:        semantic.Source{Type: "Observation", Canonical: "Observation.code", Path: "code"},
		Key:           semantic.Key{Selector: "code.coding[]", System: "urn:study", Code: "stage"},
		ChoiceArm:     "valueQuantity", LogicalType: "decimal", Completeness: semantic.CompletenessComplete,
		Status: "SUPPORTED", RuleHint: semantic.RuleHintCodedValueV1, RuleVersion: strconv.Itoa(semantic.SemanticObservationRuleVersion),
		Value: semantic.Value{Selector: "valueQuantity.value", Type: "decimal"},
	}
	if kind == "codeable concept" {
		observation.Value = semantic.Value{Selector: "valueCodeableConcept.text", Type: "string", FallbackSelectors: []string{"valueCodeableConcept.coding[].display"}, FallbackOnBlankString: true}
		observation.ChoiceArm, observation.LogicalType = "valueCodeableConcept", "string"
	}
	return observation
}
