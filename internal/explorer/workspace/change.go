package workspace

import (
	"encoding/json"
	"fmt"
	"reflect"
)

type ConstructionDependencyIssue struct {
	StepID   string `json:"stepId"`
	ColumnID string `json:"columnId"`
}

type ConstructionImpact struct {
	ChangedStepID   string                        `json:"changedStepId,omitempty"`
	RemovedStepIDs  []string                      `json:"removedStepIds,omitempty"`
	AffectedStepIDs []string                      `json:"affectedStepIds"`
	MissingInputs   []ConstructionDependencyIssue `json:"missingInputs,omitempty"`
}

func (i ConstructionImpact) HasMissingInputs() bool {
	return len(i.MissingInputs) != 0
}

func appendedRelatedSourceSuffixStart(existing, candidate []ConstructionStep, start int, changedStepID string) (int, bool) {
	if start != len(existing) || start >= len(candidate) || len(candidate)-start < 2 ||
		candidate[len(candidate)-1].ID != changedStepID || !reflect.DeepEqual(candidate[:start], existing) {
		return 0, false
	}

	first := candidate[start]
	if first.Operation.Kind != ConstructionOperationRelatedSource || first.Operation.RelatedSource == nil || first.OwnerStepID != "" {
		return 0, false
	}
	anchorColumnID := first.Operation.RelatedSource.AnchorColumnID
	route := first.Operation.RelatedSource.Route
	previousStepID := ""
	if start > 0 {
		previousStepID = candidate[start-1].ID
	}
	for index := start; index < len(candidate); index++ {
		step := candidate[index]
		related := step.Operation.RelatedSource
		if step.Operation.Kind != ConstructionOperationRelatedSource || related == nil || step.OwnerStepID != "" ||
			related.AnchorColumnID != anchorColumnID || !reflect.DeepEqual(related.Route, route) {
			return 0, false
		}
		if len(step.Inputs) != 1 {
			return 0, false
		}
		if previousStepID == "" {
			if step.Inputs[0] != (ConstructionInputRef{Kind: ConstructionInputSourceProjection}) {
				return 0, false
			}
		} else if step.Inputs[0] != (ConstructionInputRef{Kind: ConstructionInputStepOutput, StepID: previousStepID}) {
			return 0, false
		}
		previousStepID = step.ID
	}
	return start, true
}

func recalculateCandidateStages(candidate *Document, start int, impact *ConstructionImpact) error {
	source, err := sourceStageColumns(candidate.Columns)
	if err != nil {
		return err
	}
	if start == 0 && len(candidate.Construction.Steps) > 0 {
		source, err = effectiveConstructionSourceSchema(*candidate)
		if err != nil {
			return fmt.Errorf("source projection: %w", err)
		}
	}
	authoredSourceIDs := constructionAuthoredSourceIDs(*candidate)
	for index := start; index < len(candidate.Construction.Steps); index++ {
		step := &candidate.Construction.Steps[index]
		if step.Operation.Kind == ConstructionOperationCombine || step.Operation.Combine != nil {
			if index != 0 || len(candidate.Construction.Steps) != 1 {
				return fmt.Errorf("COMBINE must remain the only construction step")
			}
			continue
		}
		inputColumns, err := effectiveConstructionInputSchema(*candidate, source, index)
		if err != nil {
			return fmt.Errorf("step %q input: %w", step.ID, err)
		}
		inputIndex, err := stageColumnIndex(inputColumns)
		if err != nil {
			return fmt.Errorf("step %q input schema: %w", step.ID, err)
		}
		for _, columnID := range step.inputColumnIDs() {
			if _, exists := inputIndex[columnID]; !exists {
				impact.MissingInputs = append(impact.MissingInputs, ConstructionDependencyIssue{StepID: step.ID, ColumnID: columnID})
			}
		}
		outputs, err := rebuildStageColumns(*step, inputColumns, authoredSourceIDs)
		if err != nil {
			return fmt.Errorf("recalculate step %q: %w", step.ID, err)
		}
		step.Outputs = outputs
	}
	return nil
}

func effectiveConstructionSourceSchema(document Document) ([]StageColumn, error) {
	if hasExplicitGroupRows(document) && document.Rows.Groups.AfterStepID == "" {
		return sourceStageColumns(document.Columns)
	}
	if document.Construction == nil || len(document.Construction.Steps) == 0 {
		return sourceStageColumns(document.Columns)
	}
	first := document.Construction.Steps[0]
	return constructionSourceColumnsWithChildrenAndProjections(document.Columns, document.Construction.SourceProjections, first.inputColumnIDs(), first.Outputs)
}

func effectiveConstructionInputSchema(document Document, source []StageColumn, index int) ([]StageColumn, error) {
	if document.Construction == nil || index < 0 || index >= len(document.Construction.Steps) {
		return nil, fmt.Errorf("construction step index is invalid")
	}
	if !hasExplicitGroupRows(document) {
		return constructionStepInputSchema(document.Construction.Steps, source, index)
	}
	anchorIndex := -1
	if document.Rows.Groups.AfterStepID != "" {
		anchorIndex = findConstructionStep(document.Construction.Steps, document.Rows.Groups.AfterStepID)
		if anchorIndex < 0 {
			return nil, fmt.Errorf("group rows afterStepId %q does not name a construction step", document.Rows.Groups.AfterStepID)
		}
	}
	if index == anchorIndex+1 {
		return explicitGroupStageColumns(document.Columns, document.Rows.Groups)
	}
	if index == 0 {
		return source, nil
	}
	return document.Construction.Steps[index-1].Outputs, nil
}

func hasExplicitGroupRows(document Document) bool {
	return document.Rows.Kind == RowDefinitionGroups && document.Rows.Groups != nil && document.Rows.Groups.Source.Kind == GroupSourceExplicit
}

func normalizeEffectiveConstructionInputs(document *Document) {
	if document == nil || document.Construction == nil {
		return
	}
	anchorIndex := -2
	if hasExplicitGroupRows(*document) {
		anchorIndex = -1
		if document.Rows.Groups.AfterStepID != "" {
			anchorIndex = findConstructionStep(document.Construction.Steps, document.Rows.Groups.AfterStepID)
		}
	}
	for index := range document.Construction.Steps {
		step := &document.Construction.Steps[index]
		if step.Operation.Kind == ConstructionOperationCombine || step.Operation.Combine != nil {
			continue
		}
		switch {
		case anchorIndex != -2 && index == anchorIndex+1:
			step.Inputs = []ConstructionInputRef{{Kind: ConstructionInputStepOutput, StepID: "group_rows"}}
		case index == 0:
			step.Inputs = []ConstructionInputRef{{Kind: ConstructionInputSourceProjection}}
		default:
			step.Inputs = []ConstructionInputRef{{Kind: ConstructionInputStepOutput, StepID: document.Construction.Steps[index-1].ID}}
		}
	}
}

func repairExplicitGroupAnchorAfterConstructionChange(before Document, after *Document) {
	if after == nil || !hasExplicitGroupRows(before) || after.Construction == nil {
		return
	}
	anchor := before.Rows.Groups.AfterStepID
	if anchor == "" || findConstructionStep(after.Construction.Steps, anchor) >= 0 {
		return
	}
	oldIndex := findConstructionStep(before.Construction.Steps, anchor)
	for index := oldIndex - 1; index >= 0; index-- {
		candidate := before.Construction.Steps[index].ID
		if findConstructionStep(after.Construction.Steps, candidate) >= 0 {
			after.Rows.Groups.AfterStepID = candidate
			return
		}
	}
	after.Rows.Groups.AfterStepID = ""
}

func pruneConstructionSourceProjections(projections []ConstructionSourceProjection, steps []ConstructionStep) []ConstructionSourceProjection {
	used := make(map[string]bool)
	owners := make(map[string]bool, len(steps))
	for _, step := range steps {
		owners[step.ID] = step.Operation.Kind == ConstructionOperationPivot || step.Operation.Kind == ConstructionOperationGroup ||
			step.Operation.Kind == ConstructionOperationFilter || step.Operation.Kind == ConstructionOperationDerive ||
			step.Operation.Kind == ConstructionOperationUnpivot || step.Operation.Kind == ConstructionOperationExpand
		for _, columnID := range step.inputColumnIDs() {
			used[columnID] = true
		}
	}
	pruned := make([]ConstructionSourceProjection, 0, len(projections))
	for _, projection := range projections {
		if used[projection.ColumnID] && (projection.OwnerStepID == "" || owners[projection.OwnerStepID]) {
			pruned = append(pruned, projection)
		}
	}
	return pruned
}

func isNewOwnedReshapeInput(step ConstructionStep, ownerStepID string, existing []ConstructionStep) bool {
	if ownerStepID == "" || step.OwnerStepID != ownerStepID ||
		(step.Operation.Kind != ConstructionOperationRelatedField && step.Operation.Kind != ConstructionOperationCodedPivot) {
		return false
	}
	return findConstructionStep(existing, step.ID) < 0
}

func stepDiffOnlyChangesOwnedProjection(
	oldStep, candidate ConstructionStep,
	ownerStepIDs map[string]bool,
	oldProjections, projections []ConstructionSourceProjection,
) bool {
	oldWithoutOutputs, candidateWithoutOutputs := oldStep, candidate
	oldWithoutOutputs.Outputs, candidateWithoutOutputs.Outputs = nil, nil
	if !reflect.DeepEqual(oldWithoutOutputs, candidateWithoutOutputs) {
		return false
	}
	oldOwned := make(map[string]StageColumn)
	for _, projection := range oldProjections {
		if ownerStepIDs[projection.OwnerStepID] {
			oldOwned[projection.ColumnID] = StageColumn{
				ID: projection.ColumnID, Name: ConstructionSourceProjectionName(projection.ColumnID),
				Label: projection.Label, Type: projection.LogicalType,
			}
		}
	}
	owned := make(map[string]StageColumn)
	for _, projection := range projections {
		if ownerStepIDs[projection.OwnerStepID] {
			owned[projection.ColumnID] = StageColumn{
				ID: projection.ColumnID, Name: ConstructionSourceProjectionName(projection.ColumnID),
				Label: projection.Label, Type: projection.LogicalType,
			}
		}
	}
	oldIDs := make(map[string]StageColumn, len(oldStep.Outputs))
	for _, output := range oldStep.Outputs {
		oldIDs[output.ID] = output
	}
	candidateIDs := make(map[string]StageColumn, len(candidate.Outputs))
	for _, output := range candidate.Outputs {
		candidateIDs[output.ID] = output
		old, existed := oldIDs[output.ID]
		_, wasOwned := oldOwned[output.ID]
		expected, stillOwned := owned[output.ID]
		if existed && !wasOwned && !reflect.DeepEqual(old, output) {
			return false
		}
		if stillOwned {
			if !reflect.DeepEqual(expected, output) {
				return false
			}
			delete(owned, output.ID)
			delete(oldIDs, output.ID)
			continue
		}
		if wasOwned {
			if !existed || !reflect.DeepEqual(old, output) {
				return false
			}
			delete(oldIDs, output.ID)
			continue
		}
		if !existed {
			return false
		}
		delete(oldIDs, output.ID)
	}
	for id := range oldIDs {
		if _, wasOwned := oldOwned[id]; !wasOwned {
			return false
		}
	}
	for id := range owned {
		if _, exists := candidateIDs[id]; !exists {
			return false
		}
	}
	return true
}

func rebuildStageColumns(step ConstructionStep, input []StageColumn, authoredSourceIDs map[string]struct{}) ([]StageColumn, error) {
	declared, err := stageColumnIndex(step.Outputs)
	if err != nil {
		return nil, fmt.Errorf("output declaration: %w", err)
	}
	produced := func(id string) (StageColumn, error) {
		column, exists := declared[id]
		if !exists {
			return StageColumn{}, fmt.Errorf("operation output %q has no declared schema", id)
		}
		return column, nil
	}
	var outputs []StageColumn
	switch step.Operation.Kind {
	case ConstructionOperationDerive:
		if step.Operation.Derive == nil {
			return nil, fmt.Errorf("derive payload is required")
		}
		outputs = append(outputs, input...)
		column, err := produced(step.Operation.Derive.OutputColumnID)
		if err != nil {
			return nil, err
		}
		outputs = append(outputs, column)
	case ConstructionOperationFilter:
		if step.Operation.Filter == nil {
			return nil, fmt.Errorf("filter payload is required")
		}
		outputs = append(outputs, input...)
	case ConstructionOperationPivot:
		if step.Operation.Pivot == nil {
			return nil, fmt.Errorf("pivot payload is required")
		}
		for _, id := range step.Operation.Pivot.GroupKeyIDs {
			column, exists := findStageColumnByID(input, id)
			if exists {
				if presentation, declaredOutput := declared[id]; declaredOutput {
					_, authoredSource := authoredSourceIDs[id]
					if column.Name == ConstructionSourceProjectionName(id) {
						column.Name = presentation.Name
						column.Label = presentation.Label
					} else if !authoredSource {
						column.Label = presentation.Label
					}
					column.Table = cloneTablePresentation(presentation.Table)
				}
				outputs = append(outputs, column)
			}
		}
		for _, category := range step.Operation.Pivot.Categories {
			column, err := produced(category.OutputColumnID)
			if err != nil {
				return nil, err
			}
			outputs = append(outputs, column)
		}
	case ConstructionOperationCodedPivot:
		if step.Operation.CodedPivot == nil {
			return nil, fmt.Errorf("codedPivot payload is required")
		}
		for _, category := range step.Operation.CodedPivot.Categories {
			column, err := produced(category.OutputColumnID)
			if err != nil {
				return nil, err
			}
			outputs = append(outputs, column)
		}
	case ConstructionOperationUnpivot:
		if step.Operation.Unpivot == nil {
			return nil, fmt.Errorf("unpivot payload is required")
		}
		removed := make(map[string]bool, len(step.Operation.Unpivot.Inputs))
		for _, item := range step.Operation.Unpivot.Inputs {
			removed[item.ColumnID] = true
		}
		for _, column := range input {
			if !removed[column.ID] {
				outputs = append(outputs, column)
			}
		}
		key, err := produced(step.Operation.Unpivot.KeyOutputColumnID)
		if err != nil {
			return nil, err
		}
		value, err := produced(step.Operation.Unpivot.ValueOutputColumnID)
		if err != nil {
			return nil, err
		}
		outputs = append(outputs, key, value)
	case ConstructionOperationGroup:
		if step.Operation.Group == nil {
			return nil, fmt.Errorf("group payload is required")
		}
		for _, key := range step.Operation.Group.Keys {
			column, err := produced(key.OutputColumnID)
			if err != nil {
				return nil, err
			}
			if column.Type == "" || column.Type == "INFER" {
				if inputColumn, exists := findStageColumnByID(input, key.InputColumnID); exists {
					column.Type = inputColumn.Type
				}
			}
			outputs = append(outputs, column)
		}
		for _, aggregate := range step.Operation.Group.Aggregates {
			column, err := produced(aggregate.OutputColumnID)
			if err != nil {
				return nil, err
			}
			outputs = append(outputs, column)
		}
	case ConstructionOperationCodedGroup:
		if step.Operation.CodedGroup == nil {
			return nil, fmt.Errorf("codedGroup payload is required")
		}
		coded := step.Operation.CodedGroup
		for _, id := range []string{
			coded.SystemOutputColumnID,
			coded.VersionOutputColumnID,
			coded.CodeOutputColumnID,
			coded.DistinctSourceCountOutputColumnID,
		} {
			column, err := produced(id)
			if err != nil {
				return nil, err
			}
			outputs = append(outputs, column)
		}
	case ConstructionOperationExpand:
		if step.Operation.Expand == nil {
			return nil, fmt.Errorf("expand payload is required")
		}
		for _, column := range input {
			if column.ID != step.Operation.Expand.InputColumnID {
				outputs = append(outputs, column)
			}
		}
		item, err := produced(step.Operation.Expand.OutputColumnID)
		if err != nil {
			return nil, err
		}
		outputs = append(outputs, item)
		if step.Operation.Expand.OrdinalColumnID != "" {
			ordinal, err := produced(step.Operation.Expand.OrdinalColumnID)
			if err != nil {
				return nil, err
			}
			outputs = append(outputs, ordinal)
		}
	case ConstructionOperationRelatedSource:
		if step.Operation.RelatedSource == nil {
			return nil, fmt.Errorf("relatedSource payload is required")
		}
		outputs = append(outputs, input...)
		column, err := produced(step.Operation.RelatedSource.OutputColumnID)
		if err != nil {
			return nil, err
		}
		if column.Type == "" || column.Type == "INFER" {
			column.Type = step.Operation.RelatedSource.Source.LogicalType
		}
		outputs = append(outputs, column)
	case ConstructionOperationRelatedExpand:
		if step.Operation.RelatedExpand == nil {
			return nil, fmt.Errorf("relatedExpand payload is required")
		}
		outputs = append(outputs, input...)
		column, err := produced(step.Operation.RelatedExpand.RelatedRecordColumnID)
		if err != nil {
			return nil, err
		}
		if column.Type == "" || column.Type == "INFER" {
			column.Type = "string"
		}
		column.Nullable = step.Operation.RelatedExpand.EmptyPolicy == ConstructionExpandEmptyPreserveParent
		outputs = append(outputs, column)
	case ConstructionOperationRelatedEligibility:
		if step.Operation.RelatedEligibility == nil {
			return nil, fmt.Errorf("relatedEligibility payload is required")
		}
		outputs = append(outputs, input...)
	case ConstructionOperationRelatedField:
		if step.Operation.RelatedField == nil {
			return nil, fmt.Errorf("relatedField payload is required")
		}
		outputs = append(outputs, input...)
		column, err := produced(step.Operation.RelatedField.OutputColumnID)
		if err != nil {
			return nil, err
		}
		if column.Type == "" || column.Type == "INFER" {
			column.Type = step.Operation.RelatedField.Source.LogicalType
		}

		column.Nullable = true
		outputs = append(outputs, column)
	default:
		return nil, fmt.Errorf("unsupported operation kind %q", step.Operation.Kind)
	}
	for _, value := range step.RowValues {
		column, err := produced(value.OutputColumnID)
		if err != nil {
			return nil, err
		}
		outputs = append(outputs, column)
	}
	if err := validateStageColumns(outputs); err != nil {
		return nil, err
	}
	return outputs, nil
}

func findConstructionStep(steps []ConstructionStep, id string) int {
	for index, step := range steps {
		if step.ID == id {
			return index
		}
	}
	return -1
}

func findStageColumnByID(columns []StageColumn, id string) (StageColumn, bool) {
	for _, column := range columns {
		if column.ID == id {
			return column, true
		}
	}
	return StageColumn{}, false
}

func constructionAuthoredSourceIDs(document Document) map[string]struct{} {
	ids := make(map[string]struct{}, len(document.Columns))
	for _, column := range document.Columns {
		if column.ColumnID != "" {
			ids[column.ColumnID] = struct{}{}
		}
	}
	if document.Construction != nil {
		for _, projection := range document.Construction.SourceProjections {
			if projection.ColumnID != "" {
				ids[projection.ColumnID] = struct{}{}
			}
		}
	}
	return ids
}

func cloneTablePresentation(presentation *TablePresentation) *TablePresentation {
	if presentation == nil {
		return nil
	}
	cloned := *presentation
	return &cloned
}

func stagedSourceColumnID(outputID, commandID string, commandIndex int, commandType string, identity ...any) string {
	values := make([]any, 0, len(identity)+5)
	values = append(values, "staged-source-column/v1", outputID, commandID, commandIndex, commandType)
	values = append(values, identity...)
	return CommandGeneratedID("source_", values...)
}

func cloneConstruction(construction *Construction) (*Construction, error) {
	if construction == nil {
		return nil, nil
	}
	raw, err := json.Marshal(construction)
	if err != nil {
		return nil, fmt.Errorf("clone construction: %w", err)
	}
	var cloned Construction
	if err := json.Unmarshal(raw, &cloned); err != nil {
		return nil, fmt.Errorf("clone construction: %w", err)
	}
	return &cloned, nil
}

func cloneDocumentForConstructionChange(document Document) Document {
	candidate := document
	if document.Columns == nil {
		candidate.Columns = nil
	} else {
		candidate.Columns = append([]Column{}, document.Columns...)
	}
	if document.Rows.Groups != nil {
		groups := *document.Rows.Groups
		groups.RowValues = append([]ExplicitGroupRowValue(nil), document.Rows.Groups.RowValues...)
		candidate.Rows.Groups = &groups
	}
	construction := *document.Construction
	construction.Steps = append([]ConstructionStep(nil), document.Construction.Steps...)
	for index := range construction.Steps {
		construction.Steps[index].Inputs = append([]ConstructionInputRef(nil), construction.Steps[index].Inputs...)
		construction.Steps[index].Outputs = append([]StageColumn(nil), construction.Steps[index].Outputs...)
	}
	candidate.Construction = &construction
	return candidate
}

func normalizeConstructionOutputOrder(document *Document) {
	if document == nil || document.Construction == nil {
		return
	}
	construction := *document.Construction

	construction.Steps = append([]ConstructionStep{}, document.Construction.Steps...)
	candidate := *document
	candidate.Construction = &construction
	source, err := effectiveConstructionSourceSchema(candidate)
	if err != nil {
		return
	}
	authoredSourceIDs := constructionAuthoredSourceIDs(candidate)
	for index := range construction.Steps {
		input, err := effectiveConstructionInputSchema(candidate, source, index)
		if err != nil {
			return
		}
		outputs, err := rebuildStageColumns(construction.Steps[index], input, authoredSourceIDs)
		if err != nil {
			return
		}
		construction.Steps[index].Outputs = outputs
	}
	document.Construction = &construction
}
