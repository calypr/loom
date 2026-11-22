package ir

import (
	"fmt"
)

func (validation constructionStageValidation) validateRelatedSource(stage PhysicalConstructionStage, path string) error {
	bindVars := validation.bindVars
	if stage.RelatedSource == nil || stage.RelatedExpand != nil || stage.RelatedField != nil || stage.Filter != nil || stage.Group != nil || stage.Expand != nil || stage.GroupedPivot != nil || stage.Unpivot != nil || len(stage.DerivedLets) != 0 {
		return fmt.Errorf("%s RELATED_SOURCE requires only a related-source payload", path)
	}
	if err := validatePhysicalStageRelatedSource(stage, *stage.RelatedSource, bindVars); err != nil {
		return fmt.Errorf("%s related source: %w", path, err)
	}
	return nil
}

func (validation constructionStageValidation) validateRelatedExpand(stage PhysicalConstructionStage, path string) error {
	bindVars := validation.bindVars
	if stage.RelatedExpand == nil || stage.RelatedSource != nil || stage.RelatedField != nil || stage.Filter != nil || stage.Group != nil || stage.Expand != nil || stage.GroupedPivot != nil || stage.Unpivot != nil || len(stage.DerivedLets) != 0 {
		return fmt.Errorf("%s RELATED_EXPAND requires only a related-expansion payload", path)
	}
	if err := validatePhysicalStageRelatedExpand(stage, *stage.RelatedExpand, bindVars); err != nil {
		return fmt.Errorf("%s related expansion: %w", path, err)
	}
	return nil
}

func (validation constructionStageValidation) validateRelatedEligibility(stage PhysicalConstructionStage, path string) error {
	bindVars := validation.bindVars
	if stage.Filter == nil || stage.RelatedSource != nil || stage.RelatedExpand != nil || stage.RelatedField != nil || stage.Group != nil || stage.Expand != nil || stage.GroupedPivot != nil || stage.Unpivot != nil {
		return fmt.Errorf("%s RELATED_ELIGIBILITY requires only a filter and optional typed count LET", path)
	}
	if err := validatePhysicalStageRelatedEligibility(stage, bindVars); err != nil {
		return fmt.Errorf("%s related eligibility: %w", path, err)
	}
	if err := validateStageRowOperations(stage, bindVars, true); err != nil {
		return err
	}
	return nil
}

func (validation constructionStageValidation) validateRelatedField(stage PhysicalConstructionStage, path string) error {
	bindVars := validation.bindVars
	if stage.RelatedField == nil || stage.RelatedExpand != nil || stage.RelatedSource != nil || stage.Filter != nil || stage.Group != nil || stage.Expand != nil || stage.GroupedPivot != nil || stage.Unpivot != nil || len(stage.DerivedLets) != 0 {
		return fmt.Errorf("%s RELATED_FIELD requires only a related-field payload", path)
	}
	if err := validatePhysicalStageRelatedField(stage, *stage.RelatedField, bindVars); err != nil {
		return fmt.Errorf("%s related field: %w", path, err)
	}
	return nil
}
