package lower

import (
	"fmt"

	loomtext "github.com/calypr/loom/internal/foundation/text"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/expression"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/spec"
)

func (lowerer *constructionLowerer) lowerGroupStage(step recipe.ConstructionStep, work *constructionStageLowering) error {
	inputByID := work.inputByID
	outputByID := work.outputByID
	inputSchema := work.input.schema
	rootResourceType := work.input.rootResourceType
	index := work.input.index
	rootContributorProvenance := work.input.rootContributorProvenance
	retainRootContributors := work.input.retainRootContributors
	group := step.Operation.Group
	physicalGroup, projections, compiled, err := lowerer.lowerConstructionGroup(
		*group, step.RowValues, step.Outputs, inputByID, outputByID, &work.physical, work.loweringContext(),
	)
	if err != nil {
		return err
	}
	if retainRootContributors && rootContributorProvenance {
		contributor, ok := rootContributorInput(inputSchema, rootResourceType)
		if !ok {
			return fmt.Errorf("Group cannot retain root contributors because the compiler-proven root identity was lost")
		}
		physicalGroup.RootContributorInputColumn = contributor.Name
		physicalGroup.RootContributorInputMany = contributor.Cardinality == string(expression.Many)
		physicalGroup.RootContributorOutputColumn = rootContributorSetColumn
		physicalGroup.RootContributorVariable = allocateConstructionVariable(lowerer.usedVariables, "group_root_contributors", index)
		compiled = append(compiled, CompiledOutputColumn{
			ID: rootContributorSetColumn, Name: rootContributorSetColumn, Label: rootContributorSetColumn,
			SemanticPath: "construction_root_contributors:" + group.ConstructionID,
			Kind:         string(expression.KindString), Cardinality: string(expression.Many), Internal: true,
			RootContributorResourceType: rootResourceType,
		})
		identityProjection := projections[len(projections)-1]
		projections[len(projections)-1] = ir.PhysicalProjection{Name: rootContributorSetColumn, Hidden: true, Value: ir.PhysicalValue{Variable: physicalGroup.RootContributorVariable}}
		projections = append(projections, identityProjection)
	}
	if inventory, ok := schemaColumn(inputSchema, "__loom_dynamic_runtime_keys"); ok && inventory.Internal {
		physicalGroup.DynamicRuntimeKeysVariable = allocateConstructionVariable(lowerer.usedVariables, "group_dynamic_keys", index)
		inventory.Cardinality = string(expression.Many)
		compiled = append(compiled, inventory)
		identityProjection := projections[len(projections)-1]
		projections[len(projections)-1] = ir.PhysicalProjection{
			Name: inventory.Name, Hidden: true, Value: ir.PhysicalValue{Variable: physicalGroup.DynamicRuntimeKeysVariable},
		}
		projections = append(projections, identityProjection)
	}
	work.physical.Kind, work.physical.Group = ir.PhysicalStageGroupOp, &physicalGroup
	work.physical.OutputProjections = projections
	work.schema, work.identity = compiled, constructionRowID
	return nil
}

func (lowerer *constructionLowerer) lowerCodedGroupStage(step recipe.ConstructionStep, work *constructionStageLowering) error {
	inputByID := work.inputByID
	inputIdentity := work.input.identity
	rootResourceType := work.input.rootResourceType
	index := work.input.index
	codedGroup := step.Operation.CodedGroup
	if codedGroup == nil {
		return fmt.Errorf("coded group payload is required")
	}
	physicalCodedGroup, projections, compiled, err := lowerConstructionCodedGroup(
		lowerer.plan, *codedGroup, step.RowValues, step.Outputs, inputByID, inputIdentity, rootResourceType, lowerer.usedVariables, index,
	)
	if err != nil {
		return err
	}
	work.physical.Kind, work.physical.CodedGroup, work.physical.OutputProjections = ir.PhysicalStageCodedGroupOp, &physicalCodedGroup, projections
	work.schema, work.identity = compiled, constructionRowID
	return nil
}

func (lowerer *constructionLowerer) lowerRelatedSourceStage(step recipe.ConstructionStep, work *constructionStageLowering) error {
	inputByID := work.inputByID
	outputByID := work.outputByID
	inputSchema := work.input.schema
	inputIdentity := work.input.identity
	rootResourceType := work.input.rootResourceType
	related := step.Operation.RelatedSource
	if related == nil {
		return fmt.Errorf("related source payload is required")
	}
	rowValuePolicy := related.RowValuePolicy
	if rowValuePolicy == "" {
		rowValuePolicy = recipe.ConstructionRowValueAll
	}
	projections, compiled, err := lowerer.lowerConstructionRelatedSource(step, *related, inputByID, outputByID, work.loweringContext())
	if err != nil {
		return err
	}
	work.physical.Kind, work.physical.OutputProjections = ir.PhysicalStageRelatedSourceOp, projections
	rootContributor, _ := rootContributorInput(inputSchema, rootResourceType)
	rootContributorColumn := rootContributor.Name
	if related.AnchorColumnID == rootContributorColumn && related.AnchorColumnID == inputIdentity {
		rootContributorColumn = ""
	}
	work.physical.RelatedSource = &ir.PhysicalStageRelatedSource{
		AnchorColumnID: related.AnchorColumnID, RootContributorColumn: rootContributorColumn,
		RootResourceType: rootResourceType, OutputColumnID: related.OutputColumnID,
		CandidateID: related.Source.CandidateID, SourceOccurrenceID: related.SourceOccurrenceID,
		ResourceType: related.Source.ResourceType, Path: related.Source.Path, LogicalType: related.Source.LogicalType,
		Form: related.Form, RowValuePolicy: string(rowValuePolicy), ContributorPolicy: related.ContributorPolicy,
	}
	work.schema, work.identity = compiled, inputIdentity
	return nil
}

func (lowerer *constructionLowerer) lowerRelatedExpandStage(step recipe.ConstructionStep, work *constructionStageLowering) error {
	inputByID := work.inputByID
	related := step.Operation.RelatedExpand
	if related == nil {
		return fmt.Errorf("related expansion payload is required")
	}
	physicalExpand, projections, compiled, err := lowerer.lowerConstructionRelatedExpand(step, *related, inputByID, work.loweringContext())
	if err != nil {
		return err
	}
	work.physical.Kind, work.physical.RelatedExpand = ir.PhysicalStageRelatedExpandOp, &physicalExpand
	work.physical.OutputProjections = projections
	work.schema, work.identity = compiled, constructionRowID
	return nil
}

func (lowerer *constructionLowerer) lowerRelatedEligibilityStage(step recipe.ConstructionStep, work *constructionStageLowering) error {
	inputByID := work.inputByID
	inputIdentity := work.input.identity
	related := step.Operation.RelatedEligibility
	if related == nil {
		return fmt.Errorf("related eligibility payload is required")
	}
	physicalFilter, derivedLets, projections, compiled, err := lowerer.lowerConstructionRelatedEligibility(step, *related, inputByID, work.loweringContext())
	if err != nil {
		return err
	}
	work.physical.Kind, work.physical.Filter, work.physical.DerivedLets = ir.PhysicalStageRelatedEligibilityOp, &physicalFilter, derivedLets
	work.physical.OutputProjections = projections
	work.schema, work.identity = compiled, inputIdentity
	return nil
}

func (lowerer *constructionLowerer) lowerRelatedFieldStage(step recipe.ConstructionStep, work *constructionStageLowering) error {
	inputByID := work.inputByID
	outputByID := work.outputByID
	inputSchema := work.input.schema
	inputIdentity := work.input.identity
	presenceCategoryIDs := work.input.presenceCategoryIDs
	related := step.Operation.RelatedField
	if related == nil {
		return fmt.Errorf("related field payload is required")
	}
	physicalRelatedField, projections, compiled, err := lowerConstructionRelatedField(step, *related, inputByID, outputByID, inputSchema, work.loweringContext(), presenceCategoryIDs[related.OutputColumnID])
	if err != nil {
		return err
	}
	work.physical.Kind, work.physical.RelatedField, work.physical.OutputProjections = ir.PhysicalStageRelatedFieldOp, &physicalRelatedField, projections
	work.schema, work.identity = compiled, inputIdentity
	return nil
}

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
	inputIdentity := work.input.identity
	rootResourceType := work.input.rootResourceType
	index := work.input.index
	codedPivot := step.Operation.CodedPivot
	if codedPivot == nil {
		return fmt.Errorf("coded Pivot payload is required")
	}
	physicalPivot, projections, compiled, err := lowerConstructionCodedPivot(
		lowerer.plan, *codedPivot, step.RowValues, step.Outputs, inputByID, inputIdentity, rootResourceType, lowerer.usedVariables, index,
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

func (lowerer *constructionLowerer) lowerDeriveStage(step recipe.ConstructionStep, work *constructionStageLowering) error {
	inputRow := work.physical.InputRowVariable
	inputByID := work.inputByID
	outputByID := work.outputByID
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
			prior.Name, prior.Label = declaration.Name, loomtext.FirstNonBlank(declaration.Label, prior.Label, declaration.Name)
			work.schema = append(work.schema, prior)
			continue
		}
		if declaration.ID != derive.OutputColumnID {
			return fmt.Errorf("derive output schema contains unexpected new column ID %q", declaration.ID)
		}
		metadata := derivedTypes[recipeColumn.Name]
		work.schema = append(work.schema, CompiledOutputColumn{
			ID: declaration.ID, Name: declaration.Name, Label: loomtext.FirstNonBlank(declaration.Label, declaration.Name),
			SemanticPath: "derived:" + derive.ConstructionID, Kind: string(metadata.Type.Kind),
			Cardinality: string(metadata.Type.Cardinality), Nullable: metadata.Type.Cardinality.Optional(),
			NormalizedUnit: ir.CloneUnitIdentity(metadata.NormalizedUnit),
		})
	}
	work.physical.OutputProjections = stagePassThroughAndDerivedProjections(step.Outputs, inputByID, inputRow, column.ID, column.Name, derivedProjection)
	work.identity = inputIdentity
	return nil
}

func (lowerer *constructionLowerer) lowerFilterStage(step recipe.ConstructionStep, work *constructionStageLowering) error {
	inputRow := work.physical.InputRowVariable
	inputByID := work.inputByID
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
		prior.Name, prior.Label = declaration.Name, loomtext.FirstNonBlank(declaration.Label, prior.Label, declaration.Name)
		work.schema = append(work.schema, prior)
	}
	work.physical.OutputProjections = stagePassThroughProjections(step.Outputs, inputByID, inputRow)
	work.identity = inputIdentity
	return nil
}
