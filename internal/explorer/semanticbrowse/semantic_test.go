package semanticbrowse

import (
	"reflect"
	"slices"
	"strconv"
	"testing"

	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/semanticauthoring"
	"github.com/calypr/loom/internal/semantic"
)

func TestSemanticCandidateWithFallbackPreservesRepeatedCardinalityAndAuthorizationIdentity(t *testing.T) {
	observation := semantic.Observation{
		SchemaVersion: semantic.ObservationSchemaVersion,
		Source:        semantic.Source{Type: "Observation", Canonical: "Observation.valueCodeableConcept", Path: "valueCodeableConcept"},
		Key:           semantic.Key{Selector: "code.coding[]", System: "urn:study", Code: "stage"},
		OwningScope:   "", ChoiceArm: "valueCodeableConcept", LogicalType: "string",
		Completeness: semantic.CompletenessComplete, Status: "SUPPORTED", RuleHint: semantic.RuleHintCodedValueV1,
		RuleVersion: strconv.Itoa(semantic.SemanticObservationRuleVersion),
		Value: semantic.Value{
			Selector: "valueCodeableConcept.text", Type: "string",
			FallbackSelectors: []string{"valueCodeableConcept.coding[].display"}, FallbackOnBlankString: true,
		},
	}
	primary := capability.Candidate{
		ID: "observation-value-text", NodeID: "observation", ResourceType: "Observation",
		FieldPath: "valueCodeableConcept.text", LogicalType: "string", Cardinality: "optional_one",
		ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar},
	}
	fallback := capability.Candidate{
		ID: "observation-coding-display", NodeID: "observation", ResourceType: "Observation",
		FieldPath: "valueCodeableConcept.coding[].display", LogicalType: "string", Cardinality: "many",
		RepeatedBoundaries: []capability.RepeatedBoundary{{Path: "valueCodeableConcept.coding[]", MaxItems: 3}},
		ProjectionModes:    []capability.ProjectionMode{capability.ProjectionArray, capability.ProjectionFirst},
	}
	snapshot := capability.Snapshot{Candidates: []capability.Candidate{primary, fallback}}
	combined, ok := semanticCandidateWithFallback(snapshot, observation, primary)
	if !ok || combined.Cardinality != "many" || !capability.IsRepeatedCardinality(combined.Cardinality) ||
		!reflect.DeepEqual(combined.RepeatedBoundaries, fallback.RepeatedBoundaries) ||
		!slices.Contains(combined.ProjectionModes, capability.ProjectionScalar) || !slices.Contains(combined.ProjectionModes, capability.ProjectionArray) {
		t.Fatalf("combined primary/fallback candidate = %#v, ok=%v", combined, ok)
	}
	fallback.Cardinality = "unknown_observed_many"
	snapshot.Candidates[1] = fallback
	combined, ok = semanticCandidateWithFallback(snapshot, observation, primary)
	if !ok || combined.Cardinality != "unknown_observed_many" {
		t.Fatalf("unknown fallback cardinality was downgraded: %#v, ok=%v", combined, ok)
	}

	family := capability.SemanticFrameFamily{
		BindingID: "binding-codeable", ResourceType: "Observation", SourcePath: "valueCodeableConcept",
		SourceCanonical: "Observation.valueCodeableConcept",
		KeyPath:         "code.coding[]", ValuePath: "valueCodeableConcept.text",
		ValueFallbacks: []string{"valueCodeableConcept.coding[].display"}, FallbackOnBlankString: true,
		ChoiceArms: []string{"valueCodeableConcept"}, LogicalType: "string", RuleVersion: "5", SchemaVersion: 4,
	}
	entry := semantic.Entry{ConceptID: "stage", BindingID: family.BindingID, Observation: observation}
	plan := semanticauthoring.ResolveSemanticSelectionPlan(observation)
	if !plan.Readiness.Addable() || !semanticEntryMatchesFrameFamily(entry, family) {
		t.Fatalf("fallback frame family did not reauthorize against current binding: plan=%#v family=%#v", plan, family)
	}
	changedFamily := family
	changedFamily.ValueFallbacks = []string{"valueCodeableConcept.coding[].code"}
	if semanticEntryMatchesFrameFamily(entry, changedFamily) {
		t.Fatal("reauthorization accepted changed fallback selectors")
	}
	changedFamily = family
	changedFamily.FallbackOnBlankString = false
	if semanticEntryMatchesFrameFamily(entry, changedFamily) {
		t.Fatal("reauthorization accepted changed blank-string fallback policy")
	}
	choiceFor := func(family capability.SemanticFrameFamily) capability.ConstructionChoice {
		t.Helper()
		choice, err := capability.NewSemanticFrameConstructionChoice("snapshot", "context", "build", capability.SemanticFrameChoiceSource{
			Kind: capability.ConstructionChoiceSourceSemanticFrame, AnchorConceptID: "stage-concept", Family: family,
			CandidateID: primary.ID, NodeID: primary.NodeID, FieldPath: primary.FieldPath,
		}, nil, combined)
		if err != nil {
			t.Fatal(err)
		}
		return choice
	}
	choice := choiceFor(family)
	if choice.ChoiceID == "" || !choiceHasOption(choice.Options, capability.ConstructionChoiceValue, capability.ConstructionChoiceRequiresDecision) ||
		!choiceHasOption(choice.Options, capability.ConstructionChoiceAll, capability.ConstructionChoiceDefault) ||
		!presentationHasFact(choice.Presentation.Facts, "Value cardinality", "Repeated values") ||
		!presentationHasFact(choice.Presentation.Facts, "Fallback rule", "Use the fallback when the primary string is blank") {
		t.Fatalf("repeated fallback choice did not require an explicit scalar decision: %#v", choice)
	}
	changed := family
	changed.ValueFallbacks = []string{"valueCodeableConcept.coding[].code"}
	if got := choiceFor(changed).ChoiceID; got == choice.ChoiceID {
		t.Fatal("fallback selector change did not change the reauthorized choice identity")
	}
	changed = family
	changed.FallbackOnBlankString = false
	if got := choiceFor(changed).ChoiceID; got == choice.ChoiceID {
		t.Fatal("blank-string policy change did not change the reauthorized choice identity")
	}
}

func choiceHasOption(options []capability.ConstructionChoiceOption, form capability.ConstructionChoiceForm, decision capability.ConstructionChoiceDecision) bool {
	for _, option := range options {
		if option.Form == form && option.Decision == decision && option.Support == capability.ConstructionChoiceSupported {
			return true
		}
	}
	return false
}

func presentationHasFact(facts []capability.ConstructionChoiceFact, label, value string) bool {
	for _, fact := range facts {
		if fact.Label == label && fact.Value == value {
			return true
		}
	}
	return false
}
