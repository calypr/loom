package ir

import (
	"fmt"
)

type constructionStageValidation struct {
	sequence         PhysicalStageSequence
	sourceOperations []PhysicalOperation
	bindVars         map[string]any
}

func (validation constructionStageValidation) validateOperation(stage PhysicalConstructionStage, index int, path string) error {
	switch stage.Kind {
	case PhysicalStageDeriveOp:
		return validation.validateDerive(stage, path)
	case PhysicalStageFilterOp:
		return validation.validateFilter(stage, path)
	case PhysicalStagePivotOp:
		return validation.validatePivot(stage, path, index)
	case PhysicalStageUnpivotOp:
		return validation.validateUnpivot(stage, path)
	case PhysicalStageGroupOp:
		return validation.validateGroup(stage, path)
	case PhysicalStageCodedGroupOp:
		return validation.validateCodedGroup(stage, path)
	case PhysicalStageCohortGroupOp:
		return validation.validateCohortGroup(stage, path)
	case PhysicalStageExpandOp:
		return validation.validateExpand(stage, path)
	case PhysicalStageRelatedSourceOp:
		return validation.validateRelatedSource(stage, path)
	case PhysicalStageRelatedExpandOp:
		return validation.validateRelatedExpand(stage, path)
	case PhysicalStageRelatedEligibilityOp:
		return validation.validateRelatedEligibility(stage, path)
	case PhysicalStageRelatedFieldOp:
		return validation.validateRelatedField(stage, path)
	default:
		return fmt.Errorf("%s has unsupported operation kind %q", path, stage.Kind)
	}
}
