package ir

import (
	"fmt"
)

type physicalOperationValidation struct {
	plan                   PhysicalPlan
	defined                map[string]bool
	rootScans              int
	returns                int
	graphReturns           int
	groupRows              int
	tableReshapes          int
	reshapeOutputVariable  string
	reshapeWindowSortSeen  bool
	reshapeWindowLimitSeen bool
	reshapeWindowClosed    bool
}

func (p PhysicalPlan) validateOperations() error {
	validation := physicalOperationValidation{plan: p, defined: make(map[string]bool)}
	reshapeIndex := -1
	for index, operation := range p.Operations {
		if operation.Kind == PhysicalGroupedPivotOp || operation.Kind == PhysicalUnpivotOp {
			reshapeIndex = index
			break
		}
	}
	if reshapeIndex >= 0 {
		for index := 0; index < reshapeIndex; index++ {
			if p.Operations[index].Kind == PhysicalLimitOp {
				return fmt.Errorf("operation %d: execution LIMIT cannot precede a terminal table reshape", index)
			}
		}
	}
	for index, operation := range p.Operations {
		if err := validation.validate(index, operation); err != nil {
			return err
		}
	}
	if validation.groupRows == 1 {
		if validation.rootScans != 0 || len(p.Operations) != 1 {
			return fmt.Errorf("grouped physical plan cannot contain a root scan or other operations")
		}
	} else if validation.rootScans != 1 {
		return fmt.Errorf("physical plan requires exactly one root scan")
	}
	if validation.groupRows > 0 && validation.tableReshapes > 0 {
		return fmt.Errorf("group rows cannot be combined with a table reshape")
	}
	if validation.returns+validation.graphReturns != 1 {
		return fmt.Errorf("physical plan requires exactly one RETURN, GRAPH_RETURN, POPULATION_MAPPING_RETURN, CELL_TRACE_RETURN, or TABLE_SHAPE_EXCLUSION_RETURN")
	}
	return nil
}

func (validation *physicalOperationValidation) validate(i int, operation PhysicalOperation) error {
	if validation.returns+validation.graphReturns > 0 {
		return fmt.Errorf("operation %d appears after RETURN", i)
	}
	if validation.tableReshapes > 0 {
		if validation.reshapeWindowSortSeen && !validation.reshapeWindowLimitSeen && operation.Kind != PhysicalLimitOp {
			validation.reshapeWindowClosed = true
		}
		switch operation.Kind {
		case PhysicalExpressionLetOp, PhysicalReturnOp, PhysicalCellTraceReturnOp:
		case PhysicalSortOp:
			if validation.reshapeWindowSortSeen || validation.reshapeWindowLimitSeen || validation.reshapeWindowClosed {
				return fmt.Errorf("operation %d: reshaped execution window can contain only one SORT", i)
			}
			if operation.Sort == nil || len(operation.Sort.Keys) != 1 {
				return fmt.Errorf("operation %d: reshaped execution SORT must use the stable row identity", i)
			}
			key := operation.Sort.Keys[0]
			if key.Variable != validation.reshapeOutputVariable || key.BindKey != "" || len(key.Path) != 1 || key.Path[0] != "__loom_row_id" {
				return fmt.Errorf("operation %d: reshaped execution SORT must use the stable row identity", i)
			}
			validation.reshapeWindowSortSeen = true
		case PhysicalLimitOp:
			if !validation.reshapeWindowSortSeen || validation.reshapeWindowLimitSeen || validation.reshapeWindowClosed {
				return fmt.Errorf("operation %d: reshaped execution LIMIT requires an immediately preceding stable row SORT", i)
			}
			validation.reshapeWindowLimitSeen = true
		default:
			return fmt.Errorf("operation %d (%s) appears after terminal table reshape", i, operation.Kind)
		}
	}
	if err := operation.validatePayload(); err != nil {
		return fmt.Errorf("operation %d (%s): %w", i, operation.Kind, err)
	}
	if operation.Kind == PhysicalKeySetLookupOp {
		return fmt.Errorf("operation %d: KEY_SET_LOOKUP is only legal inside a typed subplan", i)
	}
	switch operation.Kind {
	case PhysicalRootScanOp:
		return validation.validateRootScan(i, operation)
	case PhysicalTraversalOp:
		return validation.validateTraversal(i, operation)
	case PhysicalFilterOp:
		return validation.validateFilter(i, operation)
	case PhysicalDerivedLetOp:
		return validation.validateDerivedLet(i, operation)
	case PhysicalExpressionLetOp:
		return validation.validateExpressionLet(i, operation)
	case PhysicalSetOp:
		return validation.validateSet(i, operation)
	case PhysicalUnnestOp:
		return validation.validateUnnest(i, operation)
	case PhysicalSortOp:
		return validation.validateSort(i, operation)
	case PhysicalLimitOp:
		return validation.validateLimit(i, operation)
	case PhysicalPathSeedOp:
		return validation.validatePathSeed(i, operation)
	case PhysicalPathExtendOp:
		return validation.validatePathExtend(i, operation)
	case PhysicalGraphReturnOp:
		return validation.validateGraphReturn(i, operation)
	case PhysicalPopulationMappingReturnOp:
		return validation.validatePopulationMappingReturn(i, operation)
	case PhysicalCellTraceReturnOp:
		return validation.validateCellTraceReturn(i, operation)
	case PhysicalTableShapeExclusionReturnOp:
		return validation.validateTableShapeExclusionReturn(i, operation)
	case PhysicalGroupRowsOp:
		return validation.validateGroupRows(i, operation)
	case PhysicalGroupedPivotOp:
		return validation.validateGroupedPivot(i, operation)
	case PhysicalUnpivotOp:
		return validation.validateUnpivot(i, operation)
	case PhysicalReturnOp:
		return validation.validateReturn(i, operation)
	}
	return nil
}
