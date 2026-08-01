package ir

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/spec"

	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

func validatePhysicalExtract(extract PhysicalExtract, defined map[string]bool, bindVars map[string]any) error {
	if err := validatePhysicalValue(extract.Source, defined, bindVars); err != nil {
		return err
	}
	if !schemaDefinitionExists(extract.ResourceType) {
		return fmt.Errorf("extract resource type %q is not represented by the active generated FHIR schema", extract.ResourceType)
	}
	if err := validatePhysicalSelector(extract.ResourceType, extract.Selector); err != nil {
		return fmt.Errorf("extract selector: %w", err)
	}
	if extract.ExecutionMode != "" && extract.ExecutionMode != PhysicalSelectorGeneric && extract.ExecutionMode != PhysicalSelectorDirectScalar && extract.ExecutionMode != PhysicalSelectorConditionalArray {
		return fmt.Errorf("unknown selector execution mode %q", extract.ExecutionMode)
	}
	if extract.ExecutionMode == PhysicalSelectorDirectScalar && (len(extract.Fallbacks) != 0 || extract.Selector.Filter != nil || !selectorHasNoArrays(extract.Selector)) {
		return fmt.Errorf("direct scalar selector mode requires one fallback-free non-repeated selector")
	}
	if extract.ExecutionMode == PhysicalSelectorConditionalArray && (len(extract.Fallbacks) != 0 || extract.Selector.Filter != nil || !selectorHasIteratedArray(extract.Selector)) {
		return fmt.Errorf("conditional array selector mode requires one fallback-free repeated selector")
	}
	for index, fallback := range extract.Fallbacks {
		if err := validatePhysicalSelector(extract.ResourceType, fallback); err != nil {
			return fmt.Errorf("extract fallback %d: %w", index, err)
		}
	}
	if extract.Prepared != nil {
		if err := validatePhysicalPreparedReference(*extract.Prepared, defined); err != nil {
			return err
		}
		if len(extract.Fallbacks) != 0 {
			return fmt.Errorf("prepared extract cannot use fallback selectors")
		}
	}
	return nil
}

func validatePhysicalPreparedReference(reference PhysicalPreparedReference, defined map[string]bool) error {
	if !physicalVariablePattern.MatchString(reference.SetVariable) || !defined[reference.SetVariable] {
		return fmt.Errorf("prepared set variable %q is out of scope", reference.SetVariable)
	}
	if !physicalVariablePattern.MatchString(reference.Field) {
		return fmt.Errorf("prepared field %q is unsafe", reference.Field)
	}
	return nil
}

func validatePhysicalSelector(resourceType string, selector spec.Selector) error {
	if len(selector.Steps) == 0 {
		return fmt.Errorf("selector is required")
	}
	if _, _, err := spec.SelectorCardinality(resourceType, selector); err != nil {
		return err
	}
	return nil
}

// schemaDefinitionExists accepts both top-level FHIR resources and generated
// backbone/choice definitions such as GroupMember. A definition may be a
// valid selector source without being a graph collection or traversal node;
// collection and route validation deliberately continue to use HasResource.
func schemaDefinitionExists(resourceType string) bool {
	return fhirschema.DefinitionExists(resourceType)
}

func validatePhysicalAggregate(aggregate PhysicalAggregate, defined map[string]bool, bindVars map[string]any) error {
	if err := validatePhysicalValue(aggregate.Source, defined, bindVars); err != nil {
		return err
	}
	switch aggregate.Operation {
	case PhysicalCountAggregate, PhysicalCountDistinctAggregate, PhysicalExistsAggregate, PhysicalDistinctValuesAggregate, PhysicalMinAggregate, PhysicalMaxAggregate, PhysicalFirstAggregate:
	default:
		return fmt.Errorf("unknown aggregate operation %q", aggregate.Operation)
	}
	needsValue := aggregate.Operation != PhysicalCountAggregate && aggregate.Operation != PhysicalExistsAggregate
	if needsValue != (aggregate.Value != nil) {
		return fmt.Errorf("aggregate operation %q value presence is invalid", aggregate.Operation)
	}
	if aggregate.Value != nil {
		if err := validatePhysicalExpression(*aggregate.Value, defined, bindVars); err != nil {
			return fmt.Errorf("aggregate value: %w", err)
		}
	}
	if aggregate.Predicate != nil {
		if err := validatePhysicalPredicateExpression(*aggregate.Predicate, defined, bindVars); err != nil {
			return fmt.Errorf("aggregate predicate: %w", err)
		}
	}
	return nil
}

func validatePhysicalPivot(pivot PhysicalPivotMap, defined map[string]bool, bindVars map[string]any) error {
	if err := validatePhysicalValue(pivot.Source, defined, bindVars); err != nil {
		return err
	}
	if strings.TrimSpace(pivot.ResourceType) == "" || !fhirschema.HasResource(pivot.ResourceType) {
		return fmt.Errorf("pivot resource type %q is not represented by the active generated FHIR schema", pivot.ResourceType)
	}
	if err := validatePhysicalSelector(pivot.ResourceType, pivot.KeySelector); err != nil {
		return fmt.Errorf("pivot key selector: %w", err)
	}
	if err := validatePhysicalSelector(pivot.ResourceType, pivot.ValueSelector); err != nil {
		return fmt.Errorf("pivot value selector: %w", err)
	}
	if err := requireBind(bindVars, pivot.ColumnsBindKey); err != nil {
		return err
	}
	columns, ok := bindVars[pivot.ColumnsBindKey].([]string)
	if !ok || len(columns) == 0 {
		return fmt.Errorf("pivot columns bind %q must be a non-empty []string", pivot.ColumnsBindKey)
	}
	for _, column := range columns {
		if strings.TrimSpace(column) == "" {
			return fmt.Errorf("pivot columns bind %q contains an empty column", pivot.ColumnsBindKey)
		}
	}
	if pivot.PreparedKey != nil {
		if err := validatePhysicalPreparedReference(*pivot.PreparedKey, defined); err != nil {
			return fmt.Errorf("prepared pivot key: %w", err)
		}
	}
	if pivot.PreparedValue != nil {
		if err := validatePhysicalPreparedReference(*pivot.PreparedValue, defined); err != nil {
			return fmt.Errorf("prepared pivot value: %w", err)
		}
	}
	return nil
}
