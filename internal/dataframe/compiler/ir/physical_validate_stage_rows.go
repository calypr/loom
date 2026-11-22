package ir

import (
	"fmt"
)

func (validation constructionStageValidation) validateDerive(stage PhysicalConstructionStage, path string) error {
	bindVars := validation.bindVars
	if stage.Filter != nil || stage.Group != nil || stage.Expand != nil || stage.GroupedPivot != nil || stage.Unpivot != nil || stage.RelatedField != nil {
		return fmt.Errorf("%s DERIVE has mismatched operation payloads", path)
	}
	if len(stage.DerivedLets) == 0 {
		return fmt.Errorf("%s DERIVE requires at least one typed expression LET", path)
	}
	if err := validateStageRowOperations(stage, bindVars, false); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

func (validation constructionStageValidation) validateFilter(stage PhysicalConstructionStage, path string) error {
	bindVars := validation.bindVars
	if stage.Filter == nil || stage.Group != nil || stage.Expand != nil || stage.GroupedPivot != nil || stage.Unpivot != nil || stage.RelatedField != nil || len(stage.DerivedLets) != 0 {
		return fmt.Errorf("%s FILTER requires only a filter payload", path)
	}
	if err := validateStageRowOperations(stage, bindVars, true); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

func (validation constructionStageValidation) validateExpand(stage PhysicalConstructionStage, path string) error {
	bindVars := validation.bindVars
	if stage.Expand == nil || stage.Filter != nil || stage.Group != nil || stage.GroupedPivot != nil || stage.Unpivot != nil || len(stage.DerivedLets) != 0 {
		return fmt.Errorf("%s EXPAND requires only an expand payload", path)
	}
	if err := validatePhysicalStageExpand(stage, *stage.Expand, bindVars); err != nil {
		return fmt.Errorf("%s expand: %w", path, err)
	}
	expected := make([]string, 0, len(stage.InputColumns)+1)
	for _, column := range stage.InputColumns {
		if column.Internal || column.Name == stage.Expand.InputColumn {
			continue
		}
		expected = append(expected, column.Name)
	}
	expected = append(expected, stage.Expand.OutputColumn)
	if stage.Expand.OrdinalColumn != "" {
		expected = append(expected, stage.Expand.OrdinalColumn)
	}
	expected = append(expected, "__loom_row_id")
	if err := validateShapeStageOutput(stage, expected); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}
