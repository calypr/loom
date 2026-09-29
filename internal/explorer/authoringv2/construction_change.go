package authoringv2

import (
	"encoding/json"
	"fmt"
	"reflect"
)

type ConstructionDependencyIssue struct {
	StepID   string `json:"stepId"`
	ColumnID string `json:"columnId"`
}

// ConstructionImpact describes the stages that must be recalculated for a
// proposed edit or removal and any stable column references that need repair.
type ConstructionImpact struct {
	ChangedStepID   string                        `json:"changedStepId,omitempty"`
	RemovedStepIDs  []string                      `json:"removedStepIds,omitempty"`
	AffectedStepIDs []string                      `json:"affectedStepIds"`
	MissingInputs   []ConstructionDependencyIssue `json:"missingInputs,omitempty"`
}

func (i ConstructionImpact) HasMissingInputs() bool {
	return len(i.MissingInputs) != 0
}

// AnalyzeStepEdit returns an isolated candidate document with the selected
// stable step ID replaced. Later stage schemas are recalculated from the
// candidate. Missing column references are reported instead of cascading
// deletion or invalidating the accepted document.
func (d Document) AnalyzeStepEdit(replacement ConstructionStep) (Document, ConstructionImpact, error) {
	if d.Construction == nil {
		return d, ConstructionImpact{}, fmt.Errorf("document has no staged construction")
	}
	if err := d.Validate(); err != nil {
		return d, ConstructionImpact{}, fmt.Errorf("validate accepted document: %w", err)
	}
	index := findConstructionStep(d.Construction.Steps, replacement.ID)
	if index < 0 {
		return d, ConstructionImpact{}, fmt.Errorf("step %q does not exist", replacement.ID)
	}
	candidate := cloneDocumentForConstructionChange(d)
	candidate.Construction.Steps[index] = replacement
	impact := ConstructionImpact{ChangedStepID: replacement.ID, AffectedStepIDs: make([]string, 0)}
	for i := index + 1; i < len(candidate.Construction.Steps); i++ {
		impact.AffectedStepIDs = append(impact.AffectedStepIDs, candidate.Construction.Steps[i].ID)
	}
	if err := recalculateCandidateStages(&candidate, index, &impact); err != nil {
		return d, ConstructionImpact{}, err
	}
	if !impact.HasMissingInputs() {
		if err := candidate.Validate(); err != nil {
			return d, ConstructionImpact{}, fmt.Errorf("edited construction is invalid: %w", err)
		}
	}
	return candidate, impact, nil
}

// AnalyzeConstructionCandidate accepts one complete proposal and verifies its
// identity-level changes against the saved sequence. It supports replacing one
// existing step, appending one new step, and removing only explicitly named
// steps. Surviving steps retain their order; downstream edits remain available
// for repairing dependencies in the submitted candidate.
func (d Document) AnalyzeConstructionCandidate(candidateConstruction Construction, changedStepID string, removeStepIDs []string) (Document, ConstructionImpact, error) {
	base := d
	if d.Construction == nil {
		var err error
		base, err = UpgradeDocumentToConstruction(d)
		if err != nil {
			return d, ConstructionImpact{}, fmt.Errorf("upgrade accepted document: %w", err)
		}
	} else if err := d.Validate(); err != nil {
		return d, ConstructionImpact{}, fmt.Errorf("validate accepted document: %w", err)
	}
	if candidateConstruction.Version != ConstructionVersion {
		return d, ConstructionImpact{}, fmt.Errorf("unsupported construction version %d", candidateConstruction.Version)
	}
	if changedStepID != "" && !requiredID(changedStepID) {
		return d, ConstructionImpact{}, fmt.Errorf("changedStepID must be an exact non-empty token")
	}
	remove := make(map[string]bool, len(removeStepIDs))
	removedOrder := make([]string, 0, len(removeStepIDs))
	for _, id := range removeStepIDs {
		if !requiredID(id) {
			return d, ConstructionImpact{}, fmt.Errorf("removeStepIDs contains an empty step id")
		}
		if remove[id] {
			return d, ConstructionImpact{}, fmt.Errorf("step %q is selected for removal more than once", id)
		}
		if findConstructionStep(base.Construction.Steps, id) < 0 {
			return d, ConstructionImpact{}, fmt.Errorf("step %q does not exist", id)
		}
		remove[id] = true
	}
	if changedStepID != "" && remove[changedStepID] {
		return d, ConstructionImpact{}, fmt.Errorf("changed step %q cannot also be removed", changedStepID)
	}
	for _, step := range base.Construction.Steps {
		if step.OwnerStepID != "" && remove[step.OwnerStepID] {
			remove[step.ID] = true
		}
	}

	cloned, err := cloneConstruction(&candidateConstruction)
	if err != nil {
		return d, ConstructionImpact{}, err
	}
	if len(remove) != 0 {
		steps := make([]ConstructionStep, 0, len(cloned.Steps))
		for _, step := range cloned.Steps {
			if remove[step.ID] && step.OwnerStepID != "" && remove[step.OwnerStepID] {
				continue
			}
			steps = append(steps, step)
		}
		cloned.Steps = steps
	}
	candidateSteps := cloned.Steps
	candidateIDs := make(map[string]int, len(candidateSteps))
	for index, step := range candidateSteps {
		if !requiredID(step.ID) {
			return d, ConstructionImpact{}, fmt.Errorf("candidate steps[%d].id is required", index)
		}
		if _, exists := candidateIDs[step.ID]; exists {
			return d, ConstructionImpact{}, fmt.Errorf("candidate contains duplicate step id %q", step.ID)
		}
		candidateIDs[step.ID] = index
	}
	changedIndex := findConstructionStep(base.Construction.Steps, changedStepID)
	isAppend := changedStepID != "" && changedIndex < 0
	if changedStepID != "" && changedIndex >= 0 {
		for _, oldStep := range base.Construction.Steps {
			if oldStep.OwnerStepID == changedStepID {
				if _, exists := candidateIDs[oldStep.ID]; !exists {
					remove[oldStep.ID] = true
				}
			}
		}
	}
	if changedStepID != "" {
		if _, exists := candidateIDs[changedStepID]; !exists {
			return d, ConstructionImpact{}, fmt.Errorf("candidate does not contain changed step %q", changedStepID)
		}
	}

	projectionOwners := make(map[string]bool, len(remove)+1)
	if changedStepID != "" {
		projectionOwners[changedStepID] = true
	}
	for id := range remove {
		projectionOwners[id] = true
	}
	impact := ConstructionImpact{ChangedStepID: changedStepID, RemovedStepIDs: make([]string, 0, len(remove)), AffectedStepIDs: make([]string, 0)}
	expectedIndex := 0
	firstChanged := len(base.Construction.Steps)
	for oldIndex, oldStep := range base.Construction.Steps {
		if remove[oldStep.ID] {
			impact.RemovedStepIDs = append(impact.RemovedStepIDs, oldStep.ID)
			if oldIndex < firstChanged {
				firstChanged = oldIndex
			}
			continue
		}
		for expectedIndex < len(candidateSteps) && isNewOwnedPivotInput(candidateSteps[expectedIndex], changedStepID, base.Construction.Steps) {
			expectedIndex++
		}
		if expectedIndex >= len(candidateSteps) || candidateSteps[expectedIndex].ID != oldStep.ID {
			return d, ConstructionImpact{}, fmt.Errorf("candidate changes step order or omits step %q without listing it in removeStepIDs", oldStep.ID)
		}
		if oldStep.ID == changedStepID {
			if oldIndex < firstChanged {
				firstChanged = oldIndex
			}
		} else if oldIndex < firstChanged && !reflect.DeepEqual(oldStep, candidateSteps[expectedIndex]) {
			if !constructionCanCarrySourceProjection(oldStep.Operation.Kind) ||
				!stepDiffOnlyChangesOwnedProjection(oldStep, candidateSteps[expectedIndex], projectionOwners,
					base.Construction.SourceProjections, cloned.SourceProjections) {
				return d, ConstructionImpact{}, fmt.Errorf("candidate changes step %q before the changed step", oldStep.ID)
			}
		}
		expectedIndex++
	}
	if isAppend {
		for expectedIndex < len(candidateSteps) && isNewOwnedPivotInput(candidateSteps[expectedIndex], changedStepID, base.Construction.Steps) {
			expectedIndex++
		}
		if expectedIndex != len(candidateSteps)-1 || candidateSteps[expectedIndex].ID != changedStepID {
			return d, ConstructionImpact{}, fmt.Errorf("new changed step %q must be appended at the end", changedStepID)
		}
		firstChanged = len(base.Construction.Steps)
		expectedIndex++
	}
	if expectedIndex != len(candidateSteps) {
		return d, ConstructionImpact{}, fmt.Errorf("candidate includes an unrequested new step")
	}
	if changedStepID == "" && len(remove) == 0 {
		return d, ConstructionImpact{}, fmt.Errorf("candidate must replace or append a step, or explicitly remove steps")
	}
	for _, oldStep := range base.Construction.Steps {
		if remove[oldStep.ID] {
			removedOrder = append(removedOrder, oldStep.ID)
		}
	}
	impact.RemovedStepIDs = removedOrder

	result := cloneDocumentForConstructionChange(base)
	result.Construction = cloned
	result.TableShape = nil
	for index := range result.Construction.Steps {
		step := &result.Construction.Steps[index]
		if step.Operation.Kind == ConstructionOperationCombine || step.Operation.Combine != nil {
			continue
		}
		if index == 0 {
			step.Inputs = []ConstructionInputRef{{Kind: ConstructionInputSourceProjection}}
		} else {
			step.Inputs = []ConstructionInputRef{{Kind: ConstructionInputStepOutput, StepID: result.Construction.Steps[index-1].ID}}
		}
	}
	result.Construction.SourceProjections = pruneConstructionSourceProjections(result.Construction.SourceProjections, result.Construction.Steps)
	start := len(result.Construction.Steps)
	if changedStepID != "" {
		start = candidateIDs[changedStepID]
	}
	for index, step := range result.Construction.Steps {
		if isNewOwnedPivotInput(step, changedStepID, base.Construction.Steps) && index < start {
			start = index
		}
	}
	if len(remove) != 0 {
		firstRemovedIndex := len(base.Construction.Steps)
		for index, step := range base.Construction.Steps {
			if remove[step.ID] {
				firstRemovedIndex = index
				break
			}
		}
		for index := firstRemovedIndex; index < len(base.Construction.Steps); index++ {
			step := base.Construction.Steps[index]
			if remove[step.ID] {
				continue
			}
			if affectedIndex, exists := candidateIDs[step.ID]; exists && affectedIndex < start {
				start = affectedIndex
			}
			break
		}
	}
	if len(base.Construction.SourceProjections) != len(result.Construction.SourceProjections) || (len(base.Construction.SourceProjections) > 0 && !reflect.DeepEqual(base.Construction.SourceProjections, result.Construction.SourceProjections)) {
		start = 0
	}
	for index := start; index < len(result.Construction.Steps); index++ {
		impact.AffectedStepIDs = append(impact.AffectedStepIDs, result.Construction.Steps[index].ID)
	}
	if start < len(result.Construction.Steps) {
		if err := recalculateCandidateStages(&result, start, &impact); err != nil {
			return d, ConstructionImpact{}, err
		}
	}
	if !impact.HasMissingInputs() {
		if err := result.Validate(); err != nil {
			return d, ConstructionImpact{}, fmt.Errorf("candidate construction is invalid: %w", err)
		}
	}
	return result, impact, nil
}

// ProposeStepRemoval removes only the selected step and the downstream steps
// explicitly named in removeStepIDs. It retains unrelated later steps, rewires
// their stage input to the nearest surviving stage, and reports missing column
// references for repair before application.
func (d Document) ProposeStepRemoval(stepID string, removeStepIDs []string) (Document, ConstructionImpact, error) {
	if d.Construction == nil {
		return d, ConstructionImpact{}, fmt.Errorf("document has no staged construction")
	}
	if err := d.Validate(); err != nil {
		return d, ConstructionImpact{}, fmt.Errorf("validate accepted document: %w", err)
	}
	selectedIndex := findConstructionStep(d.Construction.Steps, stepID)
	if selectedIndex < 0 {
		return d, ConstructionImpact{}, fmt.Errorf("step %q does not exist", stepID)
	}
	remove := map[string]bool{stepID: true}
	for _, id := range removeStepIDs {
		if !requiredID(id) {
			return d, ConstructionImpact{}, fmt.Errorf("removeStepIDs contains an empty step id")
		}
		dependentIndex := findConstructionStep(d.Construction.Steps, id)
		if dependentIndex < 0 {
			return d, ConstructionImpact{}, fmt.Errorf("step %q does not exist", id)
		}
		if dependentIndex < selectedIndex {
			return d, ConstructionImpact{}, fmt.Errorf("step %q is before the selected removal", id)
		}
		if remove[id] {
			return d, ConstructionImpact{}, fmt.Errorf("step %q is selected for removal more than once", id)
		}
		remove[id] = true
	}
	for _, step := range d.Construction.Steps {
		if step.OwnerStepID != "" && remove[step.OwnerStepID] {
			remove[step.ID] = true
		}
	}
	firstRemoved := len(d.Construction.Steps)
	for index, step := range d.Construction.Steps {
		if remove[step.ID] && index < firstRemoved {
			firstRemoved = index
		}
	}
	candidate := cloneDocumentForConstructionChange(d)
	steps := make([]ConstructionStep, 0, len(candidate.Construction.Steps)-len(remove))
	impact := ConstructionImpact{RemovedStepIDs: make([]string, 0, len(remove)), AffectedStepIDs: make([]string, 0)}
	for _, step := range candidate.Construction.Steps {
		if remove[step.ID] {
			impact.RemovedStepIDs = append(impact.RemovedStepIDs, step.ID)
			continue
		}
		steps = append(steps, step)
	}
	candidate.Construction.Steps = steps
	candidate.Construction.SourceProjections = pruneConstructionSourceProjections(candidate.Construction.SourceProjections, steps)
	for index := firstRemoved; index < len(candidate.Construction.Steps); index++ {
		step := &candidate.Construction.Steps[index]
		impact.AffectedStepIDs = append(impact.AffectedStepIDs, step.ID)
		if index == 0 {
			step.Inputs = []ConstructionInputRef{{Kind: ConstructionInputSourceProjection}}
		} else {
			step.Inputs = []ConstructionInputRef{{Kind: ConstructionInputStepOutput, StepID: candidate.Construction.Steps[index-1].ID}}
		}
	}
	recalculateFrom := firstRemoved
	if len(d.Construction.SourceProjections) != len(candidate.Construction.SourceProjections) || (len(d.Construction.SourceProjections) > 0 && !reflect.DeepEqual(d.Construction.SourceProjections, candidate.Construction.SourceProjections)) {
		recalculateFrom = 0
	}
	if recalculateFrom < len(candidate.Construction.Steps) {
		if err := recalculateCandidateStages(&candidate, recalculateFrom, &impact); err != nil {
			return d, ConstructionImpact{}, err
		}
	}
	if !impact.HasMissingInputs() {
		if err := candidate.Validate(); err != nil {
			return d, ConstructionImpact{}, fmt.Errorf("removed construction is invalid: %w", err)
		}
	}
	return candidate, impact, nil
}

func recalculateCandidateStages(candidate *Document, start int, impact *ConstructionImpact) error {
	source, err := sourceStageColumns(candidate.Columns)
	if err != nil {
		return err
	}
	if start == 0 && len(candidate.Construction.Steps) > 0 {
		first := candidate.Construction.Steps[0]
		source, err = constructionSourceColumnsWithChildrenAndProjections(candidate.Columns, candidate.Construction.SourceProjections, first.Operation.inputColumnIDs(), first.Outputs)
		if err != nil {
			return fmt.Errorf("source projection: %w", err)
		}
	}
	for index := start; index < len(candidate.Construction.Steps); index++ {
		step := &candidate.Construction.Steps[index]
		if step.Operation.Kind == ConstructionOperationCombine || step.Operation.Combine != nil {
			if index != 0 || len(candidate.Construction.Steps) != 1 {
				return fmt.Errorf("COMBINE must remain the only construction step")
			}
			continue
		}
		inputColumns, err := constructionStepInputSchema(candidate.Construction.Steps, source, index)
		if err != nil {
			return fmt.Errorf("step %q input: %w", step.ID, err)
		}
		inputIndex, err := stageColumnIndex(inputColumns)
		if err != nil {
			return fmt.Errorf("step %q input schema: %w", step.ID, err)
		}
		for _, columnID := range step.Operation.inputColumnIDs() {
			if _, exists := inputIndex[columnID]; !exists {
				impact.MissingInputs = append(impact.MissingInputs, ConstructionDependencyIssue{StepID: step.ID, ColumnID: columnID})
			}
		}
		outputs, err := rebuildStageColumns(*step, inputColumns)
		if err != nil {
			return fmt.Errorf("recalculate step %q: %w", step.ID, err)
		}
		step.Outputs = outputs
	}
	return nil
}

func pruneConstructionSourceProjections(projections []ConstructionSourceProjection, steps []ConstructionStep) []ConstructionSourceProjection {
	used := make(map[string]bool)
	owners := make(map[string]bool, len(steps))
	for _, step := range steps {
		owners[step.ID] = step.Operation.Kind == ConstructionOperationPivot
		for _, columnID := range step.Operation.inputColumnIDs() {
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

func isNewOwnedPivotInput(step ConstructionStep, ownerStepID string, existing []ConstructionStep) bool {
	if ownerStepID == "" || step.OwnerStepID != ownerStepID || step.Operation.Kind != ConstructionOperationRelatedField {
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

func rebuildStageColumns(step ConstructionStep, input []StageColumn) ([]StageColumn, error) {
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
		// The terminal record can be absent after PRESERVE_PARENT expansion.
		column.Nullable = true
		outputs = append(outputs, column)
	default:
		return nil, fmt.Errorf("unsupported operation kind %q", step.Operation.Kind)
	}
	if err := validateStageColumns(outputs); err != nil {
		return nil, err
	}
	return outputs, nil
}

func (o ConstructionOperation) inputColumnIDs() []string {
	ids := make([]string, 0)
	add := func(id string) {
		if !requiredID(id) {
			return
		}
		for _, present := range ids {
			if present == id {
				return
			}
		}
		ids = append(ids, id)
	}
	switch o.Kind {
	case ConstructionOperationPivot:
		if o.Pivot != nil {
			for _, id := range o.Pivot.GroupKeyIDs {
				add(id)
			}
			add(o.Pivot.CategoryColumnID)
			add(o.Pivot.ValueColumnID)
		}
	case ConstructionOperationDerive:
		if o.Derive != nil {
			if o.Derive.Left.Kind == ConstructionColumnOperand {
				add(o.Derive.Left.ColumnID)
			}
			if o.Derive.Right.Kind == ConstructionColumnOperand {
				add(o.Derive.Right.ColumnID)
			}
		}
	case ConstructionOperationFilter:
		if o.Filter != nil {
			add(o.Filter.ColumnID)
		}
	case ConstructionOperationUnpivot:
		if o.Unpivot != nil {
			for _, input := range o.Unpivot.Inputs {
				add(input.ColumnID)
			}
		}
	case ConstructionOperationGroup:
		if o.Group != nil {
			for _, key := range o.Group.Keys {
				add(key.InputColumnID)
			}
			for _, aggregate := range o.Group.Aggregates {
				add(aggregate.InputColumnID)
			}
		}
	case ConstructionOperationExpand:
		if o.Expand != nil {
			add(o.Expand.InputColumnID)
		}
	case ConstructionOperationRelatedSource:
		if o.RelatedSource != nil {
			if o.RelatedSource.AnchorColumnID != "_key" {
				add(o.RelatedSource.AnchorColumnID)
			}
		}
	}
	return ids
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

func stagedSourceColumnID(document Document, commandID string, commandIndex int, commandType string, identity ...any) string {
	if document.Construction == nil {
		return ""
	}
	values := make([]any, 0, len(identity)+5)
	values = append(values, "staged-source-column/v1", document.Output.ID, commandID, commandIndex, commandType)
	values = append(values, identity...)
	return commandGeneratedID("source_", values...)
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
	candidate.Columns = append([]Column(nil), document.Columns...)
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
	construction.Steps = append([]ConstructionStep(nil), document.Construction.Steps...)
	source, err := sourceStageColumns(document.Columns)
	if err != nil {
		return
	}
	if len(construction.Steps) > 0 {
		first := construction.Steps[0]
		source, err = constructionSourceColumnsWithChildrenAndProjections(document.Columns, construction.SourceProjections, first.Operation.inputColumnIDs(), first.Outputs)
		if err != nil {
			return
		}
	}
	for index := range construction.Steps {
		input := source
		if index > 0 {
			input = construction.Steps[index-1].Outputs
		}
		outputs, err := rebuildStageColumns(construction.Steps[index], input)
		if err != nil {
			return
		}
		construction.Steps[index].Outputs = outputs
	}
	document.Construction = &construction
}
