package lower

import (
	"fmt"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

func (lowerer *constructionLowerer) lowerRelatedSourceStage(step recipe.ConstructionStep, work *constructionStageLowering) error {
	inputByID := work.inputByID
	outputByID := work.outputByID
	inputRow := work.physical.InputRowVariable
	inputSchema := work.input.schema
	inputIdentity := work.input.identity
	rootResourceType := work.input.rootResourceType
	index := work.input.index
	related := step.Operation.RelatedSource
	if related == nil {
		return fmt.Errorf("related source payload is required")
	}
	rowValuePolicy := related.RowValuePolicy
	if rowValuePolicy == "" {
		rowValuePolicy = recipe.ConstructionRowValueAll
	}
	projections, compiled, err := lowerer.lowerConstructionRelatedSource(step, *related, inputByID, outputByID, inputRow, inputIdentity, rootResourceType, index)
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
	outputByID := work.outputByID
	inputRow := work.physical.InputRowVariable
	inputIdentity := work.input.identity
	rootResourceType := work.input.rootResourceType
	index := work.input.index
	related := step.Operation.RelatedExpand
	if related == nil {
		return fmt.Errorf("related expansion payload is required")
	}
	physicalExpand, projections, compiled, err := lowerer.lowerConstructionRelatedExpand(
		step, *related, inputByID, outputByID, inputRow, inputIdentity,
		rootResourceType, index,
	)
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
	outputByID := work.outputByID
	inputRow := work.physical.InputRowVariable
	inputIdentity := work.input.identity
	rootResourceType := work.input.rootResourceType
	index := work.input.index
	related := step.Operation.RelatedEligibility
	if related == nil {
		return fmt.Errorf("related eligibility payload is required")
	}
	physicalFilter, derivedLets, projections, compiled, err := lowerer.lowerConstructionRelatedEligibility(
		step, *related, inputByID, outputByID, inputRow, inputIdentity,
		rootResourceType, index,
	)
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
	inputRow := work.physical.InputRowVariable
	inputSchema := work.input.schema
	inputIdentity := work.input.identity
	presenceCategoryIDs := work.input.presenceCategoryIDs
	related := step.Operation.RelatedField
	if related == nil {
		return fmt.Errorf("related field payload is required")
	}
	physicalRelatedField, projections, compiled, err := lowerConstructionRelatedField(step, *related, inputByID, outputByID, inputSchema, inputRow, presenceCategoryIDs[related.OutputColumnID])
	if err != nil {
		return err
	}
	work.physical.Kind, work.physical.RelatedField, work.physical.OutputProjections = ir.PhysicalStageRelatedFieldOp, &physicalRelatedField, projections
	work.schema, work.identity = compiled, inputIdentity
	return nil
}
