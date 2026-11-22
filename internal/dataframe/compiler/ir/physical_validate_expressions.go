package ir

import (
	"fmt"
	"strings"
)

func (validation *physicalOperationValidation) validateFilter(i int, operation PhysicalOperation) error {
	p := validation.plan
	if err := validatePhysicalFilter(*operation.Filter, validation.defined, p.BindVars); err != nil {
		return fmt.Errorf("operation %d: %w", i, err)
	}
	return nil
}

func (validation *physicalOperationValidation) validateDerivedLet(i int, operation PhysicalOperation) error {
	p := validation.plan
	derived := operation.DerivedLet
	if err := validatePhysicalDerivedLet(*derived, validation.defined, p.BindVars); err != nil {
		return fmt.Errorf("operation %d: %w", i, err)
	}
	if err := definePhysicalVariable(validation.defined, derived.Variable); err != nil {
		return fmt.Errorf("operation %d: %w", i, err)
	}
	return nil
}

func (validation *physicalOperationValidation) validateExpressionLet(i int, operation PhysicalOperation) error {
	p := validation.plan
	if operation.ExpressionLet == nil || strings.TrimSpace(operation.ExpressionLet.Variable) == "" {
		return fmt.Errorf("operation %d: expression LET payload and variable are required", i)
	}
	if err := validatePhysicalExpression(operation.ExpressionLet.Expression, validation.defined, p.BindVars); err != nil {
		return fmt.Errorf("operation %d expression LET: %w", i, err)
	}
	if err := definePhysicalVariable(validation.defined, operation.ExpressionLet.Variable); err != nil {
		return fmt.Errorf("operation %d: %w", i, err)
	}
	return nil
}

func (validation *physicalOperationValidation) validateSet(i int, operation PhysicalOperation) error {
	p := validation.plan
	set := operation.Set
	if err := validatePhysicalSet(*set, validation.defined, p.BindVars); err != nil {
		return fmt.Errorf("operation %d set %q: %w", i, set.Variable, err)
	}
	if err := definePhysicalVariable(validation.defined, set.Variable); err != nil {
		return fmt.Errorf("operation %d: %w", i, err)
	}
	if set.Reduction != nil {
		if err := definePhysicalVariable(validation.defined, set.Reduction.Variable); err != nil {
			return fmt.Errorf("operation %d set reduction: %w", i, err)
		}
	}
	if set.Prepared != nil {
		if err := definePhysicalVariable(validation.defined, set.Prepared.Variable); err != nil {
			return fmt.Errorf("operation %d prepared set: %w", i, err)
		}
	}
	return nil
}

func (validation *physicalOperationValidation) validateUnnest(i int, operation PhysicalOperation) error {
	p := validation.plan
	if err := validatePhysicalUnnest(*operation.Unnest, validation.defined, p.BindVars); err != nil {
		return fmt.Errorf("operation %d unnest: %w", i, err)
	}
	if err := definePhysicalUnnestVariables(*operation.Unnest, validation.defined); err != nil {
		return fmt.Errorf("operation %d unnest bindings: %w", i, err)
	}
	return nil
}

func (validation *physicalOperationValidation) validateSort(i int, operation PhysicalOperation) error {
	p := validation.plan
	if len(operation.Sort.Keys) == 0 {
		return fmt.Errorf("operation %d sort requires at least one key", i)
	}
	for keyIndex, key := range operation.Sort.Keys {
		if err := validatePhysicalValue(key, validation.defined, p.BindVars); err != nil {
			return fmt.Errorf("operation %d sort key %d: %w", i, keyIndex, err)
		}
	}
	return nil
}

func (validation *physicalOperationValidation) validateLimit(i int, operation PhysicalOperation) error {
	p := validation.plan
	if err := requireBind(p.BindVars, operation.Limit.BindKey); err != nil {
		return fmt.Errorf("operation %d: %w", i, err)
	}
	limit, ok := p.BindVars[operation.Limit.BindKey].(int)
	if !ok || limit <= 0 {
		return fmt.Errorf("operation %d: limit bind %q must be a positive int", i, operation.Limit.BindKey)
	}
	return nil
}
