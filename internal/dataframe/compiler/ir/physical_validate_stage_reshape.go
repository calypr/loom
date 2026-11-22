package ir

import (
	"fmt"
)

func (validation constructionStageValidation) validatePivot(stage PhysicalConstructionStage, path string, index int) error {
	sequence := validation.sequence
	sourceOperations := validation.sourceOperations
	bindVars := validation.bindVars
	if stage.GroupedPivot == nil || stage.Filter != nil || stage.Group != nil || stage.Expand != nil || stage.Unpivot != nil || len(stage.DerivedLets) != 0 {
		return fmt.Errorf("%s PIVOT requires only a grouped-pivot payload", path)
	}
	if stage.OutputRowVariable != stage.GroupedPivot.OutputRowVariable {
		return fmt.Errorf("%s output row variable does not match grouped pivot", path)
	}
	defined := map[string]bool{stage.InputRowVariable: true}
	if stage.GroupedPivot.CodedCorrelation != nil {
		if index != 0 || stage.InputStageID != sequence.SourceStageID {
			return fmt.Errorf("%s coded Pivot must be the first stage over the direct source projection", path)
		}
		rootVariable, resourceType, rootErr := validateCodedPivotSourceRoot(sourceOperations, bindVars)
		if rootErr != nil {
			return fmt.Errorf("%s coded Pivot source: %w", path, rootErr)
		}
		if rootVariable != stage.GroupedPivot.CodedSourceVariable || resourceType != stage.GroupedPivot.CodedCorrelation.ResourceType {
			return fmt.Errorf("%s coded Pivot source does not match the exact root scan", path)
		}
		defined[rootVariable] = true
	}
	if err := validatePhysicalGroupedPivot(*stage.GroupedPivot, defined, bindVars); err != nil {
		return fmt.Errorf("%s grouped pivot: %w", path, err)
	}
	if err := validateRootContributorReduction(
		stage.GroupedPivot.RootContributorInputColumn,
		stage.GroupedPivot.RootContributorInputMany,
		stage.GroupedPivot.RootContributorOutputColumn,
		stage.GroupedPivot.RootContributorVariable,
		stage.InputColumns, stage.OutputColumns, defined,
	); err != nil {
		return fmt.Errorf("%s grouped pivot root contributors: %w", path, err)
	}
	if err := validatePhysicalStageRowValues(
		stage.GroupedPivot.RowValues, stage.InputColumns, stage.OutputColumns,
		physicalProjectionNameSet(stage.GroupedPivot.InputProjections), groupedPivotBaseOutputs(*stage.GroupedPivot), nil, "",
	); err != nil {
		return fmt.Errorf("%s grouped pivot row values: %w", path, err)
	}
	if err := validateShapeStageOutput(stage, groupedPivotOutputNames(*stage.GroupedPivot)); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

func (validation constructionStageValidation) validateUnpivot(stage PhysicalConstructionStage, path string) error {
	bindVars := validation.bindVars
	if stage.Unpivot == nil || stage.Filter != nil || stage.Group != nil || stage.Expand != nil || stage.GroupedPivot != nil || len(stage.DerivedLets) != 0 {
		return fmt.Errorf("%s UNPIVOT requires only an unpivot payload", path)
	}
	if stage.OutputRowVariable != stage.Unpivot.OutputRowVariable {
		return fmt.Errorf("%s output row variable does not match unpivot", path)
	}
	defined := map[string]bool{stage.InputRowVariable: true}
	if err := validatePhysicalUnpivot(*stage.Unpivot, defined, bindVars); err != nil {
		return fmt.Errorf("%s unpivot: %w", path, err)
	}
	if err := validateShapeStageOutput(stage, unpivotOutputNames(*stage.Unpivot)); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}
