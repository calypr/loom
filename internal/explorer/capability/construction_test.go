package capability

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/catalog"
)

func TestNewFieldConstructionChoiceUsesCompilerProofsAndSafeDefaults(t *testing.T) {
	t.Parallel()

	t.Run("scalar", func(t *testing.T) {
		candidate := Candidate{
			ID: "candidate_scalar", NodeID: "node_patient", ResourceType: "Patient",
			FieldPath: "birthDate", Label: "Birth date", Cardinality: "optional_one",
			ProjectionModes: []ProjectionMode{ProjectionScalar, ProjectionFirst},
		}
		choice, err := NewFieldConstructionChoice("snapshot-a", candidate)
		if err != nil {
			t.Fatal(err)
		}
		source, ok := choice.Source.(FieldChoiceSource)
		if !ok || choice.ChoiceID == "" || source.Path != "birthDate" || source.ResourceType != "Patient" || source.NodeID != "node_patient" {
			t.Fatalf("choice lost exact source identity: %#v", choice)
		}
		if len(choice.Options) != 1 {
			t.Fatalf("non-repeated FIRST must not duplicate VALUE: %#v", choice.Options)
		}
		value := constructionOption(t, choice, ConstructionChoiceValue)
		if value.Decision != ConstructionChoiceDefault || value.Preservation != ConstructionChoicePreserving || value.Support != ConstructionChoiceSupported || value.Shape != ConstructionChoiceScalar || value.RowEffect != ConstructionChoicePreservesRows {
			t.Fatalf("scalar VALUE option is not the preserving default: %#v", value)
		}

		changedLabel := candidate
		changedLabel.Label = "Different presentation label"
		stable, err := NewFieldConstructionChoice("snapshot-a", changedLabel)
		if err != nil {
			t.Fatal(err)
		}
		if choice.ChoiceID != stable.ChoiceID {
			t.Fatal("display labels must not affect the choice id")
		}
		if _, err := DecodeConstructionChoiceID(choice.ChoiceID); err != nil {
			t.Fatalf("valid field choice id did not round trip: %v", err)
		}
	})

	t.Run("repeated", func(t *testing.T) {
		candidate := Candidate{
			ID: "candidate_repeated", NodeID: "node_observation", ResourceType: "Observation",
			FieldPath: "category[].coding[].code", Cardinality: "many",
			RepeatedBoundaries: []RepeatedBoundary{{Path: "category[]", MaxItems: 3}},
			ProjectionModes:    []ProjectionMode{ProjectionIndexed, ProjectionFirst, ProjectionArray, ProjectionDistinctArray},
		}
		choice, err := NewFieldConstructionChoice("snapshot-b", candidate)
		if err != nil {
			t.Fatal(err)
		}
		if len(choice.Options) != 3 {
			t.Fatalf("synthetic INDEXED mode must not be selectable: %#v", choice.Options)
		}
		all := constructionOption(t, choice, ConstructionChoiceAll)
		if all.Decision != ConstructionChoiceDefault || all.Preservation != ConstructionChoicePreserving || all.Shape != ConstructionChoiceList || all.RowEffect != ConstructionChoicePreservesRows {
			t.Fatalf("repeated ALL must preserve values by default: %#v", all)
		}
		first := constructionOption(t, choice, ConstructionChoiceFirst)
		if first.Decision != ConstructionChoiceRequiresDecision || first.Preservation != ConstructionChoiceReducing {
			t.Fatalf("repeated FIRST must be an explicit reduction: %#v", first)
		}
		distinct := constructionOption(t, choice, ConstructionChoiceDistinct)
		if distinct.Preservation != ConstructionChoiceReducing || distinct.Decision != ConstructionChoiceRequiresDecision {
			t.Fatalf("DISTINCT must not discard duplicates by default: %#v", distinct)
		}

		firstOnly := candidate
		firstOnly.ProjectionModes = []ProjectionMode{ProjectionFirst}
		choice, err = NewFieldConstructionChoice("snapshot-b", firstOnly)
		if err != nil {
			t.Fatal(err)
		}
		if constructionOption(t, choice, ConstructionChoiceFirst).Decision != ConstructionChoiceRequiresDecision {
			t.Fatal("a repeated field with only FIRST support must require an explicit decision")
		}
	})
}

func TestRelatedRouteOptionsDeclareScalarContributorPredicateSupport(t *testing.T) {
	t.Parallel()

	route := []ConstructionRouteStep{{
		EdgeID: "edge", FromNodeID: "patient", ToNodeID: "observation",
		FromResourceType: "Patient", ToResourceType: "Observation",
		Relationship: "subject_Patient", StorageDirection: "INBOUND", MatchMode: "OPTIONAL",
	}}
	for _, test := range []struct {
		logicalType string
		want        []ConstructionChoicePredicateOperator
	}{
		{logicalType: "string", want: []ConstructionChoicePredicateOperator{ConstructionChoicePredicateExists, ConstructionChoicePredicateEquals}},
		{logicalType: "code", want: []ConstructionChoicePredicateOperator{ConstructionChoicePredicateExists, ConstructionChoicePredicateEquals}},
		{logicalType: "boolean", want: []ConstructionChoicePredicateOperator{ConstructionChoicePredicateExists}},
	} {
		t.Run(test.logicalType, func(t *testing.T) {
			candidate := Candidate{
				ID: "observation-status", NodeID: "observation", ResourceType: "Observation",
				FieldPath: "status", Cardinality: "optional_one", LogicalType: test.logicalType,
				ProjectionModes: []ProjectionMode{ProjectionScalar},
			}
			choice, err := NewFieldConstructionChoiceForRoute("snapshot", route, candidate)
			if err != nil {
				t.Fatal(err)
			}
			for _, form := range []ConstructionChoiceForm{ConstructionChoiceAll, ConstructionChoiceCount, ConstructionChoicePresence} {
				option := constructionOption(t, choice, form)
				if len(option.ContributorPredicateOperators) != len(test.want) {
					t.Fatalf("%s operators = %#v, want %#v", form, option.ContributorPredicateOperators, test.want)
				}
				for index, want := range test.want {
					if option.ContributorPredicateOperators[index] != want {
						t.Fatalf("%s operators = %#v, want %#v", form, option.ContributorPredicateOperators, test.want)
					}
				}
			}
			if constructionOption(t, choice, ConstructionChoiceValue).ContributorPredicateOperators != nil {
				t.Fatal("direct source option must not advertise related contributor predicates")
			}
		})
	}
}

func TestNewSemanticConstructionChoicePinsObservationAndCompilerSource(t *testing.T) {
	t.Parallel()

	candidate := Candidate{
		ID: "candidate_observation_value", NodeID: "node_observation", ResourceType: "Observation",
		FieldPath: "component[].valueQuantity.value", Cardinality: "many",
		RepeatedBoundaries: []RepeatedBoundary{{Path: "component[]", MaxItems: 4}},
		ProjectionModes:    []ProjectionMode{ProjectionFirst, ProjectionArray},
	}
	source := SemanticBindingChoiceSource{
		ConceptID: "concept-a", BindingID: "binding-a", ResourceType: "Observation",
		SourcePath: "component[].code", SourceCanonical: "http://example.org/StructureDefinition/vitals",
		SourceProfile: "http://example.org/StructureDefinition/vitals|1.0.0",
		FieldPath:     "component[].valueQuantity.value", OwningScope: "component[]",
		ExtensionURLPath: []string{"http://example.org/ext", "nested"},
		KeySelector:      "component[].code.coding[]", System: "http://loinc.org", Version: "2.77", Code: "8480-6",
		ValueSelector: "valueQuantity.value", ChoiceArm: "Quantity",
		LogicalType: "decimal", RuleHint: catalog.SemanticRuleHintCodedValueV1, RuleVersion: strconv.Itoa(catalog.SemanticObservationRuleVersion), SchemaVersion: 1,
	}
	choice, err := NewSemanticConstructionChoice("snapshot-a", "context-a", "build-a", source, candidate)
	if err != nil {
		t.Fatal(err)
	}
	resolved, ok := choice.Source.(SemanticBindingChoiceSource)
	if !ok || resolved.Kind != ConstructionChoiceSourceSemantic || resolved.FieldPath != candidate.FieldPath || resolved.CandidateID != candidate.ID || resolved.NodeID != candidate.NodeID {
		t.Fatalf("semantic choice lost its exact compiler or semantic source: %#v", choice.Source)
	}
	if resolved.SourcePath != source.SourcePath || resolved.OwningScope != source.OwningScope || resolved.KeySelector != source.KeySelector || resolved.ValueSelector != candidate.FieldPath || resolved.ChoiceArm != source.ChoiceArm || resolved.System != source.System || resolved.Version != source.Version || resolved.Code != source.Code {
		t.Fatalf("semantic choice lost FHIR owner/member/choice identity: %#v", resolved)
	}
	if len(resolved.ExtensionURLPath) != 2 || resolved.ExtensionURLPath[0] != "http://example.org/ext" {
		t.Fatalf("semantic extension ancestry was not copied: %#v", resolved.ExtensionURLPath)
	}
	source.ExtensionURLPath[0] = "http://example.org/changed"
	if resolved.ExtensionURLPath[0] != "http://example.org/ext" {
		t.Fatal("returned semantic source must not alias caller-owned extension ancestry")
	}
	if constructionOption(t, choice, ConstructionChoiceAll).Decision != ConstructionChoiceDefault || constructionOption(t, choice, ConstructionChoiceFirst).Preservation != ConstructionChoiceReducing {
		t.Fatalf("semantic output options do not preserve repeated values by default: %#v", choice.Options)
	}

	identity, err := DecodeConstructionChoiceID(choice.ChoiceID)
	if err != nil {
		t.Fatalf("valid semantic choice id did not decode: %v", err)
	}
	decoded, ok := identity.Source.(SemanticBindingChoiceSource)
	if !ok || identity.SnapshotToken != "snapshot-a" || identity.SemanticContextToken != "context-a" || identity.BuildID != "build-a" || decoded.ConceptID != source.ConceptID || decoded.BindingID != source.BindingID || decoded.ValueSelector != candidate.FieldPath {
		t.Fatalf("semantic choice id did not round trip its pinned source: %#v", identity)
	}

	wrongValueSelector := source
	wrongValueSelector.ValueSelector = "component[].valueString"
	if _, err := NewSemanticConstructionChoice("snapshot-a", "context-a", "build-a", wrongValueSelector, candidate); err == nil {
		t.Fatal("semantic choice must reject a value selector unrelated to the compiler candidate")
	}
	unrelatedCandidate := candidate
	unrelatedCandidate.FieldPath = "component[].valueString"
	if _, err := NewSemanticConstructionChoice("snapshot-a", "context-a", "build-a", source, unrelatedCandidate); err == nil {
		t.Fatal("semantic choice must reject compiler proof for an unrelated value field")
	}

	changedContext, err := NewSemanticConstructionChoice("snapshot-a", "context-b", "build-a", source, candidate)
	if err != nil {
		t.Fatal(err)
	}
	changedBuild, err := NewSemanticConstructionChoice("snapshot-a", "context-a", "build-b", source, candidate)
	if err != nil {
		t.Fatal(err)
	}
	changedSnapshot, err := NewSemanticConstructionChoice("snapshot-b", "context-a", "build-a", source, candidate)
	if err != nil {
		t.Fatal(err)
	}
	changedCodeSource := source
	changedCodeSource.Code = "8462-4"
	changedSource, err := NewSemanticConstructionChoice("snapshot-a", "context-a", "build-a", changedCodeSource, candidate)
	if err != nil {
		t.Fatal(err)
	}
	for _, got := range []ConstructionChoice{changedContext, changedBuild, changedSnapshot, changedSource} {
		if got.ChoiceID == choice.ChoiceID {
			t.Fatal("snapshot, inventory context/build, and semantic source identity must pin the choice id")
		}
	}
}

func TestSemanticConstructionChoiceAdvertisesOwnerRecordsOnlyAfterExactProof(t *testing.T) {
	candidate := Candidate{
		ID: "component-value", NodeID: "observation", ResourceType: "Observation",
		FieldPath: "component[].valueQuantity.value", Cardinality: "many",
		RepeatedBoundaries: []RepeatedBoundary{{Path: "component[]", MaxItems: 8}},
		ProjectionModes:    []ProjectionMode{ProjectionFirst, ProjectionArray},
	}
	source := SemanticBindingChoiceSource{
		ConceptID: "concept", BindingID: "binding", ResourceType: "Observation",
		SourcePath: "component[].code", FieldPath: candidate.FieldPath, OwningScope: "component[]",
		KeySelector: "component[].code.coding[]", System: "http://loinc.org", Code: "8480-6",
		ValueSelector: "valueQuantity.value", ChoiceArm: "valueQuantity", LogicalType: "decimal",
		RuleHint: catalog.SemanticRuleHintCodedValueV1, RuleVersion: strconv.Itoa(catalog.SemanticObservationRuleVersion), SchemaVersion: 1,
	}

	unproved, err := NewSemanticConstructionChoice("snapshot", "context", "build", source, candidate)
	if err != nil {
		t.Fatal(err)
	}
	for _, option := range unproved.Options {
		if option.Form == ConstructionChoiceOwnerRecords {
			t.Fatal("owner records was advertised without its compiler proof")
		}
	}

	proved, err := NewSemanticConstructionChoice("snapshot", "context", "build", source, candidate, true)
	if err != nil {
		t.Fatal(err)
	}
	option := constructionOption(t, proved, ConstructionChoiceOwnerRecords)
	if option.Shape != ConstructionChoiceList || option.Preservation != ConstructionChoicePreserving ||
		option.Decision != ConstructionChoiceRequiresDecision || option.RowEffect != ConstructionChoicePreservesRows {
		t.Fatalf("owner-record option = %#v", option)
	}

	withoutOwnerBoundary := candidate
	withoutOwnerBoundary.RepeatedBoundaries = []RepeatedBoundary{{Path: "component[].code.coding[]", MaxItems: 8}}
	notProved, err := NewSemanticConstructionChoice("snapshot", "context", "build", source, withoutOwnerBoundary, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, option := range notProved.Options {
		if option.Form == ConstructionChoiceOwnerRecords {
			t.Fatal("owner records was advertised for an unrelated repeated boundary")
		}
	}
}

func TestDecodeConstructionChoiceIDRejectsTamperedAndMalformedTokens(t *testing.T) {
	t.Parallel()

	candidate := Candidate{
		ID: "candidate", NodeID: "node", ResourceType: "Patient", FieldPath: "birthDate",
		Cardinality: "optional_one", ProjectionModes: []ProjectionMode{ProjectionScalar},
	}
	choice, err := NewFieldConstructionChoice("snapshot", candidate)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(choice.ChoiceID, ".")
	if len(parts) != 3 {
		t.Fatalf("unexpected versioned choice id format %q", choice.ChoiceID)
	}
	if len(parts[1]) == 0 {
		t.Fatal("choice id has an empty payload")
	}
	if parts[1][0] == 'A' {
		parts[1] = "B" + parts[1][1:]
	} else {
		parts[1] = "A" + parts[1][1:]
	}
	tampered := strings.Join(parts, ".")

	for _, invalid := range []string{tampered, "cc2.invalid.invalid", "cc1.%%%.deadbeef", strings.Repeat("x", ConstructionChoiceIDMaxLength+1)} {
		if _, err := DecodeConstructionChoiceID(invalid); err == nil {
			t.Errorf("DecodeConstructionChoiceID(%q) accepted an invalid token", invalid)
		}
	}

	unknownKind := []byte(`{"version":"construction-choice/v1","kind":"ROUTE","snapshotToken":"snapshot","source":{}}`)
	if _, err := DecodeConstructionChoiceID(choiceIDForPayload(unknownKind)); err == nil {
		t.Fatal("choice id decoder accepted an unknown source kind")
	}
	unknownField := []byte(`{"version":"construction-choice/v1","kind":"FIELD","snapshotToken":"snapshot","source":{"kind":"FIELD","candidateId":"c","nodeId":"n","resourceType":"Patient","path":"birthDate","cardinality":"optional_one","unexpected":true}}`)
	if _, err := DecodeConstructionChoiceID(choiceIDForPayload(unknownField)); err == nil {
		t.Fatal("choice id decoder accepted an unknown source field")
	}
}

func TestRelatedResourceRouteChoicePinsNodeAndRouteWithoutField(t *testing.T) {
	t.Parallel()

	route := []ConstructionRouteStep{{
		EdgeID: "edge_patient_observation", FromNodeID: "patient_node", ToNodeID: "observation_node",
		FromResourceType: "Patient", ToResourceType: "Observation",
		Relationship: "subject_Patient", StorageDirection: "INBOUND", MatchMode: "OPTIONAL",
	}}
	choice, err := NewConstructionRelatedResourceRouteChoice("snapshot", "stage_1", "observation_node", "Observation", route)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := DecodeConstructionChoiceID(choice.ChoiceID)
	if err != nil {
		t.Fatalf("decode route choice: %v", err)
	}
	source, ok := identity.Source.(RelatedResourceChoiceSource)
	if !ok || source.StageID != "stage_1" || source.NodeID != "observation_node" || source.ResourceType != "Observation" || identity.SnapshotToken != "snapshot" || !reflect.DeepEqual(identity.Route, route) {
		t.Fatalf("route choice lost its pinned identity: %#v", identity)
	}
	if _, err := NewConstructionRelatedResourceRouteChoice("snapshot", "stage_1", "other_node", "Observation", route); err == nil {
		t.Fatal("route choice accepted a target node that differs from its terminal hop")
	}
}

func constructionOption(t *testing.T, choice ConstructionChoice, form ConstructionChoiceForm) ConstructionChoiceOption {
	t.Helper()
	for _, option := range choice.Options {
		if option.Form == form {
			return option
		}
	}
	t.Fatalf("choice %q has no %s option", choice.ChoiceID, form)
	return ConstructionChoiceOption{}
}

func choiceIDForPayload(payload []byte) string {
	digest := sha256.Sum256(payload)
	return "cc1." + base64.RawURLEncoding.EncodeToString(payload) + "." + hex.EncodeToString(digest[:])
}
