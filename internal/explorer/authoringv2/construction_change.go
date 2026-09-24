package authoringv2

import (
	"fmt"
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
	for index := firstRemoved; index < len(candidate.Construction.Steps); index++ {
		step := &candidate.Construction.Steps[index]
		impact.AffectedStepIDs = append(impact.AffectedStepIDs, step.ID)
		if index == 0 {
			step.Inputs = []ConstructionInputRef{{Kind: ConstructionInputSourceProjection}}
		} else {
			step.Inputs = []ConstructionInputRef{{Kind: ConstructionInputStepOutput, StepID: candidate.Construction.Steps[index-1].ID}}
		}
	}
	if firstRemoved < len(candidate.Construction.Steps) {
		if err := recalculateCandidateStages(&candidate, firstRemoved, &impact); err != nil {
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
	for index := start; index < len(candidate.Construction.Steps); index++ {
		step := &candidate.Construction.Steps[index]
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
