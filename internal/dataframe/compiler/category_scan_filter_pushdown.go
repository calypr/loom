package compiler

import (
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/expression"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

// withCategoryScanSourceFilterPushdown duplicates leading construction
// equality filters onto direct root projections. The stage filters remain in
// place as the semantic checks; this copy lets Arango reject nonmatching root
// documents before materializing the full source projection.
//
// Pushdown stops at the first non-filter stage or any filter shape that is not
// a bound scalar equality over a direct pass-through projection. This keeps
// filters from crossing derived expressions, row reshapes, or other work that
// can change evaluation behavior.
func withCategoryScanSourceFilterPushdown(plan ir.PhysicalPlan) ir.PhysicalPlan {
	sequence := plan.StageSequence
	if sequence == nil || len(sequence.Stages) == 0 || len(plan.Operations) < 2 ||
		plan.Operations[0].Kind != ir.PhysicalRootScanOp || plan.Operations[0].RootScan == nil ||
		plan.Operations[0].RootScan.Population != nil {
		return plan
	}
	insertAt := rootPageInsertionIndex(plan.Operations)
	if insertAt < 1 || insertAt >= len(plan.Operations) {
		return plan
	}

	rootVariable := plan.Operations[0].RootScan.Variable
	lineage, ok := categoryScanSourceProjectionLineage(plan, sequence.SourceColumns, rootVariable)
	if !ok {
		return plan
	}
	filters := make([]ir.PhysicalOperation, 0, len(sequence.Stages))
	priorStageID := sequence.SourceStageID
	for _, stage := range sequence.Stages {
		if stage.Kind != ir.PhysicalStageFilterOp || stage.Filter == nil || len(stage.DerivedLets) != 0 ||
			stage.InputStageID != priorStageID || stage.Filter.Expression != nil {
			break
		}
		predicate := stage.Filter.Predicate
		if predicate.Operator != string(recipe.FilterEquals) || predicate.LeftExpression != nil ||
			predicate.Left.Variable != stage.InputRowVariable || predicate.Left.BindKey != "" ||
			len(predicate.Left.Path) != 1 || predicate.Right == nil || predicate.Right.BindKey == "" ||
			predicate.Right.Variable != "" || len(predicate.Right.Path) != 0 {
			break
		}
		inputColumn, found := categoryScanStageColumnByName(stage.InputColumns, predicate.Left.Path[0])
		if !found || !categoryScanDirectScalarStageColumn(inputColumn) ||
			!categoryScanScalarFilterBind(plan.BindVars[predicate.Right.BindKey]) {
			break
		}
		rootValue, found := lineage[inputColumn.ID]
		if !found || rootValue.Variable != rootVariable || len(rootValue.Path) == 0 || rootValue.BindKey != "" {
			break
		}
		filters = append(filters, ir.PhysicalOperation{
			Kind: ir.PhysicalFilterOp,
			Source: ir.PhysicalSource{
				SemanticNode: plan.Source.SemanticNode,
				ResourceType: plan.Source.ResourceType,
			},
			Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{
				Operator: string(recipe.FilterEquals),
				Left:     rootValue,
				Right:    &ir.PhysicalValue{BindKey: predicate.Right.BindKey},
			}},
		})

		lineage, ok = categoryScanPassThroughFilterLineage(stage, lineage)
		if !ok {
			break
		}
		priorStageID = stage.ID
	}
	if len(filters) == 0 {
		return plan
	}

	out := ir.ClonePhysicalPlan(plan)
	operations := make([]ir.PhysicalOperation, 0, len(out.Operations)+len(filters))
	operations = append(operations, out.Operations[:insertAt]...)
	operations = append(operations, filters...)
	operations = append(operations, out.Operations[insertAt:]...)
	out.Operations = operations
	return out
}

func categoryScanSourceProjectionLineage(plan ir.PhysicalPlan, columns []ir.PhysicalStageColumn, rootVariable string) (map[string]ir.PhysicalValue, bool) {
	if rootVariable == "" {
		return nil, false
	}
	projections := make(map[string]ir.PhysicalProjection, len(columns))
	projectionCounts := make(map[string]int, len(columns))
	for _, operation := range plan.Operations {
		if operation.Kind != ir.PhysicalReturnOp || operation.Return == nil {
			continue
		}
		for _, projection := range operation.Return.Projections {
			projectionCounts[projection.Name]++
			projections[projection.Name] = projection
		}
	}
	lineage := make(map[string]ir.PhysicalValue, len(columns))
	for _, column := range columns {
		projection, found := projections[column.Name]
		if !found || projectionCounts[column.Name] != 1 {
			continue
		}
		value, direct := categoryScanDirectRootProjectionValue(projection, rootVariable)
		if direct {
			lineage[column.ID] = value
		}
	}
	return lineage, len(lineage) > 0
}

func categoryScanDirectRootProjectionValue(projection ir.PhysicalProjection, rootVariable string) (ir.PhysicalValue, bool) {
	if projection.Expression == nil {
		value := projection.Value
		return value, value.Variable == rootVariable && value.BindKey == "" && len(value.Path) > 0
	}
	expression := projection.Expression
	if expression.Kind != ir.PhysicalExtractExpression || expression.Cardinality != ir.PhysicalScalarCardinality ||
		expression.Extract == nil {
		return ir.PhysicalValue{}, false
	}
	extract := expression.Extract
	if extract.ExecutionMode != ir.PhysicalSelectorDirectScalar || extract.Source.Variable != rootVariable ||
		extract.Source.BindKey != "" || len(extract.Source.Path) == 0 || len(extract.Fallbacks) != 0 ||
		extract.Prepared != nil || extract.Distinct || extract.UnitNormalization != nil || extract.Selector.Filter != nil ||
		len(extract.Selector.Steps) == 0 {
		return ir.PhysicalValue{}, false
	}
	path := append([]string(nil), extract.Source.Path...)
	for _, step := range extract.Selector.Steps {
		if step.Field == "" || step.Iterate || step.Index != nil {
			return ir.PhysicalValue{}, false
		}
		path = append(path, step.Field)
	}
	return ir.PhysicalValue{Variable: rootVariable, Path: path}, true
}

func categoryScanPassThroughFilterLineage(stage ir.PhysicalConstructionStage, input map[string]ir.PhysicalValue) (map[string]ir.PhysicalValue, bool) {
	if stage.Kind != ir.PhysicalStageFilterOp || len(stage.OutputProjections) == 0 ||
		len(stage.OutputProjections) != len(stage.OutputColumns) {
		return nil, false
	}
	outputColumns := make(map[string]ir.PhysicalStageColumn, len(stage.OutputColumns))
	for _, column := range stage.OutputColumns {
		if _, duplicate := outputColumns[column.Name]; duplicate {
			return nil, false
		}
		outputColumns[column.Name] = column
	}
	lineage := make(map[string]ir.PhysicalValue, len(stage.OutputColumns))
	for _, projection := range stage.OutputProjections {
		if projection.Expression != nil || projection.Value.Variable != stage.InputRowVariable || projection.Value.BindKey != "" ||
			len(projection.Value.Path) != 1 {
			return nil, false
		}
		inputColumn, found := categoryScanStageColumnByName(stage.InputColumns, projection.Value.Path[0])
		if !found {
			return nil, false
		}
		rootValue, found := input[inputColumn.ID]
		if !found {
			return nil, false
		}
		outputColumn, found := outputColumns[projection.Name]
		if !found {
			return nil, false
		}
		lineage[outputColumn.ID] = rootValue
	}
	return lineage, true
}

func categoryScanStageColumnByName(columns []ir.PhysicalStageColumn, name string) (ir.PhysicalStageColumn, bool) {
	var result ir.PhysicalStageColumn
	for _, column := range columns {
		if column.Name != name {
			continue
		}
		if result.Name != "" {
			return ir.PhysicalStageColumn{}, false
		}
		result = column
	}
	return result, result.Name != ""
}

func categoryScanDirectScalarStageColumn(column ir.PhysicalStageColumn) bool {
	if column.ID == "" || column.Name == "" || column.Internal || column.Identity ||
		(column.Cardinality != string(expression.RequiredOne) && column.Cardinality != string(expression.OptionalOne)) {
		return false
	}
	switch expression.ValueKind(column.Kind) {
	case expression.KindBoolean, expression.KindInteger, expression.KindDecimal, expression.KindString,
		expression.KindDate, expression.KindDateTime, expression.KindCode, expression.KindUUID:
		return true
	default:
		return false
	}
}

func categoryScanScalarFilterBind(value any) bool {
	switch value.(type) {
	case string, bool, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
		return true
	default:
		return false
	}
}
