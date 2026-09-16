package ir

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/spec"

	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

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
	case PhysicalCountAggregate, PhysicalCountDistinctAggregate, PhysicalExistsAggregate, PhysicalDistinctValuesAggregate, PhysicalMinAggregate, PhysicalMaxAggregate, PhysicalFirstAggregate, PhysicalContainsAllAggregate:
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
	if aggregate.Operation == PhysicalContainsAllAggregate {
		if strings.TrimSpace(aggregate.RequiredValuesBindKey) == "" {
			return fmt.Errorf("CONTAINS_ALL requires required values bind")
		}
		if err := requireBind(bindVars, aggregate.RequiredValuesBindKey); err != nil {
			return err
		}
		values, ok := bindVars[aggregate.RequiredValuesBindKey].([]string)
		if !ok || len(values) == 0 {
			return fmt.Errorf("required values bind %q must be a non-empty []string", aggregate.RequiredValuesBindKey)
		}
		seen := map[string]bool{}
		for _, value := range values {
			if strings.TrimSpace(value) == "" || seen[value] {
				return fmt.Errorf("required values bind %q contains an empty or duplicate value", aggregate.RequiredValuesBindKey)
			}
			seen[value] = true
		}
	} else if strings.TrimSpace(aggregate.RequiredValuesBindKey) != "" {
		return fmt.Errorf("aggregate operation %q does not accept required values", aggregate.Operation)
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
	if pivot.Correlation == nil {
		if err := validatePhysicalSelector(pivot.ResourceType, pivot.KeySelector); err != nil {
			return fmt.Errorf("pivot key selector: %w", err)
		}
		if err := validatePhysicalSelector(pivot.ResourceType, pivot.ValueSelector); err != nil {
			return fmt.Errorf("pivot value selector: %w", err)
		}
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
	if mode := strings.ToUpper(strings.TrimSpace(pivot.ProjectionMode)); mode != "" && mode != "VALUE" && mode != "FIRST" && mode != "ALL" && mode != "DISTINCT" {
		return fmt.Errorf("pivot projection mode %q is unsupported", pivot.ProjectionMode)
	}
	aliases := map[string]string{}
	for key, alias := range pivot.ColumnAliases {
		if !containsString(columns, key) || strings.TrimSpace(alias) == "" {
			return fmt.Errorf("pivot column alias %q is invalid", key)
		}
		if previous, exists := aliases[alias]; exists && previous != key {
			return fmt.Errorf("pivot column alias %q is shared by %q and %q", alias, previous, key)
		}
		aliases[alias] = key
	}
	if pivot.Correlation != nil {
		if err := validatePhysicalCorrelation(*pivot.Correlation, defined, bindVars); err != nil {
			return fmt.Errorf("pivot correlation: %w", err)
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

func validatePhysicalCorrelation(correlation PhysicalCorrelation, defined map[string]bool, bindVars map[string]any) error {
	if err := validatePhysicalValue(correlation.Source, defined, bindVars); err != nil {
		return err
	}
	if strings.TrimSpace(correlation.ResourceType) == "" || !schemaDefinitionExists(correlation.ResourceType) {
		return fmt.Errorf("correlation resource type %q is not represented by generated FHIR schema", correlation.ResourceType)
	}
	ownerResource := correlation.OwnerResource
	if ownerResource == "" {
		ownerResource = correlation.ResourceType
	}
	if !schemaDefinitionExists(ownerResource) {
		return fmt.Errorf("correlation owner resource %q is not represented by generated FHIR schema", ownerResource)
	}
	if correlation.OwnerSelector.CanonicalPath() != "" {
		if err := validatePhysicalSelector(correlation.ResourceType, correlation.OwnerSelector); err != nil {
			return fmt.Errorf("owner selector: %w", err)
		}
	}
	if strings.TrimSpace(correlation.KeyResource) == "" || !schemaDefinitionExists(correlation.KeyResource) {
		return fmt.Errorf("correlation key resource %q is not represented by generated FHIR schema", correlation.KeyResource)
	}
	if err := validatePhysicalSelector(ownerResource, correlation.KeySelector); err != nil {
		return fmt.Errorf("key selector: %w", err)
	}
	if err := validatePhysicalSelector(correlation.KeyResource, correlation.SystemSelector); err != nil {
		return fmt.Errorf("system selector: %w", err)
	}
	if err := validatePhysicalSelector(correlation.KeyResource, correlation.CodeSelector); err != nil {
		return fmt.Errorf("code selector: %w", err)
	}
	if err := validatePhysicalSelector(ownerResource, correlation.ValueSelector); err != nil {
		return fmt.Errorf("value selector: %w", err)
	}
	for index, fallback := range correlation.ValueFallbacks {
		if err := validatePhysicalSelector(ownerResource, fallback); err != nil {
			return fmt.Errorf("value fallback %d: %w", index, err)
		}
	}
	for index, choice := range correlation.ChoiceSelectors {
		if err := validatePhysicalSelector(ownerResource, choice); err != nil {
			return fmt.Errorf("choice selector %d: %w", index, err)
		}
	}
	if strings.TrimSpace(correlation.LogicalType) == "" {
		return fmt.Errorf("correlation logical type is required")
	}
	if primitive := strings.TrimSpace(correlation.ValuePrimitive); primitive != "" && primitive != string(fhirschema.PrimitiveString) && primitive != string(fhirschema.PrimitiveBoolean) && primitive != string(fhirschema.PrimitiveInteger) && primitive != string(fhirschema.PrimitiveDecimal) && primitive != string(fhirschema.PrimitiveDate) && primitive != string(fhirschema.PrimitiveDateTime) {
		return fmt.Errorf("correlation value primitive %q is unsupported", correlation.ValuePrimitive)
	}
	for name, key := range map[string]string{"system": correlation.SystemBindKey, "code": correlation.CodeBindKey} {
		if strings.TrimSpace(key) == "" {
			return fmt.Errorf("correlation %s bind key is required", name)
		}
		if err := requireBind(bindVars, key); err != nil {
			return err
		}
		if _, ok := bindVars[key].(string); !ok {
			return fmt.Errorf("correlation %s bind %q must be a string", name, key)
		}
	}
	return nil
}
