package ir

import (
	"fmt"
	"strings"
)

func (validation *physicalOperationValidation) validateGraphReturn(i int, operation PhysicalOperation) error {
	p := validation.plan
	if err := validatePhysicalGraphReturn(*operation.GraphReturn, validation.defined, p.BindVars); err != nil {
		return fmt.Errorf("operation %d graph return: %w", i, err)
	}
	validation.graphReturns++
	return nil
}

func (validation *physicalOperationValidation) validatePopulationMappingReturn(i int, operation PhysicalOperation) error {
	p := validation.plan
	if err := validatePhysicalPopulationMappingReturn(*operation.PopulationMappingReturn, validation.defined, p.BindVars); err != nil {
		return fmt.Errorf("operation %d population mapping return: %w", i, err)
	}
	validation.returns++
	return nil
}

func (validation *physicalOperationValidation) validateCellTraceReturn(i int, operation PhysicalOperation) error {
	p := validation.plan
	if err := validatePhysicalCellTraceReturn(*operation.CellTraceReturn, validation.defined, p.BindVars); err != nil {
		return fmt.Errorf("operation %d cell trace return: %w", i, err)
	}
	validation.returns++
	return nil
}

func (validation *physicalOperationValidation) validateTableShapeExclusionReturn(i int, operation PhysicalOperation) error {
	p := validation.plan
	if err := validatePhysicalTableShapeExclusionReturn(*operation.TableShapeExclusionReturn, validation.defined, p.BindVars); err != nil {
		return fmt.Errorf("operation %d table-shape exclusion return: %w", i, err)
	}
	validation.returns++
	return nil
}

func (validation *physicalOperationValidation) validateReturn(i int, operation PhysicalOperation) error {
	p := validation.plan
	validation.returns++
	seenNames := map[string]bool{}
	for _, projection := range operation.Return.Projections {
		if strings.TrimSpace(projection.Name) == "" || seenNames[projection.Name] {
			return fmt.Errorf("operation %d: return projection name %q is empty or duplicated", i, projection.Name)
		}
		seenNames[projection.Name] = true
		if err := validatePhysicalProjection(projection, validation.defined, p.BindVars); err != nil {
			return fmt.Errorf("operation %d projection %q: %w", i, projection.Name, err)
		}
	}
	return nil
}
