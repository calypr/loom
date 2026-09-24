package semantic

import "github.com/calypr/loom/internal/fhir/schema"

// HasUnresolvedValueGroup identifies values that must remain visible for
// review instead of becoming anonymous fields or guessed associations.
func (r *DatatypeRegistry) HasUnresolvedValueGroup(owner schema.DefinitionName) bool {
	return r != nil && r.unresolvedValues[owner]
}

// structuralScopePairing recognizes the FHIR discriminator/value convention,
// not arbitrary categorical siblings of any choice group.
func structuralScopePairing(definition schema.Definition) (ScopePairingDescriptor, bool) {
	var keys []schema.Element
	hasValue := false
	for _, element := range definition.Elements {
		if element.ChoiceGroup == "value" {
			hasValue = true
		}
		if (element.Name == "code" || element.Name == "type") &&
			(element.ReferencedType == "CodeableConcept" || element.ReferencedType == "Coding") &&
			element.ChoiceGroup == "" {
			keys = append(keys, element)
		}
	}
	if !hasValue || len(keys) != 1 {
		return ScopePairingDescriptor{}, false
	}
	return ScopePairingDescriptor{
		OwnerType: definition.Name, CategoricalPath: keys[0].Name,
		ValueChoiceGroup: "value", Rule: "DISCRIMINATOR_VALUE_CHOICE",
	}, true
}

func pairingOwnsValue(pairing ScopePairingDescriptor, element schema.Element) bool {
	return pairing.ValuePath != "" && pairing.ValuePath == element.Name ||
		pairing.ValueChoiceGroup != "" && pairing.ValueChoiceGroup == element.ChoiceGroup
}
