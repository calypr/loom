package ir

import (
	"fmt"
)

func (validation *physicalOperationValidation) validateGroupRows(i int, operation PhysicalOperation) error {
	p := validation.plan
	if i != len(p.Operations)-1 {
		return fmt.Errorf("operation %d: GROUP_ROWS must be the terminal operation", i)
	}
	if err := validatePhysicalGroupRows(*operation.GroupRows, p.BindVars); err != nil {
		return fmt.Errorf("operation %d group rows: %w", i, err)
	}
	validation.groupRows++
	validation.returns++
	return nil
}

func (validation *physicalOperationValidation) validateGroupedPivot(i int, operation PhysicalOperation) error {
	p := validation.plan
	validation.tableReshapes++
	if validation.tableReshapes > 1 {
		return fmt.Errorf("operation %d: physical plan has multiple table reshapes", i)
	}
	if validation.groupRows > 0 {
		return fmt.Errorf("operation %d: grouped row sources cannot be combined with table reshapes", i)
	}
	if err := validatePhysicalGroupedPivot(*operation.GroupedPivot, validation.defined, p.BindVars); err != nil {
		return fmt.Errorf("operation %d grouped pivot: %w", i, err)
	}
	pivotVariables := []string{operation.GroupedPivot.InputRowVariable}
	if !operation.GroupedPivot.OneInputRowPerGroup {
		pivotVariables = append(pivotVariables, operation.GroupedPivot.GroupRowsVariable)
	}
	pivotVariables = append(pivotVariables, groupedPivotVariables(operation.GroupedPivot.GroupKeys)...)
	for _, variable := range pivotVariables {
		if err := definePhysicalVariable(validation.defined, variable); err != nil {
			return fmt.Errorf("operation %d grouped pivot: %w", i, err)
		}
	}
	if err := definePhysicalVariable(validation.defined, operation.GroupedPivot.OutputRowVariable); err != nil {
		return fmt.Errorf("operation %d grouped pivot output row: %w", i, err)
	}
	validation.reshapeOutputVariable = operation.GroupedPivot.OutputRowVariable
	return nil
}

func (validation *physicalOperationValidation) validateUnpivot(i int, operation PhysicalOperation) error {
	p := validation.plan
	validation.tableReshapes++
	if validation.tableReshapes > 1 {
		return fmt.Errorf("operation %d: physical plan has multiple table reshapes", i)
	}
	if validation.groupRows > 0 {
		return fmt.Errorf("operation %d: grouped row sources cannot be combined with table reshapes", i)
	}
	if err := validatePhysicalUnpivot(*operation.Unpivot, validation.defined, p.BindVars); err != nil {
		return fmt.Errorf("operation %d unpivot: %w", i, err)
	}
	for _, variable := range []string{operation.Unpivot.InputRowVariable, operation.Unpivot.SlotVariable, operation.Unpivot.OutputRowVariable} {
		if err := definePhysicalVariable(validation.defined, variable); err != nil {
			return fmt.Errorf("operation %d unpivot: %w", i, err)
		}
	}
	validation.reshapeOutputVariable = operation.Unpivot.OutputRowVariable
	return nil
}
