package catalog

import (
	"strconv"

	fhirsemantic "github.com/calypr/loom/internal/fhir/semantic"
)

const SemanticRuleHintStructuredSlotV1 = "STRUCTURED_SLOT_V1"

func (p *Profiler) emitStructuredScope(scope *semanticScope, profile string, emit func(SemanticObservation, []any)) {
	observation := SemanticObservation{
		SchemaVersion: SemanticObservationSchemaVersion,
		Role:          SemanticRoleStructuredSlot,
		Source:        SemanticObservationSource{Canonical: semanticCanonical(p.resourceType, scope.canonicalPath), Type: p.resourceType, Profile: profile, Path: scope.canonicalPath},
		Key:           SemanticObservationKey{Selector: scope.canonicalPath},
		Value:         SemanticObservationValue{Selector: scope.canonicalPath, Type: "object"},
		OwningScope:   scope.canonicalPath,
		LogicalType:   "object", Completeness: SemanticComplete, Status: "SUPPORTED",
		RuleHint: SemanticRuleHintStructuredSlotV1, RuleVersion: strconv.Itoa(SemanticObservationRuleVersion),
	}
	observation.SlotLabel, observation.SlotDescription = semanticSlotPresentation(scope.member, scope.canonicalPath)
	p.recordSchemaSemanticObservation(scope.canonicalPath, observation, []any{scope.value}, emit)
}

func semanticScopeHasOwner(scope *semanticScope, scopes map[string]*semanticScope, registry *fhirsemantic.DatatypeRegistry) bool {
	for scope.ownerPath != "" {
		parent := scopes[directOwnerPath(scope.ownerPath)]
		if parent == nil || parent == scope {
			return false
		}
		if descriptor, ok := registry.Lookup(parent.definition); ok && descriptor.Disposition != fhirsemantic.DispositionAdvancedOnly {
			return true
		}
		if registry.HasUnresolvedValueGroup(parent.definition) && scope.member.ChoiceGroup == "value" {
			return true
		}
		for _, pairing := range registry.ScopePairings(parent.definition) {
			if pairing.ValuePath == scope.member.Name || pairing.ValueChoiceGroup != "" && pairing.ValueChoiceGroup == scope.member.ChoiceGroup {
				return true
			}
		}
		scope = parent
	}
	return false
}
