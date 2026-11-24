package fieldprofile

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/calypr/loom/internal/semantic"
	"github.com/calypr/loom/internal/testkit/semanticfixtures"
)

func TestSemanticInventoryEmitsBeyondBoundedFieldSummary(t *testing.T) {
	components := make([]any, 1000)
	for i := range components {
		components[i] = semanticfixtures.InventoryComponent(fmt.Sprintf("code-%04d", i), "v1", "label")
	}
	payload := map[string]any{"resourceType": "Observation", "component": components}
	profiler := NewProfilerForGeneration("project", "generation", "scope-a", "Observation", nil)
	contributions := make([]semantic.Contribution, 0, len(components))
	profiler.ObservePayloadWithInventory(payload, map[string]float64{}, "Observations.ndjson#1", func(contribution semantic.Contribution) {
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
		components[i] = semanticfixtures.InventoryComponent(fmt.Sprintf("retained-%04d", i), "v1", "same label")
	}
	emitter := semantic.NewEmitter("project", "generation")
	contributions := make([]semantic.Contribution, 0, len(components))
	emitter.ObservePayload(map[string]any{"resourceType": "Observation", "component": components}, "Observation", "scope-a", "retained:Observation/key-1", func(contribution semantic.Contribution) {
		contributions = append(contributions, contribution)
	})
	if len(contributions) != 1000 {
		t.Fatalf("retained emitter events = %d, want 1000 concepts", len(contributions))
	}
	concepts := map[string]struct{}{}
	for _, contribution := range contributions {
		concepts[contribution.ConceptID] = struct{}{}
		if contribution.SourceKind != semantic.SourceRetained || contribution.AuthResourcePath != "scope-a" || contribution.SourceID != "retained:Observation/key-1" {
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

	emitter := semantic.NewEmitter("loom_dev_cda_fhir", "cda-fhir-v1")
	contributionsByID := make(map[string][]semantic.Contribution, len(witness))
	for _, record := range witness {
		record := record
		emitter.ObservePayload(record.payload, "Observation", "", "retained:Observation/"+record.id, func(contribution semantic.Contribution) {
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
		if contribution.SourceKind != semantic.SourceRetained ||
			contribution.ResourceType != "Observation" || contribution.SourceID != "retained:Observation/"+record.id ||
			observation.Source.Path != "code" || observation.Key.System != "http://snomed.info/sct" || observation.Key.Code != "1222593009" ||
			observation.Value.Selector != "valueCodeableConcept.text" ||
			!reflect.DeepEqual(observation.Value.FallbackSelectors, []string{"valueCodeableConcept.coding[].display"}) ||
			!observation.Value.FallbackOnBlankString || observation.Status != "SUPPORTED" || observation.RuleHint != semantic.RuleHintCodedValueV1 {
			t.Fatalf("%s retained semantic contribution = %#v", record.id, contribution)
		}
	}
	standalone := contributionsByID[witness[2].id]
	if len(standalone) != 1 {
		t.Fatalf("unpaired third observation contributions = %d, want one standalone coded concept", len(standalone))
	}
	observation := standalone[0].Observation
	if observation.RuleHint != semantic.RuleHintCodedConceptV1 || observation.Source.Path != "code.coding[]" ||
		observation.OwningScope != "" || observation.Key.Selector != "code.coding[]" || observation.Key.System != "http://loinc.org" || observation.Key.Code != "81247-9" ||
		observation.Value.Selector != "code.coding[].display" || !reflect.DeepEqual(observation.Value.FallbackSelectors, []string{"code.coding[].code"}) {
		t.Fatalf("unpaired third observation standalone contribution = %#v", standalone[0])
	}
}

func TestSemanticInventoryIdentitySeparatesCodingVersionButNotDisplay(t *testing.T) {
	base := inventoryTestObservation("v1", "original label")
	renamed := inventoryTestObservation("v1", "updated label")
	newVersion := inventoryTestObservation("v2", "original label")

	baseConcept := semantic.ConceptID(base)
	if got := semantic.ConceptID(renamed); got != baseConcept {
		t.Fatalf("display-only concept ID = %q, want unchanged %q", got, baseConcept)
	}
	if got := semantic.BindingID(renamed); got != semantic.BindingID(base) {
		t.Fatalf("display-only binding ID = %q, want unchanged %q", got, semantic.BindingID(base))
	}
	if got := semantic.ConceptID(newVersion); got == baseConcept {
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
	if got, want := semantic.BindingID(withoutFallback), semantic.BindingID(base); got == want {
		t.Fatalf("fallback policy did not change binding identity: %q", got)
	}
	changedFallback := base
	changedFallback.Value.FallbackSelectors = []string{"valueCodeableConcept.coding[].code"}
	if got, want := semantic.BindingID(changedFallback), semantic.BindingID(base); got == want {
		t.Fatalf("fallback selector did not change binding identity: %q", got)
	}
}

func TestLegacySemanticBindingIDV1MatchesPinnedPrimaryOnlyDigest(t *testing.T) {
	current := semantic.Observation{
		SchemaVersion: semantic.ObservationSchemaVersion,
		Source: semantic.Source{
			Type: "Observation", Profile: "http://example.org/Profile", Path: "valueCodeableConcept",
			Canonical: "Observation.valueCodeableConcept",
		},
		Key:         semantic.Key{Selector: "code.coding[]", System: "urn:study", Code: "stage"},
		OwningScope: "", ChoiceArm: "valueCodeableConcept", LogicalType: "string",
		RuleHint: semantic.RuleHintCodedValueV1, RuleVersion: "5",
		ExtensionURLPath: []string{"urn:outer", "urn:inner"},
		Value: semantic.Value{
			Selector: "valueCodeableConcept.text", Type: "string",
			FallbackSelectors: []string{"valueCodeableConcept.coding[].display"}, FallbackOnBlankString: true,
		},
	}
	const want = "f00fb0de08f991fba1daef08c147104f1ffcd9f0e7ac2c9a85f3aba9376f56eb"
	if got := semantic.LegacySemanticBindingIDV1(current); got != want {
		t.Fatalf("legacy v1 binding ID = %q, want pinned pre-change digest %q", got, want)
	}

	legacy := current
	legacy.SchemaVersion = semantic.SemanticObservationLegacyV1SchemaVersion
	legacy.RuleVersion = "4"
	legacy.Value.FallbackSelectors = nil
	legacy.Value.FallbackOnBlankString = false
	if got := semantic.LegacySemanticBindingIDV1(legacy); got != want {
		t.Fatalf("primary-only v1 frame identity = %q, want %q", got, want)
	}
	changedExtensionOrder := current
	changedExtensionOrder.ExtensionURLPath = []string{"urn:inner", "urn:outer"}
	if got := semantic.LegacySemanticBindingIDV1(changedExtensionOrder); got == want {
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
	options := semantic.PageOptions{
		Project:                       "project",
		DatasetGeneration:             "generation",
		AuthResourcePaths:             []string{"scope-b", "scope-a", "scope-a"},
		AuthResourcePathsUnrestricted: new(false),
		Limit:                         1000,
	}
	cursor := semantic.EncodeCursor(options, "build", "binding", "concept")
	options.AuthResourcePaths = []string{"scope-a", "scope-b"}
	options.Limit = semantic.PageLimit
	if _, _, err := semantic.DecodeCursor(cursor, options, "build"); err != nil {
		t.Fatalf("equivalent scope and clamped limit rejected cursor: %v", err)
	}
	options.AuthResourcePaths = []string{"scope-a"}
	if _, _, err := semantic.DecodeCursor(cursor, options, "build"); err != semantic.ErrSemanticInventoryCursorMismatch {
		t.Fatalf("changed authorization scope error = %v, want cursor mismatch", err)
	}
}

func TestSemanticInventoryPageDigestBindsFrameBindingFilter(t *testing.T) {
	options := semantic.PageOptions{Project: "project", DatasetGeneration: "generation", ResourceType: "Observation"}
	unfiltered := semantic.PageDigest(options, "build", semantic.PageLimit)
	options.BindingID = "frame-binding"
	filtered := semantic.PageDigest(options, "build", semantic.PageLimit)
	if filtered == unfiltered {
		t.Fatal("frame-scoped binding filter did not change semantic inventory cursor identity")
	}
}

func inventoryTestObservation(version, display string) semantic.Observation {
	profiler := NewProfilerForGeneration("project", "generation", "scope-a", "Observation", nil)
	var observation semantic.Observation
	profiler.ObservePayloadWithInventory(map[string]any{
		"resourceType": "Observation",
		"component":    []any{semanticfixtures.InventoryComponent("glucose", version, display)},
	}, map[string]float64{}, "Observation.ndjson#1", func(contribution semantic.Contribution) {
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
	profiler.ObservePayloadWithInventory(payload, map[string]float64{}, "Observation.ndjson#1", func(contribution semantic.Contribution) {
		identities[contribution.Key] = contribution.ConceptID + ":" + contribution.BindingID + ":" + fmt.Sprint(contribution.Ordinal)
	})
	return identities
}

func TestSemanticObservationsKeepCodingPairAndOwnerScope(t *testing.T) {
	p := NewProfilerForGeneration("project", "generation", "scope", "Observation", nil)
	payload := map[string]any{
		"resourceType": "Observation",
		"code":         map[string]any{"coding": []any{map[string]any{"system": "urn:root", "code": "root-code"}}},
		"component": []any{
			map[string]any{
				"code": map[string]any{"coding": []any{
					map[string]any{"system": "urn:study:A", "code": "shared"},
					map[string]any{"system": "urn:decoy", "code": "decoy"},
				}},
				"valueQuantity": map[string]any{"value": 111.0, "unit": "cm"},
			},
			map[string]any{
				"code":          map[string]any{"coding": []any{map[string]any{"system": "urn:study:B", "code": "shared"}}},
				"valueQuantity": map[string]any{"value": 222.0, "unit": "cm"},
			},
		},
		"extension": []any{map[string]any{
			"url":       "parent",
			"extension": []any{map[string]any{"url": "leaf", "valueString": "left-only"}},
		}},
	}
	p.ObservePayload(payload, map[string]float64{})
	documents := p.Documents()
	var observations []semantic.Observation
	for _, document := range documents {
		observations = append(observations, document.SemanticObservations...)
	}
	byPair := map[string]semantic.Observation{}
	for _, observation := range observations {
		if observation.OwningScope != "component[]" || observation.Key.Code != "shared" {
			continue
		}
		byPair[observation.Key.System+"\x00"+observation.Key.Code] = observation
	}
	if got := byPair["urn:study:A\x00shared"]; got.Status != "SUPPORTED" || got.Examples[0] != "111" || got.ObservedUnits[0] != "cm" || got.LogicalType != "decimal" || got.Value.Type != "decimal" || got.Value.Selector != "valueQuantity.value" {
		t.Fatalf("A paired observation = %#v", got)
	}
	if got := byPair["urn:study:B\x00shared"]; got.Status != "SUPPORTED" || got.Examples[0] != "222" {
		t.Fatalf("B paired observation = %#v", got)
	}
	pairedValues := 0
	for _, observation := range observations {
		if observation.RuleHint == semantic.RuleHintCodedValueV1 && observation.OwningScope == "component[]" && observation.Key.Code == "shared" {
			pairedValues++
		}
		if observation.RuleHint == semantic.RuleHintCodedConceptV1 && observation.OwningScope == "component[]" && observation.Key.Code == "shared" {
			t.Fatalf("paired CodeableConcept was also emitted as standalone: %#v", observation)
		}
		if len(observation.ExtensionURLPath) == 2 && observation.ExtensionURLPath[0] == "parent" && observation.ExtensionURLPath[1] == "leaf" {
			if observation.Examples[0] != "left-only" {
				t.Fatalf("nested extension example = %#v", observation)
			}
			if pairedValues != 2 {
				t.Fatalf("paired coded observations = %d, want 2", pairedValues)
			}
			return
		}
	}
	t.Fatal("nested extension ancestry was not retained")
}

func TestStandaloneCodingScopesObserveEachIdentifiedCodingAtItsOwnerScope(t *testing.T) {
	payload := map[string]any{
		"resourceType": "Observation",
		"status":       "final",
		"meta": map[string]any{"security": []any{
			map[string]any{"system": "urn:security", "code": "security-code", "display": "Security label"},
		}},
		"identifier": []any{map[string]any{
			"system": "urn:identifier",
			"value":  "identifier-value",
			"type": map[string]any{"coding": []any{
				map[string]any{"system": "urn:identifier-type", "code": "identifier-type"},
			}},
		}},
		"extension": []any{map[string]any{
			"url": "urn:extension",
			"valueCodeableConcept": map[string]any{"coding": []any{
				map[string]any{"system": "urn:extension-value", "code": "extension-code"},
			}},
		}},
		"code": map[string]any{"coding": []any{
			map[string]any{"system": "urn:root", "code": "root-code", "display": "Root label"},
		}},
		"component": []any{map[string]any{
			"code": map[string]any{"coding": []any{
				map[string]any{"system": "urn:panel", "version": "v1", "code": "alpha", "display": "Alpha"},
				map[string]any{"system": "urn:panel", "version": "v2", "code": "beta", "display": "  "},
				map[string]any{"system": "", "code": "missing-system", "display": "Unidentified system"},
				map[string]any{"system": "urn:panel", "code": "", "display": "Unidentified code"},
			}},
		}},
	}

	var contributions []semantic.Contribution
	emitter := semantic.NewEmitter("project", "generation")
	emitter.ObservePayload(payload, "Observation", "scope", "observation-1", func(contribution semantic.Contribution) {
		contributions = append(contributions, contribution)
	})
	byCode := map[string]semantic.Contribution{}
	for _, contribution := range contributions {
		if contribution.Observation.RuleHint == semantic.RuleHintCodedConceptV1 {
			byCode[contribution.Observation.Key.Code] = contribution
		}
	}
	if len(byCode) != 4 {
		t.Fatalf("standalone coded concept contributions = %d, want root, security, alpha, and beta: %#v", len(byCode), byCode)
	}

	root := byCode["root-code"].Observation
	if root.OwningScope != "" || root.Source.Path != "code.coding[]" || root.Source.Canonical != "Observation.code.coding[]" || root.Key.Selector != "code.coding[]" || root.Value.Selector != "code.coding[].display" || !reflect.DeepEqual(root.Value.FallbackSelectors, []string{"code.coding[].code"}) {
		t.Fatalf("root standalone observation = %#v", root)
	}

	security := byCode["security-code"].Observation
	if security.OwningScope != "" || security.Source.Path != "meta.security[]" || security.Source.Canonical != "Observation.meta.security[]" || security.Key.Selector != "meta.security[]" || security.Value.Selector != "meta.security[].display" || !reflect.DeepEqual(security.Value.FallbackSelectors, []string{"meta.security[].code"}) {
		t.Fatalf("direct Coding observation = %#v", security)
	}

	alpha := byCode["alpha"]
	if alpha.ConceptID == "" || alpha.BindingID == "" || alpha.Observation.Population != 1 || alpha.Observation.Source.Path != "component[].code.coding[]" || alpha.Observation.Source.Canonical != "Observation.component[].code.coding[]" || alpha.Observation.OwningScope != "component[]" {
		t.Fatalf("alpha standalone identity or owner scope = %#v", alpha)
	}
	if alpha.Observation.Key.System != "urn:panel" || alpha.Observation.Key.Version != "v1" || alpha.Observation.Key.Code != "alpha" || alpha.Observation.Key.Display != "Alpha" || alpha.Example != "Alpha" {
		t.Fatalf("alpha coding projection = %#v", alpha)
	}
	if alpha.Observation.Key.Selector != "code.coding[]" || alpha.Observation.Value.Selector != "code.coding[].display" || alpha.Observation.Value.Type != "string" || !reflect.DeepEqual(alpha.Observation.Value.FallbackSelectors, []string{"code.coding[].code"}) || !alpha.Observation.Value.FallbackOnBlankString {
		t.Fatalf("alpha display/fallback selectors = %#v", alpha.Observation.Value)
	}

	beta := byCode["beta"]
	if beta.Observation.Key.System != "urn:panel" || beta.Observation.Key.Version != "v2" || beta.Observation.Key.Code != "beta" || beta.Observation.Key.Display != "  " || beta.Example != "beta" {
		t.Fatalf("beta coding projection mixed a sibling coding's display or fallback: %#v", beta)
	}
	if beta.Observation.Value.Selector != "code.coding[].display" || !reflect.DeepEqual(beta.Observation.Value.FallbackSelectors, []string{"code.coding[].code"}) || !beta.Observation.Value.FallbackOnBlankString {
		t.Fatalf("beta display/fallback selectors = %#v", beta.Observation.Value)
	}
}

func TestStandaloneScalarCodingChoiceUsesRepeatedOwnerAndExcludesIdentifierAndExtension(t *testing.T) {
	payload := map[string]any{
		"resourceType": "Task",
		"status":       "requested",
		"intent":       "order",
		"input": []any{map[string]any{
			"type": map[string]any{"text": "Task result"},
			"valueCoding": map[string]any{
				"system":  "urn:task-result",
				"version": "2025",
				"code":    "approved",
				"display": "Approved",
			},
		}},
		"identifier": []any{map[string]any{
			"system": "urn:task-identifier",
			"value":  "task-1",
			"type": map[string]any{"coding": []any{
				map[string]any{"system": "urn:identifier-type", "code": "identifier-type", "display": "Identifier type"},
			}},
		}},
		"extension": []any{map[string]any{
			"url": "urn:task-extension",
			"valueCoding": map[string]any{
				"system":  "urn:extension-value",
				"code":    "extension-code",
				"display": "Extension value",
			},
		}},
	}

	var contributions []semantic.Contribution
	emitter := semantic.NewEmitter("project", "generation")
	emitter.ObservePayload(payload, "Task", "scope", "task-1", func(contribution semantic.Contribution) {
		contributions = append(contributions, contribution)
	})
	var standalone []semantic.Contribution
	for _, contribution := range contributions {
		if contribution.Observation.RuleHint == semantic.RuleHintCodedConceptV1 {
			standalone = append(standalone, contribution)
		}
	}
	if len(standalone) != 1 {
		t.Fatalf("standalone Task Coding contributions = %d, want only valueCoding: %#v", len(standalone), standalone)
	}

	contribution := standalone[0]
	observation := contribution.Observation
	if observation.Source.Type != "Task" || observation.Source.Path != "input[].valueCoding" || observation.Source.Canonical != "Task.input[].valueCoding" ||
		observation.OwningScope != "input[]" || observation.Key.Selector != "valueCoding" ||
		observation.Value.Selector != "valueCoding.display" || !reflect.DeepEqual(observation.Value.FallbackSelectors, []string{"valueCoding.code"}) {
		t.Fatalf("scalar Coding owner-relative selectors = %#v", observation)
	}
	if observation.Key.System != "urn:task-result" || observation.Key.Version != "2025" || observation.Key.Code != "approved" || observation.Key.Display != "Approved" || contribution.Example != "Approved" || observation.Population != 1 {
		t.Fatalf("scalar Coding identity/display = %#v", contribution)
	}
}

func TestSemanticObservationsUseGeneratedDatePrimitive(t *testing.T) {
	p := NewProfilerForGeneration("project", "generation", "scope", "Observation", nil)
	p.ObservePayload(map[string]any{
		"resourceType": "Observation",
		"code": map[string]any{"coding": []any{
			map[string]any{"system": "urn:study", "code": "observed-at"},
		}},
		"valueDateTime": "2026-09-16T12:30:00Z",
	}, map[string]float64{})
	for _, document := range p.Documents() {
		for _, observation := range document.SemanticObservations {
			if observation.Key.Code != "observed-at" {
				continue
			}
			if observation.Value.Selector != "valueDateTime" || observation.Value.Type != "date_time" || observation.LogicalType != "date_time" {
				t.Fatalf("date-time observation = %#v", observation)
			}
			return
		}
	}
	t.Fatal("date-time semantic observation was not retained")
}

func TestSemanticObservationProjectionNeedsObservedScalar(t *testing.T) {
	tests := []struct {
		name     string
		arm      string
		value    map[string]any
		selector string
		status   string
	}{
		{name: "coding-only CodeableConcept", arm: "valueCodeableConcept", value: map[string]any{"coding": []any{map[string]any{"system": "http://snomed.info/sct", "code": "1222593009"}}}, selector: "valueCodeableConcept.text", status: semantic.StatusUnsupportedValueProjection},
		{name: "blank CodeableConcept text", arm: "valueCodeableConcept", value: map[string]any{"text": "  ", "coding": []any{map[string]any{"system": "http://snomed.info/sct", "code": "1222593009"}}}, selector: "valueCodeableConcept.text", status: semantic.StatusUnsupportedValueProjection},
		{name: "Quantity without value", arm: "valueQuantity", value: map[string]any{"unit": "days"}, selector: "valueQuantity.value", status: semantic.StatusUnsupportedValueProjection},
		{name: "Period without start", arm: "valuePeriod", value: map[string]any{"end": "2026-09-01"}, selector: "valuePeriod.start", status: semantic.StatusUnsupportedValueProjection},
		{name: "Range without low value", arm: "valueRange", value: map[string]any{"low": map[string]any{"unit": "days"}}, selector: "valueRange.low.value", status: semantic.StatusUnsupportedValueProjection},
		{name: "Ratio without numerator value", arm: "valueRatio", value: map[string]any{"numerator": map[string]any{"unit": "mg"}}, selector: "valueRatio.numerator.value", status: semantic.StatusUnsupportedValueProjection},
		{name: "CodeableConcept with text", arm: "valueCodeableConcept", value: map[string]any{"text": "Stage I"}, selector: "valueCodeableConcept.text", status: "SUPPORTED"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			p := NewProfilerForGeneration("project", "generation", "scope", "Observation", nil)
			p.ObservePayload(map[string]any{
				"resourceType": "Observation",
				"code": map[string]any{"coding": []any{
					map[string]any{"system": "urn:study", "code": "stage", "display": "Stage"},
				}},
				test.arm: test.value,
			}, map[string]float64{})

			for _, document := range p.Documents() {
				for _, observation := range document.SemanticObservations {
					if observation.RuleHint != semantic.RuleHintCodedValueV1 || observation.Key.Code != "stage" {
						continue
					}
					if observation.Value.Selector != test.selector || observation.Status != test.status {
						t.Fatalf("profiled value projection = %#v, want selector %q and status %q", observation, test.selector, test.status)
					}
					return
				}
			}
			t.Fatal("Observation value was not retained in the semantic catalog")
		})
	}
}

func TestSemanticObservationMergeDoesNotDoubleCountNewKey(t *testing.T) {
	newPayload := func(system, code string, value float64) map[string]any {
		return map[string]any{
			"resourceType": "Observation",
			"component": []any{map[string]any{
				"code":          map[string]any{"coding": []any{map[string]any{"system": system, "code": code}}},
				"valueQuantity": map[string]any{"value": value, "unit": "cm"},
			}},
		}
	}
	left := NewProfilerForGeneration("project", "generation", "scope", "Observation", nil)
	right := NewProfilerForGeneration("project", "generation", "scope", "Observation", nil)
	left.ObservePayload(newPayload("urn:left", "height", 111), map[string]float64{})
	right.ObservePayload(newPayload("urn:right", "height", 222), map[string]float64{})
	if err := left.Merge(right); err != nil {
		t.Fatal(err)
	}
	for _, document := range left.Documents() {
		for _, observation := range document.SemanticObservations {
			if observation.Key.System == "urn:right" && observation.Key.Code == "height" {
				if observation.Population != 1 {
					t.Fatalf("new merged observation population = %d, want 1: %#v", observation.Population, observation)
				}
				return
			}
		}
	}
	t.Fatal("merged right-hand semantic observation was not retained")
}

func TestSemanticObservationsExposeMissingAndMixedStates(t *testing.T) {
	p := NewProfilerForGeneration("project", "generation", "scope", "Observation", nil)
	p.ObservePayload(map[string]any{
		"resourceType": "Observation",
		"component": []any{
			map[string]any{
				"code":          map[string]any{"coding": []any{map[string]any{"code": "missing-system"}}},
				"valueQuantity": map[string]any{"value": 333.0},
			},
			map[string]any{
				"code":          map[string]any{"coding": []any{map[string]any{"system": "urn:mixed", "code": "mixed"}}},
				"valueString":   "not-numeric",
				"valueQuantity": map[string]any{"value": 1.0, "unit": "mg"},
			},
		},
	}, map[string]float64{})
	var missing, mixed int
	for _, document := range p.Documents() {
		for _, observation := range document.SemanticObservations {
			if observation.Key.Code == "missing-system" && observation.Status == "SUPPORTED" {
				t.Fatal("missing-system candidate was reported supported")
			}
			if observation.Key.Code == "mixed" && observation.Status == "MIXED_CHOICE" {
				mixed++
			}
			if observation.Status == "UNRESOLVED_SYSTEM" {
				missing++
			}
		}
	}
	if missing == 0 {
		t.Fatal("missing system candidate was not retained")
	}
	if mixed < 2 {
		t.Fatalf("mixed choice observations = %d, want both raw arms", mixed)
	}
}
