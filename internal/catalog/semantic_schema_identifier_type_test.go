package catalog

import (
	"strings"
	"testing"
)

func TestSemanticInventoryKeepsIdentifierTypeCodingRaw(t *testing.T) {
	payload := map[string]any{
		"resourceType": "Patient",
		"identifier": []any{map[string]any{
			"system": "urn:mrn",
			"value":  "mrn-17",
			"type": map[string]any{"coding": []any{map[string]any{
				"system":  "urn:identifier-type",
				"version": "v3",
				"code":    "MR",
				"display": " ",
			}}},
		}},
	}
	var semantic []SemanticInventoryContribution
	var emissions []SemanticCoverageEmissionEvidence
	var coverage SemanticCoverageSourceContribution
	emitter := NewSemanticInventoryEmitter("identifier-type-test", "generation-1")
	if err := emitter.ObservePayloadWithCoverage(
		payload,
		"Patient",
		"scope-a",
		"retained:Patient/identifier-type",
		"schema-sha256-test",
		func(contribution SemanticInventoryContribution) { semantic = append(semantic, contribution) },
		func(evidence SemanticCoverageEmissionEvidence) { emissions = append(emissions, evidence) },
		func(contribution SemanticCoverageSourceContribution) error {
			coverage = contribution
			return nil
		},
	); err != nil {
		t.Fatalf("observe Identifier.type with coverage: %v", err)
	}

	var identifier *SemanticInventoryContribution
	var identifierTypeEmission bool
	for index := range semantic {
		contribution := &semantic[index]
		if contribution.Observation.RuleHint == "IDENTIFIER_SYSTEM_VALUE" {
			identifier = contribution
		}
		if contribution.Observation.Source.Path == "identifier[].type.coding[]" {
			identifierTypeEmission = true
		}
	}
	if identifier == nil || identifier.Observation.Source.Path != "identifier[]" || identifier.Observation.Key.System != "urn:mrn" ||
		identifier.Observation.Key.Selector != "identifier[].system" || identifier.Observation.Value.Selector != "identifier[].value" {
		t.Fatalf("Identifier namespace/value family changed: %#v", identifier)
	}
	if identifierTypeEmission || len(semantic) != 1 {
		t.Fatalf("Identifier.type Coding must remain raw beside Identifier.system/value; contributions=%#v", semantic)
	}
	coding := findContributionCoveragePath(t, coverage, SemanticCoverageCoding, "identifier[].type.coding[]")
	if coding.Kind != SemanticCoverageCoding || coding.Disposition == SemanticCoverageSemanticDisposition || len(emissions) != 1 || emissions[0].SourcePath != "identifier[]" {
		t.Fatalf("Identifier.type census/emission evidence = %#v/%#v, want one raw Coding owner and only Identifier system/value emission", coding, emissions)
	}
}

func TestSemanticInventoryKeepsIdentifierTypeCodingInsideExtensionURLAncestry(t *testing.T) {
	payload := map[string]any{
		"resourceType": "Patient",
		"extension": []any{
			map[string]any{
				"url": "urn:extension:current",
				"valueIdentifier": map[string]any{
					"system": "urn:namespace",
					"value":  "current-id",
					"type": map[string]any{"coding": []any{map[string]any{
						"system": "urn:identifier-type", "version": "v3", "code": "MR", "display": "Current MR",
					}}},
				},
			},
			map[string]any{
				"url": "urn:extension:history",
				"valueIdentifier": map[string]any{
					"system": "urn:namespace",
					"value":  "historical-id",
					"type": map[string]any{"coding": []any{map[string]any{
						"system": "urn:identifier-type", "version": "v3", "code": "MR", "display": "Historical MR",
					}}},
				},
			},
		},
	}
	var semantic []SemanticInventoryContribution
	emitter := NewSemanticInventoryEmitter("identifier-type-test", "generation-1")
	if err := emitter.ObservePayload(payload, "Patient", "scope-a", "retained:Patient/extension-identifiers", func(contribution SemanticInventoryContribution) {
		semantic = append(semantic, contribution)
	}); err != nil {
		t.Fatalf("observe Extension.valueIdentifier: %v", err)
	}

	identifiers := map[string]SemanticInventoryContribution{}
	byExtensionURL := map[string]SemanticInventoryContribution{}
	for _, contribution := range semantic {
		observation := contribution.Observation
		switch observation.RuleHint {
		case "IDENTIFIER_SYSTEM_VALUE":
			identifiers[contribution.Example] = contribution
		case "EXTENSION_URL_VALUE":
			if len(observation.ExtensionURLPath) == 1 {
				byExtensionURL[observation.ExtensionURLPath[0]] = contribution
			}
		case SemanticRuleHintCodedConceptV1:
			if strings.Contains(observation.Source.Path, "valueIdentifier.type.coding[]") {
				t.Fatalf("Identifier.type under Extension escaped URL scoping: %#v", contribution)
			}
		}
	}
	if len(identifiers) != 2 {
		t.Fatalf("Extension.valueIdentifier namespace/value observations = %d, want both Identifier values: %#v", len(identifiers), identifiers)
	}
	for _, value := range []string{"current-id", "historical-id"} {
		if contribution, ok := identifiers[value]; !ok || contribution.Observation.Key.System != "urn:namespace" ||
			contribution.Observation.Value.Selector != "extension[].valueIdentifier.value" {
			t.Errorf("Extension.valueIdentifier namespace/value for %q = %#v", value, contribution)
		}
	}
	if len(byExtensionURL) != 2 {
		t.Fatalf("Extension URL semantic observations = %d, want two URL owners: %#v", len(byExtensionURL), byExtensionURL)
	}
	current := byExtensionURL["urn:extension:current"]
	history := byExtensionURL["urn:extension:history"]
	for url, contribution := range byExtensionURL {
		observation := contribution.Observation
		if observation.Key.Selector != "extension[].url" || observation.Key.Display != url ||
			observation.ChoiceArm != "valueIdentifier" || observation.Value.Selector != "extension[].valueIdentifier" {
			t.Errorf("Extension URL/value binding for %q = %#v", url, contribution)
		}
	}
	if current.ConceptID == history.ConceptID {
		t.Fatalf("sibling Extension URL ancestries merged: current=%+v history=%+v", current, history)
	}
}
