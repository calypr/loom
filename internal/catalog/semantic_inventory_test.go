package catalog

import (
	"fmt"
	"reflect"
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

	if len(contributions) != 1000 {
		t.Fatalf("inventory contributions = %d, want all 1000 concepts", len(contributions))
	}
	conceptIDs := make(map[string]struct{}, len(contributions))
	bindingIDs := make(map[string]struct{}, len(contributions))
	keys := make(map[string]struct{}, len(contributions))
	for _, contribution := range contributions {
		conceptIDs[contribution.ConceptID] = struct{}{}
		bindingIDs[contribution.BindingID] = struct{}{}
		if _, duplicate := keys[contribution.Key]; duplicate {
			t.Fatalf("duplicate contribution key %q", contribution.Key)
		}
		keys[contribution.Key] = struct{}{}
		if len(contribution.Observation.Examples) > maxSemanticExamples {
			t.Fatalf("contribution examples = %d, exceeds bound %d", len(contribution.Observation.Examples), maxSemanticExamples)
		}
	}
	if len(conceptIDs) != 1000 {
		t.Fatalf("unique concept IDs = %d, want 1000", len(conceptIDs))
	}
	if len(bindingIDs) != 1 {
		t.Fatalf("unique binding IDs = %d, want one structural binding", len(bindingIDs))
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
	if len(contributions) != 1000 {
		t.Fatalf("retained emitter events = %d, want 1000 concepts", len(contributions))
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
	if len(concepts) != 1000 {
		t.Fatalf("retained unique concept IDs = %d, want 1000", len(concepts))
	}
}

func TestRetainedSemanticEmitterProjectsExactCodedGroupWitness(t *testing.T) {
	witness := []struct {
		id      string
		payload map[string]any
	}{
		{
			id: "26c7500c-f54f-5229-b5a7-a6757614bc21",
			payload: map[string]any{
				"resourceType": "Observation",
				"id":           "26c7500c-f54f-5229-b5a7-a6757614bc21",
				"code": map[string]any{"coding": []any{map[string]any{
					"system":  "http://snomed.info/sct",
					"code":    "1222593009",
					"display": "American Joint Committee on Cancer pathological stage group allowable value",
				}}},
				"valueCodeableConcept": map[string]any{"coding": []any{map[string]any{
					"system":  "http://snomed.info/sct",
					"code":    "1222593009",
					"display": "Stage I",
				}}},
			},
		},
		{
			id: "64704041-c4da-5375-aac0-88f951f78344",
			payload: map[string]any{
				"resourceType": "Observation",
				"id":           "64704041-c4da-5375-aac0-88f951f78344",
				"code": map[string]any{"coding": []any{map[string]any{
					"system":  "http://snomed.info/sct",
					"code":    "1222593009",
					"display": "American Joint Committee on Cancer pathological stage group allowable value",
				}}},
				"valueCodeableConcept": map[string]any{"coding": []any{map[string]any{
					"system":  "http://snomed.info/sct",
					"code":    "1222593009",
					"display": "Stage I",
				}}},
			},
		},
		{
			id: "7d2fe423-f934-56ff-bd96-426fb1fdf94e",
			payload: map[string]any{
				"resourceType": "Observation",
				"id":           "7d2fe423-f934-56ff-bd96-426fb1fdf94e",
				"code": map[string]any{"coding": []any{map[string]any{
					"system":  "http://loinc.org",
					"code":    "81247-9",
					"display": "Master HL7 genetic variant reporting panel",
				}}},
				"valueCodeableConcept": nil,
			},
		},
	}

	emitter := NewSemanticInventoryEmitter("loom_dev_cda_fhir", "cda-fhir-v1")
	contributionsByID := make(map[string][]SemanticInventoryContribution, len(witness))
	for _, record := range witness {
		record := record
		emitter.ObservePayload(record.payload, "Observation", "", "retained:Observation/"+record.id, func(contribution SemanticInventoryContribution) {
			contributionsByID[record.id] = append(contributionsByID[record.id], contribution)
		})
	}
	for _, record := range witness[:2] {
		contributions := contributionsByID[record.id]
		if len(contributions) != 1 {
			t.Fatalf("%s retained contributions = %d, want one coded value observation", record.id, len(contributions))
		}
		contribution := contributions[0]
		observation := contribution.Observation
		if contribution.SourceKind != SemanticInventorySourceRetained ||
			contribution.ResourceType != "Observation" || contribution.SourceID != "retained:Observation/"+record.id ||
			observation.Source.Path != "code" || observation.Key.System != "http://snomed.info/sct" || observation.Key.Code != "1222593009" ||
			observation.Value.Selector != "valueCodeableConcept.text" ||
			!reflect.DeepEqual(observation.Value.FallbackSelectors, []string{"valueCodeableConcept.coding[].display"}) ||
			!observation.Value.FallbackOnBlankString || observation.Status != "SUPPORTED" || observation.RuleHint != SemanticRuleHintCodedValueV1 {
			t.Fatalf("%s retained semantic contribution = %#v", record.id, contribution)
		}
	}
	standalone := contributionsByID[witness[2].id]
	if len(standalone) != 1 {
		t.Fatalf("unpaired third observation contributions = %d, want one standalone coded concept", len(standalone))
	}
	observation := standalone[0].Observation
	if observation.RuleHint != SemanticRuleHintCodedConceptV1 || observation.Source.Path != "code.coding[]" ||
		observation.OwningScope != "" || observation.Key.Selector != "code.coding[]" || observation.Key.System != "http://loinc.org" || observation.Key.Code != "81247-9" ||
		observation.Value.Selector != "code.coding[].display" || !reflect.DeepEqual(observation.Value.FallbackSelectors, []string{"code.coding[].code"}) {
		t.Fatalf("unpaired third observation standalone contribution = %#v", standalone[0])
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

func TestSemanticBindingIdentityIncludesValueFallbackPolicy(t *testing.T) {
	base := observeCodeableConceptValue(t, map[string]any{
		"text":   "Stage I",
		"coding": []any{map[string]any{"system": "urn:value", "code": "stage", "display": "Stage I"}},
	})
	withoutFallback := base
	withoutFallback.Value.FallbackSelectors = nil
	withoutFallback.Value.FallbackOnBlankString = false
	if got, want := semanticBindingID(withoutFallback), semanticBindingID(base); got == want {
		t.Fatalf("fallback policy did not change binding identity: %q", got)
	}
	changedFallback := base
	changedFallback.Value.FallbackSelectors = []string{"valueCodeableConcept.coding[].code"}
	if got, want := semanticBindingID(changedFallback), semanticBindingID(base); got == want {
		t.Fatalf("fallback selector did not change binding identity: %q", got)
	}
}

func TestLegacySemanticBindingIDV1MatchesPinnedPrimaryOnlyDigest(t *testing.T) {
	current := SemanticObservation{
		SchemaVersion: SemanticObservationSchemaVersion,
		Source: SemanticObservationSource{
			Type: "Observation", Profile: "http://example.org/Profile", Path: "valueCodeableConcept",
			Canonical: "Observation.valueCodeableConcept",
		},
		Key:         SemanticObservationKey{Selector: "code.coding[]", System: "urn:study", Code: "stage"},
		OwningScope: "", ChoiceArm: "valueCodeableConcept", LogicalType: "string",
		RuleHint: SemanticRuleHintCodedValueV1, RuleVersion: "5",
		ExtensionURLPath: []string{"urn:outer", "urn:inner"},
		Value: SemanticObservationValue{
			Selector: "valueCodeableConcept.text", Type: "string",
			FallbackSelectors: []string{"valueCodeableConcept.coding[].display"}, FallbackOnBlankString: true,
		},
	}
	const want = "f00fb0de08f991fba1daef08c147104f1ffcd9f0e7ac2c9a85f3aba9376f56eb"
	if got := LegacySemanticBindingIDV1(current); got != want {
		t.Fatalf("legacy v1 binding ID = %q, want pinned pre-change digest %q", got, want)
	}

	legacy := current
	legacy.SchemaVersion = SemanticObservationLegacyV1SchemaVersion
	legacy.RuleVersion = "4"
	legacy.Value.FallbackSelectors = nil
	legacy.Value.FallbackOnBlankString = false
	if got := LegacySemanticBindingIDV1(legacy); got != want {
		t.Fatalf("primary-only v1 frame identity = %q, want %q", got, want)
	}
	changedExtensionOrder := current
	changedExtensionOrder.ExtensionURLPath = []string{"urn:inner", "urn:outer"}
	if got := LegacySemanticBindingIDV1(changedExtensionOrder); got == want {
		t.Fatalf("legacy binding ID ignored ExtensionURLPath order: %q", got)
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

func TestSemanticInventoryCursorBindsAuthorizedScopeAndClampsPageLimit(t *testing.T) {
	options := SemanticInventoryPageOptions{
		Project:                       "project",
		DatasetGeneration:             "generation",
		AuthResourcePaths:             []string{"scope-b", "scope-a", "scope-a"},
		AuthResourcePathsUnrestricted: ExplicitAuthResourcePathsUnrestricted(false),
		Limit:                         1000,
	}
	cursor := EncodeSemanticInventoryCursor(options, "build", "binding", "concept")
	options.AuthResourcePaths = []string{"scope-a", "scope-b"}
	options.Limit = SemanticInventoryPageLimit
	if _, _, err := DecodeSemanticInventoryCursor(cursor, options, "build"); err != nil {
		t.Fatalf("equivalent scope and clamped limit rejected cursor: %v", err)
	}
	options.AuthResourcePaths = []string{"scope-a"}
	if _, _, err := DecodeSemanticInventoryCursor(cursor, options, "build"); err != ErrSemanticInventoryCursorMismatch {
		t.Fatalf("changed authorization scope error = %v, want cursor mismatch", err)
	}
}

func TestSemanticInventoryPageDigestBindsFrameBindingFilter(t *testing.T) {
	options := SemanticInventoryPageOptions{Project: "project", DatasetGeneration: "generation", ResourceType: "Observation"}
	unfiltered := SemanticInventoryPageDigest(options, "build", SemanticInventoryPageLimit)
	options.BindingID = "frame-binding"
	filtered := SemanticInventoryPageDigest(options, "build", SemanticInventoryPageLimit)
	if filtered == unfiltered {
		t.Fatal("frame-scoped binding filter did not change semantic inventory cursor identity")
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
		observation = contribution.Observation
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
