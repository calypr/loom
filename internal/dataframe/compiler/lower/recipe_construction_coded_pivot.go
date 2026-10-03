package lower

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/expression"
	"github.com/calypr/loom/internal/dataframe/recipe"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

func lowerConstructionCodedPivot(
	plan *ir.PhysicalPlan,
	pivot recipe.ConstructionCodedPivot,
	rowValues []recipe.ConstructionRowValue,
	declarations []recipe.StageColumn,
	input map[string]CompiledOutputColumn,
	inputIdentity, rootResourceType, inputRow string,
	usedVariables map[string]bool,
	stepIndex int,
) (ir.PhysicalGroupedPivot, []ir.PhysicalProjection, []CompiledOutputColumn, error) {
	if inputIdentity != "_key" || pivot.Source.ResourceType != rootResourceType || len(pivot.Source.Route) != 0 {
		return ir.PhysicalGroupedPivot{}, nil, nil, fmt.Errorf("CODED_PIVOT requires the direct root resource identified by _key")
	}
	if err := constructionDirectRootRowsUnique(plan, "CODED_PIVOT"); err != nil {
		return ir.PhysicalGroupedPivot{}, nil, nil, err
	}
	if plan.Source.ResourceType != rootResourceType || len(plan.Operations) == 0 ||
		plan.Operations[0].Kind != ir.PhysicalRootScanOp || plan.Operations[0].RootScan == nil {
		return ir.PhysicalGroupedPivot{}, nil, nil, fmt.Errorf("CODED_PIVOT requires a direct root resource scan")
	}
	rootScan := plan.Operations[0].RootScan
	if rootScan.CollectionBindKey == "" || plan.BindVars[rootScan.CollectionBindKey] != rootResourceType {
		return ir.PhysicalGroupedPivot{}, nil, nil, fmt.Errorf("CODED_PIVOT root collection does not match selected root resource %q", rootResourceType)
	}
	if len(pivot.Categories) == 0 || len(pivot.Categories) > 50 {
		return ir.PhysicalGroupedPivot{}, nil, nil, fmt.Errorf("CODED_PIVOT requires 1..50 categories")
	}

	valueType, ok := tableReshapeScalarKind(pivot.Source.LogicalType)
	if !ok {
		return ir.PhysicalGroupedPivot{}, nil, nil, fmt.Errorf("CODED_PIVOT source logical type %q is not a supported scalar", pivot.Source.LogicalType)
	}
	if pivot.DuplicatePolicy != recipe.PivotDuplicateError && valueType != "INTEGER" && valueType != "DECIMAL" {
		return ir.PhysicalGroupedPivot{}, nil, nil, &PivotReducerTypeError{Policy: pivot.DuplicatePolicy, ValueColumn: pivot.Source.FieldPath}
	}

	outputs := make(map[string]recipe.StageColumn, len(declarations))
	for _, declaration := range declarations {
		outputs[declaration.ID] = declaration
	}
	constructionBind := nextTableReshapeBindKey(plan.BindVars, "reshape_construction")
	plan.BindVars[constructionBind] = pivot.ConstructionID

	correlatedBinding := fhirschema.CorrelatedBinding{
		OwnerPath: pivot.Source.OwningScope, KeyPath: pivot.Source.KeyPath,
		SystemPath: "system", CodePath: "code", ValuePath: pivot.Source.ValuePath,
		ChoiceArms: append([]string(nil), pivot.Source.ChoiceArms...), LogicalType: pivot.Source.LogicalType,
	}
	physical := ir.PhysicalGroupedPivot{
		ConstructionID:      pivot.ConstructionID,
		InputRowVariable:    allocateConstructionVariable(usedVariables, "coded_pivot_input", stepIndex),
		GroupRowsVariable:   allocateConstructionVariable(usedVariables, "coded_pivot_group_rows", stepIndex),
		OutputRowVariable:   allocateConstructionVariable(usedVariables, "coded_pivot_output", stepIndex),
		OneInputRowPerGroup: true,
		InputProjections: []ir.PhysicalProjection{
			{Name: "_key", Hidden: true, Value: ir.PhysicalValue{Variable: rootScan.Variable, Path: []string{"_key"}}},
			{Name: codedPivotSourcePayloadColumn, Hidden: true, Value: ir.PhysicalValue{Variable: rootScan.Variable, Path: []string{"payload"}}},
		},
		GroupKeys: []ir.PhysicalGroupedPivotKey{{
			Column: "_key", Output: "_key", Variable: allocateConstructionVariable(usedVariables, "coded_pivot_root_key", stepIndex),
			Kind: "STRING", Hidden: true,
		}},
		ValueType: valueType, ConstructionIDBindKey: constructionBind,
		DuplicatePolicy: string(pivot.DuplicatePolicy), MissingCellPolicy: string(pivot.MissingCellPolicy),
		UnlistedCategoryPolicy: "IGNORE", CodedSourceVariable: rootScan.Variable,
	}
	physicalRowValues, rowValueColumns, err := lowerConstructionRowValues(
		rowValues, input, outputs, pivot.ConstructionID, usedVariables, stepIndex,
	)
	if err != nil {
		return ir.PhysicalGroupedPivot{}, nil, nil, err
	}
	physical.RowValues = physicalRowValues
	rowValueProjections, err := constructionSourceRowValueProjections(plan, physical.RowValues)
	if err != nil {
		return ir.PhysicalGroupedPivot{}, nil, nil, err
	}
	physical.InputProjections = append(physical.InputProjections, rowValueProjections...)

	seenPairs, seenOutputs := map[string]bool{}, map[string]bool{}
	compiled := make([]CompiledOutputColumn, 0, len(pivot.Categories)+len(rowValueColumns))
	projections := make([]ir.PhysicalProjection, 0, len(pivot.Categories)+len(rowValueColumns)+2)
	for index, category := range pivot.Categories {
		declaration, found := outputs[category.OutputColumnID]
		if !found || strings.TrimSpace(category.System) == "" || strings.TrimSpace(category.Code) == "" {
			return ir.PhysicalGroupedPivot{}, nil, nil, fmt.Errorf("CODED_PIVOT category %d has no durable key or declared output", index)
		}
		pair := category.System + "\x00" + category.Code
		if seenPairs[pair] || seenOutputs[declaration.Name] {
			return ir.PhysicalGroupedPivot{}, nil, nil, fmt.Errorf("CODED_PIVOT categories contain duplicate keys or outputs")
		}
		seenPairs[pair], seenOutputs[declaration.Name] = true, true
		systemBind := nextTableReshapeBindKey(plan.BindVars, fmt.Sprintf("coded_pivot_system_%d", index))
		codeBind := nextTableReshapeBindKey(plan.BindVars, fmt.Sprintf("coded_pivot_code_%d", index))
		plan.BindVars[systemBind], plan.BindVars[codeBind] = category.System, category.Code
		if physical.CodedCorrelation == nil {
			correlation, err := LowerCorrelatedBinding(rootResourceType, correlatedBinding,
				ir.PhysicalValue{Variable: physical.InputRowVariable, Path: []string{codedPivotSourcePayloadColumn}}, systemBind, codeBind)
			if err != nil {
				return ir.PhysicalGroupedPivot{}, nil, nil, fmt.Errorf("CODED_PIVOT correlated source: %w", err)
			}
			physical.CodedCorrelation = &correlation
		}
		physical.CodedCategories = append(physical.CodedCategories, ir.PhysicalGroupedCodedPivotCategory{
			Output: declaration.Name, SystemBindKey: systemBind, CodeBindKey: codeBind,
		})
		nullable := pivot.MissingCellPolicy == recipe.PivotMissingCellNull
		cardinality := string(expression.RequiredOne)
		if nullable {
			cardinality = string(expression.OptionalOne)
		}
		compiled = append(compiled, CompiledOutputColumn{
			ID: declaration.ID, Name: declaration.Name, Label: constructionFirstNonEmpty(declaration.Label, declaration.Name),
			SemanticPath: "construction_coded_pivot:" + pivot.ConstructionID + ":" + pivot.Source.BindingID + ":" + category.System + ":" + category.Code,
			Kind:         pivot.Source.LogicalType, Cardinality: cardinality, Nullable: nullable,
		})
		projections = append(projections, ir.PhysicalProjection{
			Name: declaration.Name, Value: ir.PhysicalValue{Variable: physical.OutputRowVariable, Path: []string{declaration.Name}},
		})
	}
	for _, column := range rowValueColumns {
		compiled = append(compiled, column)
		projections = append(projections, ir.PhysicalProjection{
			Name: column.Name, Value: ir.PhysicalValue{Variable: physical.OutputRowVariable, Path: []string{column.Name}},
		})
	}
	projections = append(projections,
		ir.PhysicalProjection{Name: "_key", Hidden: true, Value: ir.PhysicalValue{Variable: physical.OutputRowVariable, Path: []string{"_key"}}},
		ir.PhysicalProjection{Name: constructionRowID, Hidden: true, Value: ir.PhysicalValue{Variable: physical.OutputRowVariable, Path: []string{constructionRowID}}},
	)
	return physical, projections, compiled, nil
}

func reconcileConstructionCodedPivotSchema(declarations []recipe.StageColumn, computed []CompiledOutputColumn, input map[string]CompiledOutputColumn) ([]CompiledOutputColumn, error) {
	computedByName := make(map[string]CompiledOutputColumn, len(computed))
	for _, column := range computed {
		computedByName[column.Name] = column
	}
	result := make([]CompiledOutputColumn, 0, len(declarations))
	for _, declaration := range declarations {
		column, ok := computedByName[declaration.Name]
		if !ok {
			return nil, fmt.Errorf("CODED_PIVOT output ID %q name %q has no physical category", declaration.ID, declaration.Name)
		}
		if declaration.Type != "" && declaration.Type != "INFER" && declaration.Type != column.Kind &&
			!(declaration.Type == "array" && column.Cardinality == string(expression.Many)) {
			return nil, fmt.Errorf("CODED_PIVOT output %q type %q does not match source type %q", declaration.ID, declaration.Type, column.Kind)
		}
		if _, exists := input[declaration.ID]; exists {
			return nil, fmt.Errorf("CODED_PIVOT output ID %q collides with its input", declaration.ID)
		}
		column.ID = declaration.ID
		column.Label = constructionFirstNonEmpty(declaration.Label, column.Label, declaration.Name)
		result = append(result, column)
	}
	return result, nil
}

func constructionDirectRootRowsUnique(plan *ir.PhysicalPlan, operation string) error {
	if plan == nil || len(plan.Operations) < 2 || plan.Operations[0].Kind != ir.PhysicalRootScanOp || plan.Operations[0].RootScan == nil {
		return fmt.Errorf("%s requires a direct root scan with one row per root identity", operation)
	}
	returns := 0
	for index, item := range plan.Operations {
		switch item.Kind {
		case ir.PhysicalRootScanOp:
			if index != 0 || item.RootScan == nil {
				return fmt.Errorf("%s source must contain exactly one direct root scan", operation)
			}
		case ir.PhysicalFilterOp, ir.PhysicalDerivedLetOp, ir.PhysicalExpressionLetOp,
			ir.PhysicalSetOp, ir.PhysicalSortOp, ir.PhysicalLimitOp:
			// These operations can remove or annotate rows but cannot add roots.
		case ir.PhysicalReturnOp:
			returns++
			if item.Return == nil || index != len(plan.Operations)-1 {
				return fmt.Errorf("%s source must have one terminal root projection", operation)
			}
		default:
			return fmt.Errorf("%s cannot preserve one row per root after source operation %q", operation, item.Kind)
		}
	}
	if returns != 1 {
		return fmt.Errorf("%s source must have one terminal root projection", operation)
	}
	return nil
}
