package catalog

import (
	"fmt"
	"testing"
)

func TestSemanticInventoryEmitsBeyondBoundedFieldSummary(t *testing.T) {
	components := make([]any, 1000)
	for i := range components {
		components[i] = inventoryTestComponent(fmt.Sprintf("code-%04d", i), "v1", "label")
	}
	payload := map[string]any{"resourceType": "Observation", "component": components}
	profiler := NewProfilerForGeneration("project", "generation", "scope-a", "Observation", nil)
	contributions := make([]SemanticInventoryContribution, 0, len(components))
	profiler.ObservePayloadWithInventory(payload, map[string]float64{}, "Observations.ndjson#1", func(contribution SemanticInventoryContribution) {
		contributions = append(contributions, contribution)
	})

	if len(contributions) != 1001 {
		t.Fatalf("inventory contributions = %d, want 1000 coded values and one type metadata slot", len(contributions))
	}
	conceptIDs := make(map[string]struct{}, len(contributions))
	bindingIDs := make(map[string]struct{}, len(contributions))
	keys := make(map[string]struct{}, len(contributions))
	categoryCount, codedValueCount := 0, 0
	for _, contribution := range contributions {
		conceptIDs[contribution.ConceptID] = struct{}{}
		bindingIDs[contribution.BindingID] = struct{}{}
		switch contribution.Observation.RuleHint {
		case SemanticRuleHintCategoricalCodeV1:
			categoryCount++
		case SemanticRuleHintCodedValueV1:
			codedValueCount++
		}
		if _, duplicate := keys[contribution.Key]; duplicate {
			t.Fatalf("duplicate contribution key %q", contribution.Key)
		}
		keys[contribution.Key] = struct{}{}
		if len(contribution.Observation.Examples) > maxSemanticExamples {
			t.Fatalf("contribution examples = %d, exceeds bound %d", len(contribution.Observation.Examples), maxSemanticExamples)
		}
	}
	if len(conceptIDs) != 1001 {
		t.Fatalf("unique concept IDs = %d, want 1001", len(conceptIDs))
	}
	if categoryCount != 1 || codedValueCount != 1000 {
		t.Fatalf("standalone category/coded-value contributions = %d/%d, want 1/1000", categoryCount, codedValueCount)
	}
	if len(bindingIDs) != 2 {
		t.Fatalf("unique binding IDs = %d, want value and type metadata bindings", len(bindingIDs))
	}

	var legacyObservations int
	for _, document := range profiler.Documents() {
		if document.Path == "component[]" {
			legacyObservations += len(document.SemanticObservations)
			for _, observation := range document.SemanticObservations {
				if len(observation.Examples) > maxSemanticExamples {
					t.Fatalf("legacy examples = %d, exceeds bound %d", len(observation.Examples), maxSemanticExamples)
				}
			}
		}
	}
	if legacyObservations != maxSemanticObservations {
		t.Fatalf("bounded field summary retained %d observations, want its existing cap %d", legacyObservations, maxSemanticObservations)
	}
}

func TestRetainedSemanticEmitterEmitsBeyondSummaryCapWithoutRetainingScopeMaps(t *testing.T) {
	components := make([]any, 1000)
	for i := range components {
		components[i] = inventoryTestComponent(fmt.Sprintf("retained-%04d", i), "v1", "same label")
	}
	emitter := NewSemanticInventoryEmitter("project", "generation")
	contributions := make([]SemanticInventoryContribution, 0, len(components))
	emitter.ObservePayload(map[string]any{"resourceType": "Observation", "component": components}, "Observation", "scope-a", "retained:Observation/key-1", func(contribution SemanticInventoryContribution) {
		contributions = append(contributions, contribution)
	})
	if len(contributions) != 1001 {
		t.Fatalf("retained emitter events = %d, want 1000 coded values and one type metadata slot", len(contributions))
	}
	concepts := map[string]struct{}{}
	for _, contribution := range contributions {
		concepts[contribution.ConceptID] = struct{}{}
		if contribution.SourceKind != SemanticInventorySourceRetained || contribution.AuthResourcePath != "scope-a" || contribution.SourceID != "retained:Observation/key-1" {
			t.Fatalf("retained contribution identity = %+v", contribution)
		}
		if len(contribution.Observation.Examples) > maxSemanticExamples {
			t.Fatalf("retained contribution examples = %d, exceeds %d", len(contribution.Observation.Examples), maxSemanticExamples)
		}
	}
	if len(concepts) != 1001 {
		t.Fatalf("retained unique concept IDs = %d, want 1001", len(concepts))
	}
}

func TestSemanticInventoryClassificationSeparatesRecognitionFromCompilerReadiness(t *testing.T) {
	base := inventoryTestObservation("", "label")
	base.SchemaVersion = SemanticObservationSchemaVersion
	base.RuleVersion = fmt.Sprint(SemanticObservationRuleVersion)
	base.Completeness = SemanticComplete
	base.Status = "SUPPORTED"
	base.RuleHint = SemanticRuleHintCodedValueV1

	tests := []struct {
		name        string
		observation SemanticObservation
		section     SemanticInventoryCatalogSection
		code        string
	}{
		{name: "recognized", observation: base, section: SemanticInventorySectionConcepts, code: SemanticInventoryRecognitionRecognized},
		{name: "recognized despite compiler detail", observation: func() SemanticObservation { value := base; value.Value.Selector = "not-a-schema-path"; return value }(), section: SemanticInventorySectionConcepts, code: SemanticInventoryRecognitionRecognized},
		{name: "stale version", observation: func() SemanticObservation { value := base; value.RuleVersion = "1"; return value }(), section: SemanticInventorySectionNeedsReview, code: "SEMANTIC_OBSERVATION_VERSION_UNSUPPORTED"},
		{name: "incomplete", observation: func() SemanticObservation { value := base; value.Completeness = SemanticIncomplete; return value }(), section: SemanticInventorySectionNeedsReview, code: "SEMANTIC_OBSERVATION_INCOMPLETE"},
		{name: "unresolved", observation: func() SemanticObservation { value := base; value.Status = "UNRESOLVED_SYSTEM"; return value }(), section: SemanticInventorySectionNeedsReview, code: "SEMANTIC_OBSERVATION_UNRESOLVED_SYSTEM"},
		{name: "unknown rule", observation: func() SemanticObservation {
			value := base
			value.RuleHint = "FUTURE_RULE"
			value.Role = SemanticRoleUnknown
			return value
		}(), section: SemanticInventorySectionNeedsReview, code: "SEMANTIC_RULE_UNSUPPORTED"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			classification := SemanticInventoryClassificationForObservation(test.observation)
			if classification.Section != test.section || classification.Code != test.code {
				t.Fatalf("classification = %#v, want section=%q code=%q", classification, test.section, test.code)
			}
		})
	}
}

func TestSemanticInventoryIdentitySeparatesCodingVersionButNotDisplay(t *testing.T) {
	base := inventoryTestObservation("v1", "original label")
	renamed := inventoryTestObservation("v1", "updated label")
	newVersion := inventoryTestObservation("v2", "original label")

	baseConcept := semanticConceptID(base)
	if got := semanticConceptID(renamed); got != baseConcept {
		t.Fatalf("display-only concept ID = %q, want unchanged %q", got, baseConcept)
	}
	if got := semanticBindingID(renamed); got != semanticBindingID(base) {
		t.Fatalf("display-only binding ID = %q, want unchanged %q", got, semanticBindingID(base))
	}
	if got := semanticConceptID(newVersion); got == baseConcept {
		t.Fatalf("explicit Coding.version did not distinguish concepts: %q", got)
	}
}

func TestSemanticConceptSlotMatchesPairedAndStandaloneFormsAtSameLocation(t *testing.T) {
	paired := inventoryTestObservation("v1", "label")
	paired.Source.Path = "component[]"
	paired.OwningScope = "component[]"
	paired.Key.Selector = "code.coding[]"
	paired.RuleHint = SemanticRuleHintCodedValueV1

	standalone := paired
	standalone.Source.Path = "component[].code"
	standalone.OwningScope = "component[].code"
	standalone.Key.Selector = "component[].code.coding[]"
	standalone.Role = SemanticRoleCategoricalSlot
	standalone.RuleHint = SemanticRuleHintCategoricalCodeV1

	if got, want := semanticConceptSlotID(standalone), semanticConceptSlotID(paired); got != want {
		t.Fatalf("standalone concept slot = %q, want paired slot %q", got, want)
	}
	standalone.Key.Selector = "category.coding[]"
	if got, pairedSlot := semanticConceptSlotID(standalone), semanticConceptSlotID(paired); got == pairedSlot {
		t.Fatalf("different code location reused paired concept slot %q", got)
	}
}

func TestSemanticInventoryReplayIdentityIgnoresMapConstructionOrder(t *testing.T) {
	first := inventoryTestPayload(false)
	second := inventoryTestPayload(true)
	firstProfiler := NewProfilerForGeneration("project", "generation", "scope-a", "Observation", nil)
	secondProfiler := NewProfilerForGeneration("project", "generation", "scope-a", "Observation", nil)
	firstKeys := inventoryContributionIdentity(t, firstProfiler, first)
	secondKeys := inventoryContributionIdentity(t, secondProfiler, second)
	if len(firstKeys) != len(secondKeys) {
		t.Fatalf("replay contribution counts differ: %d vs %d", len(firstKeys), len(secondKeys))
	}
	for key, identity := range firstKeys {
		if got := secondKeys[key]; got != identity {
			t.Fatalf("replay identity for %q = %q, want %q", key, got, identity)
		}
	}
}

func TestSemanticInventoryCursorBindsAuthorizedScopeAndAllowsPageSizeChange(t *testing.T) {
	options := SemanticInventoryPageOptions{
		Project:                       "project",
		DatasetGeneration:             "generation",
		AuthResourcePaths:             []string{"scope-b", "scope-a", "scope-a"},
		AuthResourcePathsUnrestricted: ExplicitAuthResourcePathsUnrestricted(false),
		Limit:                         1000,
	}
	cursor := EncodeSemanticInventoryCursor(options, "build", "binding", "concept")
	options.AuthResourcePaths = []string{"scope-a", "scope-b"}
	options.Limit = 1
	if _, _, err := DecodeSemanticInventoryCursor(cursor, options, "build"); err != nil {
		t.Fatalf("equivalent scope with a smaller next page rejected cursor: %v", err)
	}
	options.AuthResourcePaths = []string{"scope-a"}
	if _, _, err := DecodeSemanticInventoryCursor(cursor, options, "build"); err != ErrSemanticInventoryCursorMismatch {
		t.Fatalf("changed authorization scope error = %v, want cursor mismatch", err)
	}
}

func TestSemanticInventoryCursorBindsCatalogSection(t *testing.T) {
	options := SemanticInventoryPageOptions{
		Project:           "project",
		DatasetGeneration: "generation",
		CatalogSection:    SemanticInventorySectionConcepts,
	}
	cursor := EncodeSemanticInventoryCursor(options, "build", "binding", "concept")
	options.CatalogSection = SemanticInventorySectionNeedsReview
	if _, _, err := DecodeSemanticInventoryCursor(cursor, options, "build"); err != ErrSemanticInventoryCursorMismatch {
		t.Fatalf("changed catalog section error = %v, want cursor mismatch", err)
	}
}

func inventoryTestComponent(code, version, display string) map[string]any {
	return map[string]any{
		"code": map[string]any{"coding": []any{map[string]any{
			"system":  "urn:inventory",
			"version": version,
			"code":    code,
			"display": display,
		}}},
		"valueQuantity": map[string]any{"value": 1.0, "unit": "mg"},
	}
}

func inventoryTestObservation(version, display string) SemanticObservation {
	profiler := NewProfilerForGeneration("project", "generation", "scope-a", "Observation", nil)
	var observation SemanticObservation
	profiler.ObservePayloadWithInventory(map[string]any{
		"resourceType": "Observation",
		"component":    []any{inventoryTestComponent("glucose", version, display)},
	}, map[string]float64{}, "Observation.ndjson#1", func(contribution SemanticInventoryContribution) {
		if contribution.Observation.Role == SemanticRoleCodedValue {
			observation = contribution.Observation
		}
	})
	return observation
}

func inventoryTestPayload(reverse bool) map[string]any {
	component := map[string]any{}
	keys := []string{"code", "valueQuantity"}
	if reverse {
		keys[0], keys[1] = keys[1], keys[0]
	}
	for _, key := range keys {
		switch key {
		case "code":
			component[key] = map[string]any{"coding": []any{map[string]any{"system": "urn:inventory", "code": "glucose", "version": "v1", "display": "label"}}}
		case "valueQuantity":
			component[key] = map[string]any{"value": 1.0, "unit": "mg"}
		}
	}
	root := map[string]any{}
	rootKeys := []string{"resourceType", "component"}
	if reverse {
		rootKeys[0], rootKeys[1] = rootKeys[1], rootKeys[0]
	}
	for _, key := range rootKeys {
		if key == "resourceType" {
			root[key] = "Observation"
		} else {
			root[key] = []any{component}
		}
	}
	return root
}

func inventoryContributionIdentity(t *testing.T, profiler *Profiler, payload map[string]any) map[string]string {
	t.Helper()
	identities := make(map[string]string)
	profiler.ObservePayloadWithInventory(payload, map[string]float64{}, "Observation.ndjson#1", func(contribution SemanticInventoryContribution) {
		identities[contribution.Key] = contribution.ConceptID + ":" + contribution.BindingID + ":" + fmt.Sprint(contribution.Ordinal)
	})
	return identities
}
