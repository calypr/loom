package lower

import (
	"fmt"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

func (lowerer *constructionLowerer) lowerDeriveStage(step recipe.ConstructionStep, work *constructionStageLowering) error {
	inputByID := work.inputByID
	outputByID := work.outputByID
	inputRow := work.physical.InputRowVariable
	inputProjections := work.physical.InputProjections
	inputSchema := work.input.schema
	inputIdentity := work.input.identity
	derive := step.Operation.Derive
	column, ok := outputByID[derive.OutputColumnID]
	if !ok {
		return fmt.Errorf("derived output ID %q is missing from step schema", derive.OutputColumnID)
	}
	recipeColumn, err := constructionDerivedColumn(*derive, column, inputByID)
	if err != nil {
		return err
	}
	temporary := ir.PhysicalPlan{BindVars: lowerer.plan.BindVars, Operations: []ir.PhysicalOperation{{Kind: ir.PhysicalReturnOp, Return: &ir.PhysicalReturn{Projections: inputProjections}}}}
	derivedTypes, err := appendRecipeDerivedColumnsWithVariables(&temporary, []recipe.DerivedColumn{recipeColumn}, inputSchema, lowerer.usedVariables)
	if err != nil {
		return err
	}
	derivedProjection, found := findProjection(temporary.Operations, column.Name)
	if !found {
		return fmt.Errorf("derived output %q has no physical projection", column.Name)
	}
	work.physical.Kind = ir.PhysicalStageDeriveOp
	for _, operation := range temporary.Operations {
		if operation.Kind != ir.PhysicalReturnOp {
			work.physical.DerivedLets = append(work.physical.DerivedLets, operation)
		}
	}
	work.schema = make([]CompiledOutputColumn, 0, len(step.Outputs)+1)
	for _, declaration := range step.Outputs {
		if prior, exists := inputByID[declaration.ID]; exists {
			prior.Name, prior.Label = declaration.Name, constructionFirstNonEmpty(declaration.Label, prior.Label, declaration.Name)
			work.schema = append(work.schema, prior)
			continue
		}
		if declaration.ID != derive.OutputColumnID {
			return fmt.Errorf("derive output schema contains unexpected new column ID %q", declaration.ID)
		}
		metadata := derivedTypes[recipeColumn.Name]
		work.schema = append(work.schema, CompiledOutputColumn{
			ID: declaration.ID, Name: declaration.Name, Label: constructionFirstNonEmpty(declaration.Label, declaration.Name),
			SemanticPath: "derived:" + derive.ConstructionID, Kind: string(metadata.Type.Kind),
			Cardinality: string(metadata.Type.Cardinality), Nullable: metadata.Type.Cardinality.Optional(),
			NormalizedUnit: cloneUnitIdentity(metadata.NormalizedUnit),
		})
	}
	work.physical.OutputProjections = stagePassThroughAndDerivedProjections(step.Outputs, inputByID, inputRow, column.ID, column.Name, derivedProjection)
	work.identity = inputIdentity
	return nil
}

func (lowerer *constructionLowerer) lowerFilterStage(step recipe.ConstructionStep, work *constructionStageLowering) error {
	inputByID := work.inputByID
	inputRow := work.physical.InputRowVariable
	inputIdentity := work.input.identity
	filter := step.Operation.Filter
	inputColumn, ok := inputByID[filter.ColumnID]
	if !ok || inputColumn.Internal {
		return fmt.Errorf("filter column ID %q is not a public input", filter.ColumnID)
	}
	physicalFilter, err := constructionPhysicalFilter(lowerer.plan, *filter, inputRow, inputColumn)
	if err != nil {
		return err
	}
	work.physical.Kind, work.physical.Filter = ir.PhysicalStageFilterOp, &physicalFilter
	work.schema = make([]CompiledOutputColumn, 0, len(step.Outputs)+1)
	for _, declaration := range step.Outputs {
		prior, exists := inputByID[declaration.ID]
		if !exists || prior.Internal {
			return fmt.Errorf("filter output ID %q is not a public input column", declaration.ID)
		}
		prior.Name, prior.Label = declaration.Name, constructionFirstNonEmpty(declaration.Label, prior.Label, declaration.Name)
		work.schema = append(work.schema, prior)
	}
	work.physical.OutputProjections = stagePassThroughProjections(step.Outputs, inputByID, inputRow)
	work.identity = inputIdentity
	return nil
}
