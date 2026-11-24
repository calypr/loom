package workspace

import (
	"fmt"
	"reflect"
)

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

func (d Document) AnalyzeConstructionCandidate(candidateConstruction Construction, changedStepID string, removeStepIDs []string) (Document, ConstructionImpact, error) {
	base := d
	if d.Construction == nil {
		var err error
		base, err = StartDocumentConstruction(d)
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
	appendedRelatedSourceStart := -1
	for oldIndex, oldStep := range base.Construction.Steps {
		if remove[oldStep.ID] {
			impact.RemovedStepIDs = append(impact.RemovedStepIDs, oldStep.ID)
			if oldIndex < firstChanged {
				firstChanged = oldIndex
			}
			continue
		}
		for expectedIndex < len(candidateSteps) && isNewOwnedReshapeInput(candidateSteps[expectedIndex], changedStepID, base.Construction.Steps) {
			expectedIndex++
		}
		if expectedIndex >= len(candidateSteps) || candidateSteps[expectedIndex].ID != oldStep.ID {
			return d, ConstructionImpact{}, fmt.Errorf("candidate changes step order or omits step %q without listing it in removeStepIDs", oldStep.ID)
		}
		candidateStep := candidateSteps[expectedIndex]
		if oldStep.OwnerStepID == changedStepID && oldStep.Operation.Kind == ConstructionOperationCodedPivot &&
			candidateStep.OwnerStepID == changedStepID && candidateStep.Operation.Kind == ConstructionOperationCodedPivot &&
			!reflect.DeepEqual(oldStep, candidateStep) && oldIndex < firstChanged {

			firstChanged = oldIndex
		}
		if oldStep.ID == changedStepID {
			if oldIndex < firstChanged {
				firstChanged = oldIndex
			}
		} else if oldIndex < firstChanged && !reflect.DeepEqual(oldStep, candidateStep) {
			if !ConstructionCanCarrySourceProjection(oldStep.Operation.Kind) ||
				!stepDiffOnlyChangesOwnedProjection(oldStep, candidateSteps[expectedIndex], projectionOwners,
					base.Construction.SourceProjections, cloned.SourceProjections) {
				return d, ConstructionImpact{}, fmt.Errorf("candidate changes step %q before the changed step", oldStep.ID)
			}
		}
		expectedIndex++
	}
	if isAppend {
		for expectedIndex < len(candidateSteps) && isNewOwnedReshapeInput(candidateSteps[expectedIndex], changedStepID, base.Construction.Steps) {
			expectedIndex++
		}
		if expectedIndex == len(candidateSteps)-1 && candidateSteps[expectedIndex].ID == changedStepID {
			expectedIndex++
		} else if start, ok := appendedRelatedSourceSuffixStart(base.Construction.Steps, candidateSteps, expectedIndex, changedStepID); ok {
			appendedRelatedSourceStart = start
			expectedIndex = len(candidateSteps)
		} else {
			return d, ConstructionImpact{}, fmt.Errorf("new changed step %q must be appended at the end", changedStepID)
		}
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
	result.ArchiveRetiredShape = nil
	repairExplicitGroupAnchorAfterConstructionChange(base, &result)
	normalizeEffectiveConstructionInputs(&result)
	result.Construction.SourceProjections = pruneConstructionSourceProjections(result.Construction.SourceProjections, result.Construction.Steps)
	start := len(result.Construction.Steps)
	if changedStepID != "" {
		start = candidateIDs[changedStepID]
	}
	if appendedRelatedSourceStart >= 0 && appendedRelatedSourceStart < start {
		start = appendedRelatedSourceStart
	}
	for index, step := range result.Construction.Steps {
		if isNewOwnedReshapeInput(step, changedStepID, base.Construction.Steps) && index < start {
			start = index
		}
		if step.OwnerStepID == changedStepID && step.Operation.Kind == ConstructionOperationCodedPivot {
			if previousIndex := findConstructionStep(base.Construction.Steps, step.ID); previousIndex >= 0 &&
				!reflect.DeepEqual(base.Construction.Steps[previousIndex], step) && index < start {
				start = index
			}
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
	repairExplicitGroupAnchorAfterConstructionChange(d, &candidate)
	normalizeEffectiveConstructionInputs(&candidate)
	for index := firstRemoved; index < len(candidate.Construction.Steps); index++ {
		step := &candidate.Construction.Steps[index]
		impact.AffectedStepIDs = append(impact.AffectedStepIDs, step.ID)
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

func (workspace Workspace) DocumentIndex(outputID string) int {
	index := -1
	for candidate := range workspace.Documents {
		if workspace.Documents[candidate].Output.ID != outputID {
			continue
		}
		if index >= 0 {
			return -1
		}
		index = candidate
	}
	return index
}
