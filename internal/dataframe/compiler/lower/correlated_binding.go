package lower

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/spec"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

// LowerCorrelatedBinding converts the checked schema binding into the one
// physical representation shared by filters and projections. Callers provide
// bound terminology values; selectors and scope are never rendered as user
// supplied query text.
func LowerCorrelatedBinding(resourceType string, binding fhirschema.CorrelatedBinding, source ir.PhysicalValue, systemBindKey, codeBindKey string) (ir.PhysicalCorrelation, error) {
	checked, err := fhirschema.ValidateCorrelatedBinding(resourceType, binding)
	if err != nil {
		return ir.PhysicalCorrelation{}, err
	}
	if strings.TrimSpace(systemBindKey) == "" || strings.TrimSpace(codeBindKey) == "" {
		return ir.PhysicalCorrelation{}, fmt.Errorf("correlated binding system and code bind keys are required")
	}
	choiceArms := append([]string(nil), checked.ChoiceArms...)
	if len(choiceArms) == 0 {
		// A checked value[x] selector names its requested arm even when older
		// authoring omitted ChoiceArms. Infer only that structural arm; this is
		// not a clinical equivalence and lets runtime data expose another arm as
		// INVALID_CHOICE_ARM instead of disappearing as null.
		valuePath := checked.ValueSelector.CanonicalPath()
		if first, _, found := strings.Cut(valuePath, "."); found || strings.HasPrefix(valuePath, "value") {
			if found {
				choiceArms = []string{strings.TrimSuffix(first, "[]")}
			} else {
				choiceArms = []string{strings.TrimSuffix(valuePath, "[]")}
			}
		}
	}
	choiceSelectors := make([]spec.Selector, 0)
	if len(choiceArms) > 0 {
		for _, option := range fhirschema.ChoiceValueSelectorOptions(checked.OwnerResource) {
			selector, parseErr := spec.ParseSelector(fhirschema.SelectorExpression(option))
			if parseErr != nil {
				return ir.PhysicalCorrelation{}, fmt.Errorf("choice selector %q: %w", fhirschema.SelectorExpression(option), parseErr)
			}
			choiceSelectors = append(choiceSelectors, selector)
		}
	}
	return ir.PhysicalCorrelation{
		Source: source, ResourceType: resourceType, OwnerResource: checked.OwnerResource, OwnerSelector: checked.OwnerSelector, KeyResource: checked.KeyResource,
		KeySelector: checked.KeySelector, SystemSelector: checked.SystemSelector, CodeSelector: checked.CodeSelector,
		ValueSelector: checked.ValueSelector, ValueFallbacks: append([]spec.Selector(nil), checked.ValueFallbacks...),
		ChoiceArms: choiceArms, ChoiceSelectors: choiceSelectors, LogicalType: checked.LogicalType, ValuePrimitive: string(checked.ValuePrimitive),
		SystemBindKey: systemBindKey, CodeBindKey: codeBindKey,
	}, nil
}

// LowerCorrelatedPredicate uses the same binding as projection lowering. The
// code system and code are independent bind values but are evaluated against
// the same Coding item by the renderer.
func LowerCorrelatedPredicate(physical *ir.PhysicalPlan, resourceType string, binding fhirschema.CorrelatedBinding, source ir.PhysicalValue, code spec.CodeValue) (ir.PhysicalPredicate, error) {
	return LowerCorrelatedPredicateWithIdentity(physical, resourceType, binding, source, code, "")
}

// LowerCorrelatedPredicateWithIdentity is the filter-safe entry point for
// callers lowering more than one correlated predicate over the same source.
// The identity is part of both bind keys, so later filters cannot overwrite an
// earlier system/code pair in the shared physical bind map.
func LowerCorrelatedPredicateWithIdentity(physical *ir.PhysicalPlan, resourceType string, binding fhirschema.CorrelatedBinding, source ir.PhysicalValue, code spec.CodeValue, identity string) (ir.PhysicalPredicate, error) {
	if physical == nil {
		return ir.PhysicalPredicate{}, fmt.Errorf("physical plan is required")
	}
	suffix := sanitizeColumnName(source.Variable)
	if strings.TrimSpace(identity) != "" {
		suffix += "_" + sanitizeColumnName(identity)
	}
	systemKey := "correlated_system_" + suffix
	codeKey := "correlated_code_" + suffix
	physical.BindVars[systemKey] = code.System
	physical.BindVars[codeKey] = code.Code
	return correlatedPredicateWithBinds(resourceType, binding, source, systemKey, codeKey)
}

func correlatedPredicateWithBinds(resourceType string, binding fhirschema.CorrelatedBinding, source ir.PhysicalValue, systemKey, codeKey string) (ir.PhysicalPredicate, error) {
	correlation, err := LowerCorrelatedBinding(resourceType, binding, source, systemKey, codeKey)
	if err != nil {
		return ir.PhysicalPredicate{}, err
	}
	return ir.PhysicalPredicate{Operator: "EQUALS", ValueKind: spec.FilterCode, Correlation: &correlation}, nil
}
