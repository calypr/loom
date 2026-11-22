package lower

import (
	"fmt"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/expression"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/spec"
)

func (lowerer *constructionLowerer) lowerPivotStage(step recipe.ConstructionStep, work *constructionStageLowering) error {
	inputByID := work.inputByID
	outputByID := work.outputByID
	inputProjections := work.physical.InputProjections
	publicInput := work.publicInput
	inputSchema := work.input.schema
	rootResourceType := work.input.rootResourceType
	index := work.input.index
	rootContributorProvenance := work.input.rootContributorProvenance
	retainRootContributors := work.input.retainRootContributors
	pivot := step.Operation.Pivot
	semanticPivot, outputNames, err := constructionSemanticPivot(*pivot, inputByID, outputByID)
	if err != nil {
		return err
	}
	nameSchema, nameProjections := compiledSchemaByName(publicInput), stageProjectionMap(inputProjections)
	physicalPivot, projections, compiled, err := lowerRecipeGroupedPivot(lowerer.plan, semanticPivot, inputProjections, nameSchema, nameProjections, lowerer.usedVariables)
	if err != nil {
		return err
	}
	for index := range physicalPivot.GroupKeys {
		outputName := outputNames[physicalPivot.GroupKeys[index].Column]
		physicalPivot.GroupKeys[index].Output = outputName
	}
	for index := range physicalPivot.Categories {
		physicalPivot.Categories[index].Output = outputNames[physicalPivot.Categories[index].Output]
	}
	rowValuesIR, rowValueColumns, err := lowerConstructionRowValues(
		step.RowValues, inputByID, outputByID, pivot.ConstructionID, lowerer.usedVariables, index,
	)
	if err != nil {
		return err
	}
	physicalPivot.RowValues = rowValuesIR
	if retainRootContributors && rootContributorProvenance {
		contributor, ok := rootContributorInput(inputSchema, rootResourceType)
		if !ok {
			return fmt.Errorf("Pivot cannot retain root contributors because the compiler-proven root identity was lost")
		}
		physicalPivot.RootContributorInputColumn = contributor.Name
		physicalPivot.RootContributorInputMany = contributor.Cardinality == string(expression.Many)
		physicalPivot.RootContributorOutputColumn = rootContributorSetColumn
		physicalPivot.RootContributorVariable = allocateConstructionVariable(lowerer.usedVariables, "pivot_root_contributors", index)
		compiled = append(compiled, CompiledOutputColumn{
			ID: rootContributorSetColumn, Name: rootContributorSetColumn, Label: rootContributorSetColumn,
			SemanticPath: "construction_root_contributors:" + pivot.ConstructionID,
			Kind:         string(expression.KindString), Cardinality: string(expression.Many), Internal: true,
			RootContributorResourceType: rootResourceType,
		})
		identityProjection := projections[len(projections)-1]
		projections[len(projections)-1] = ir.PhysicalProjection{Name: rootContributorSetColumn, Hidden: true, Value: ir.PhysicalValue{Variable: physicalPivot.RootContributorVariable}}
		projections = append(projections, identityProjection)
	}
	for _, column := range rowValueColumns {
		outputNames[column.Name] = column.Name
		compiled = append(compiled, column)
		projections = append(projections, ir.PhysicalProjection{
			Name: column.Name, Value: ir.PhysicalValue{Variable: physicalPivot.OutputRowVariable, Path: []string{column.Name}},
		})
	}
	work.physical.Kind, work.physical.GroupedPivot = ir.PhysicalStagePivotOp, &physicalPivot
	work.physical.OutputRowVariable = physicalPivot.OutputRowVariable
	work.physical.OutputProjections = projections
	work.schema, err = reconcileConstructionShapeSchema(step.Outputs, outputNames, compiled, inputByID)
	if err != nil {
		return err
	}
	if physicalPivot.RootContributorOutputColumn != "" {
		work.schema = append(work.schema, CompiledOutputColumn{
			ID: rootContributorSetColumn, Name: rootContributorSetColumn, Label: rootContributorSetColumn,
			SemanticPath: "construction_root_contributors:" + pivot.ConstructionID,
			Kind:         string(expression.KindString), Cardinality: string(expression.Many), Internal: true,
			RootContributorResourceType: rootResourceType,
		})
	}
	if physicalPivot.UnlistedEvidenceColumn != "" {
		for _, column := range compiled {
			if column.Name == physicalPivot.UnlistedEvidenceColumn {
				column.ID = column.Name
				work.schema = append(work.schema, column)
			}
		}
	}
	work.identity = constructionRowID
	return nil
}

func (lowerer *constructionLowerer) lowerCodedPivotStage(step recipe.ConstructionStep, work *constructionStageLowering) error {
	inputByID := work.inputByID
	inputRow := work.physical.InputRowVariable
	inputIdentity := work.input.identity
	rootResourceType := work.input.rootResourceType
	index := work.input.index
	codedPivot := step.Operation.CodedPivot
	if codedPivot == nil {
		return fmt.Errorf("coded Pivot payload is required")
	}
	physicalPivot, projections, compiled, err := lowerConstructionCodedPivot(
		lowerer.plan, *codedPivot, step.RowValues, step.Outputs, inputByID, inputIdentity, rootResourceType, inputRow, lowerer.usedVariables, index,
	)
	if err != nil {
		return err
	}
	work.physical.Kind, work.physical.GroupedPivot = ir.PhysicalStagePivotOp, &physicalPivot
	work.physical.OutputRowVariable, work.physical.OutputProjections = physicalPivot.OutputRowVariable, projections
	work.schema, err = reconcileConstructionCodedPivotSchema(step.Outputs, compiled, inputByID)
	if err != nil {
		return err
	}
	rootIdentity := inputByID[inputIdentity]
	rootIdentity.Identity = false
	rootIdentity.Internal = true
	work.schema = append([]CompiledOutputColumn{rootIdentity}, work.schema...)
	work.identity = constructionRowID
	return nil
}

func (lowerer *constructionLowerer) lowerUnpivotStage(step recipe.ConstructionStep, work *constructionStageLowering) error {
	inputByID := work.inputByID
	outputByID := work.outputByID
	inputProjections := work.physical.InputProjections
	inputSchema := work.input.schema
	inputIdentity := work.input.identity
	unpivot := step.Operation.Unpivot
	semanticUnpivot, err := constructionSemanticUnpivot(*unpivot, inputByID, outputByID)
	if err != nil {
		return err
	}
	nameProjections := stageProjectionMap(inputProjections)
	identity := spec.RowIdentity{Grain: spec.RowGrainPatient, Fields: []string{inputIdentity}}
	physicalUnpivot, projections, compiled, err := lowerRecipeUnpivot(lowerer.plan, semanticUnpivot, &identity, inputProjections, compiledSchemaByName(inputSchema), nameProjections, lowerer.usedVariables)
	if err != nil {
		return err
	}
	physicalUnpivot.PreservedOutputs = constructionUnpivotPreservedOutputs(*unpivot, step.Outputs, inputByID)
	work.physical.Kind, work.physical.Unpivot = ir.PhysicalStageUnpivotOp, &physicalUnpivot
	work.physical.OutputRowVariable = physicalUnpivot.OutputRowVariable
	work.physical.OutputProjections = projections
	work.schema, err = reconcileConstructionUnpivotSchema(step.Outputs, compiled, inputByID, *unpivot)
	if err != nil {
		return err
	}
	work.identity = constructionRowID
	return nil
}

func (lowerer *constructionLowerer) lowerExpandStage(step recipe.ConstructionStep, work *constructionStageLowering) error {
	inputByID := work.inputByID
	outputByID := work.outputByID
	inputRow := work.physical.InputRowVariable
	index := work.input.index
	expand := step.Operation.Expand
	physicalExpand, projections, compiled, err := lowerConstructionExpand(lowerer.plan, *expand, step.Outputs, inputByID, outputByID, inputRow, lowerer.usedVariables, index)
	if err != nil {
		return err
	}
	work.physical.Kind, work.physical.Expand = ir.PhysicalStageExpandOp, &physicalExpand
	work.physical.OutputProjections = projections
	work.schema, work.identity = compiled, constructionRowID
	return nil
}
