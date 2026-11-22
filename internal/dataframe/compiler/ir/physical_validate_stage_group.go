package ir

import (
	"fmt"
)

func (validation constructionStageValidation) validateGroup(stage PhysicalConstructionStage, path string) error {
	sequence := validation.sequence
	sourceOperations := validation.sourceOperations
	bindVars := validation.bindVars
	if stage.Group == nil || stage.Filter != nil || stage.Expand != nil || stage.GroupedPivot != nil || stage.Unpivot != nil || len(stage.DerivedLets) != 0 {
		return fmt.Errorf("%s GROUP requires only a group payload", path)
	}
	if err := validatePhysicalStageGroup(stage, *stage.Group, bindVars, sequence.SourceRowIdentity, sourceOperations); err != nil {
		return fmt.Errorf("%s group: %w", path, err)
	}
	expected := make([]string, 0, len(stage.Group.Keys)+len(stage.Group.Aggregates)+len(stage.Group.RowValues)+2)
	for _, key := range stage.Group.Keys {
		if key.OutputColumn != "" {
			expected = append(expected, key.OutputColumn)
		}
	}
	if stage.Group.SemanticKey != nil {
		expected = append(expected, stage.Group.SemanticKey.OutputColumn)
	}
	if stage.Group.CodingKey != nil {
		expected = append(expected, stage.Group.CodingKey.OutputColumn)
	}
	for _, aggregate := range stage.Group.Aggregates {
		expected = append(expected, aggregate.Output)
	}
	for _, rowValue := range stage.Group.RowValues {
		expected = append(expected, rowValue.Output)
	}
	if stage.Group.RootContributorOutputColumn != "" {
		expected = append(expected, stage.Group.RootContributorOutputColumn)
	}
	expected = append(expected, "__loom_row_id")
	if err := validateShapeStageOutput(stage, expected); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

func (validation constructionStageValidation) validateCodedGroup(stage PhysicalConstructionStage, path string) error {
	sourceOperations := validation.sourceOperations
	bindVars := validation.bindVars
	if stage.CodedGroup == nil || stage.Group != nil || stage.Filter != nil || stage.Expand != nil || stage.GroupedPivot != nil || stage.Unpivot != nil || stage.RelatedSource != nil || stage.RelatedExpand != nil || stage.RelatedField != nil || len(stage.DerivedLets) != 0 {
		return fmt.Errorf("%s CODED_GROUP requires only a coded-group payload", path)
	}
	if err := validatePhysicalStageCodedGroup(stage, *stage.CodedGroup, bindVars, sourceOperations); err != nil {
		return fmt.Errorf("%s coded group: %w", path, err)
	}
	expected := []string{stage.CodedGroup.SystemOutputColumn, stage.CodedGroup.VersionOutputColumn, stage.CodedGroup.CodeOutputColumn, stage.CodedGroup.CountOutputColumn}
	for _, rowValue := range stage.CodedGroup.RowValues {
		expected = append(expected, rowValue.Output)
	}
	expected = append(expected, "__loom_row_id")
	if err := validateShapeStageOutput(stage, expected); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

func (validation constructionStageValidation) validateCohortGroup(stage PhysicalConstructionStage, path string) error {
	bindVars := validation.bindVars
	if stage.CohortGroup == nil || stage.Group != nil || stage.CodedGroup != nil || stage.Filter != nil || stage.Expand != nil || stage.GroupedPivot != nil || stage.Unpivot != nil || len(stage.DerivedLets) != 0 || len(stage.OutputProjections) != 0 {
		return fmt.Errorf("%s COHORT_GROUP requires only a cohort-group payload", path)
	}
	if err := validatePhysicalStageCohortGroup(stage, *stage.CohortGroup, bindVars); err != nil {
		return fmt.Errorf("%s cohort group: %w", path, err)
	}
	expected := []string{"group_revision_id", "group_id", "group_label", "group_ordinal", "members"}
	for _, value := range stage.CohortGroup.Rows.MemberValues {
		expected = append(expected, value.Output)
	}
	expected = append(expected, stage.CohortGroup.RootContributorOutputColumn, "__loom_row_id")
	if err := validateShapeStageOutput(stage, expected); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}
