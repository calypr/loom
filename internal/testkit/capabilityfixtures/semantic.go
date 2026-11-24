package capabilityfixtures

import (
	"strconv"

	"github.com/calypr/loom/internal/semantic"
)

func SemanticSelectionEntry(conceptID, bindingID, system, code, version string) *semantic.Entry {
	return &semantic.Entry{
		ConceptID: conceptID,
		BindingID: bindingID,
		Observation: semantic.Observation{
			SchemaVersion: semantic.ObservationSchemaVersion,
			Source:        semantic.Source{Type: "Observation", Path: "code"},
			Key:           semantic.Key{Selector: "code.coding[]", System: system, Version: version, Code: code, Display: "Observed label"},
			Value:         semantic.Value{Selector: "valueQuantity.value", Type: "decimal"},
			ChoiceArm:     "valueQuantity", LogicalType: "decimal", Completeness: semantic.CompletenessComplete, Status: "SUPPORTED", RuleHint: semantic.RuleHintCodedValueV1, RuleVersion: strconv.Itoa(semantic.SemanticObservationRuleVersion),
		},
	}
}
