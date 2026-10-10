package compiler

import (
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

// withConstructionPreviewRootIDFilter adds an indexed root-id equality for a
// first-stage construction filter over the exact root.payload.id projection.
// The construction filter remains in the stage sequence as the value-level
// check. Loom ingestion stores the same unmodified FHIR id in the document id
// and payload.id fields, so this root predicate can safely narrow the scan.
func withConstructionPreviewRootIDFilter(output lower.CompiledRecipeOutput, plan ir.PhysicalPlan) ir.PhysicalPlan {
	filter, ok := constructionPreviewRootIDFilter(output, plan)
	if !ok {
		return plan
	}

	out := clonePhysicalPlan(plan)
	insertAt := rootPageInsertionIndex(out.Operations)
	if insertAt < 1 || insertAt >= len(out.Operations) {
		return plan
	}
	operations := make([]ir.PhysicalOperation, 0, len(out.Operations)+1)
	operations = append(operations, out.Operations[:insertAt]...)
	operations = append(operations, filter)
	operations = append(operations, out.Operations[insertAt:]...)
	out.Operations = operations
	return out
}

func constructionPreviewRootIDFilter(output lower.CompiledRecipeOutput, plan ir.PhysicalPlan) (ir.PhysicalOperation, bool) {
	sequence := plan.StageSequence
	if sequence == nil || sequence.SourceStageID == "" || len(sequence.Stages) == 0 ||
		plan.Source.ResourceType != output.RootResourceType || len(plan.Operations) < 2 ||
		plan.Operations[0].Kind != ir.PhysicalRootScanOp || plan.Operations[0].RootScan == nil ||
		plan.Operations[0].Source.ResourceType != output.RootResourceType {
		return ir.PhysicalOperation{}, false
	}

	stage := sequence.Stages[0]
	if stage.Kind != ir.PhysicalStageFilterOp || stage.Filter == nil || len(stage.DerivedLets) != 0 ||
		stage.InputStageID != sequence.SourceStageID {
		return ir.PhysicalOperation{}, false
	}
	predicate := stage.Filter.Predicate
	if stage.Filter.Expression != nil || predicate.Operator != string(recipe.FilterEquals) ||
		predicate.LeftExpression != nil || predicate.Left.Variable != stage.InputRowVariable ||
		predicate.Left.BindKey != "" || len(predicate.Left.Path) != 1 || predicate.Right == nil ||
		predicate.Right.BindKey == "" || predicate.Right.Variable != "" || len(predicate.Right.Path) != 0 {
		return ir.PhysicalOperation{}, false
	}
	value, exists := plan.BindVars[predicate.Right.BindKey]
	if !exists {
		return ir.PhysicalOperation{}, false
	}
	id, ok := value.(string)
	if !ok || strings.TrimSpace(id) == "" {
		return ir.PhysicalOperation{}, false
	}

	inputName := predicate.Left.Path[0]
	var inputColumn ir.PhysicalStageColumn
	for _, column := range stage.InputColumns {
		if column.Name == inputName {
			if inputColumn.Name != "" {
				return ir.PhysicalOperation{}, false
			}
			inputColumn = column
		}
	}
	if inputColumn.Name == "" || inputColumn.Internal || inputColumn.Kind != "string" {
		return ir.PhysicalOperation{}, false
	}
	if !constructionPreviewSourceColumnIsRootID(output, sequence.SourceStageID, inputColumn) {
		return ir.PhysicalOperation{}, false
	}

	root := plan.Operations[0].RootScan.Variable
	if !constructionPreviewProjectionIsRootID(plan, inputName, output.RootResourceType, root) {
		return ir.PhysicalOperation{}, false
	}
	return ir.PhysicalOperation{
		Kind: ir.PhysicalFilterOp,
		Source: ir.PhysicalSource{
			SemanticNode:  plan.Source.SemanticNode,
			ResourceType:  output.RootResourceType,
			SemanticField: "id",
		},
		Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{
			Operator: string(recipe.FilterEquals),
			Left:     ir.PhysicalValue{Variable: root, Path: []string{"id"}},
			Right:    &ir.PhysicalValue{BindKey: predicate.Right.BindKey},
		}},
	}, true
}

func constructionPreviewSourceColumnIsRootID(output lower.CompiledRecipeOutput, sourceStageID string, input ir.PhysicalStageColumn) bool {
	for _, stage := range output.Stages {
		if stage.ID != sourceStageID || stage.Operation != "SOURCE_PROJECTION" {
			continue
		}
		for _, column := range stage.Columns {
			if column.ID == input.ID && column.Name == input.Name && !column.Internal && column.Kind == "string" {
				return true
			}
		}
	}
	return false
}

func constructionPreviewProjectionIsRootID(plan ir.PhysicalPlan, columnName, rootResourceType, root string) bool {
	found := false
	for _, operation := range plan.Operations {
		if operation.Kind != ir.PhysicalReturnOp || operation.Return == nil {
			continue
		}
		for _, projection := range operation.Return.Projections {
			if projection.Name != columnName {
				continue
			}
			if found {
				return false
			}
			if projection.Expression == nil {
				if projection.Value.Variable != root || len(projection.Value.Path) != 2 ||
					projection.Value.Path[0] != "payload" || projection.Value.Path[1] != "id" {
					return false
				}
			} else {
				physical := projection.Expression
				if physical.Kind != ir.PhysicalExtractExpression ||
					physical.Cardinality != ir.PhysicalScalarCardinality || physical.Extract == nil {
					return false
				}
				extract := physical.Extract
				if extract.ResourceType != rootResourceType || extract.Source.Variable != root ||
					len(extract.Source.Path) != 1 || extract.Source.Path[0] != "payload" ||
					extract.Prepared != nil || extract.Distinct || len(extract.Fallbacks) != 0 ||
					len(extract.Selector.Steps) != 1 || extract.Selector.Filter != nil {
					return false
				}
				step := extract.Selector.Steps[0]
				if step.Field != "id" || step.Iterate || step.Index != nil {
					return false
				}
			}
			found = true
		}
	}
	return found
}
