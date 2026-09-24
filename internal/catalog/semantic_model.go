package catalog

// SemanticObservationRole is the closed domain role carried explicitly by a
// semantic observation. Keeping role classification separate from the
// observed key/value envelope prevents a structural categorical slot from
// pretending that one observed Coding is its identity.
type SemanticObservationRole string

const (
	SemanticRoleUnknown         SemanticObservationRole = "UNKNOWN"
	SemanticRoleIdentifier      SemanticObservationRole = "IDENTIFIER_SYSTEM_VALUE"
	SemanticRoleExtension       SemanticObservationRole = "EXTENSION_URL_VALUE"
	SemanticRoleCodedValue      SemanticObservationRole = "DISCRIMINATED_VALUE"
	SemanticRoleCategoricalSlot SemanticObservationRole = "CATEGORICAL_SLOT"
	SemanticRoleStructuredSlot  SemanticObservationRole = "STRUCTURED_SLOT"
)

// SemanticObservationRoleForRule maps the persisted rule envelope to its
// closed domain role. Unknown rules are intentionally retained as unknown so
// callers can route them to review without interpreting identity fields.
func SemanticObservationRoleForRule(rule string) SemanticObservationRole {
	switch rule {
	case "IDENTIFIER_SYSTEM_VALUE":
		return SemanticRoleIdentifier
	case "EXTENSION_URL_VALUE":
		return SemanticRoleExtension
	case SemanticRuleHintCodedValueV1:
		return SemanticRoleCodedValue
	case SemanticRuleHintCategoricalSlotV1:
		return SemanticRoleCategoricalSlot
	case SemanticRuleHintStructuredSlotV1:
		return SemanticRoleStructuredSlot
	case "CATEGORICAL_CODE_V1":
		// Persisted observations from the pre-slot model remain readable, but
		// newly emitted observations use CATEGORICAL_SLOT_V1 and an explicit
		// role. The old rule never changes the structural identity here.
		return SemanticRoleCategoricalSlot
	default:
		return SemanticRoleUnknown
	}
}

// SemanticObservationRoleOf returns the explicit role on new observations and
// derives the role from RuleHint for older persisted observations.
func SemanticObservationRoleOf(observation SemanticObservation) SemanticObservationRole {
	if observation.Role != "" {
		switch observation.Role {
		case SemanticRoleIdentifier, SemanticRoleExtension, SemanticRoleCodedValue, SemanticRoleCategoricalSlot, SemanticRoleStructuredSlot:
			return observation.Role
		default:
			return SemanticRoleUnknown
		}
	}
	return SemanticObservationRoleForRule(observation.RuleHint)
}
