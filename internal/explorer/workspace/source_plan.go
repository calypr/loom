package workspace

import (
	"fmt"
	"strconv"
	"strings"
)

const maxImportedSourcePlanSteps = 4096

const maxImportedSourcePlanIDBytes = maxImportedSourcePlanSteps * 32

type ImportedSourceFieldBinding struct {
	FieldID  string `json:"fieldId"`
	ColumnID string `json:"columnId"`
}

type ImportedSourcePlan struct {
	RemovedStepIDs []string                     `json:"removedStepIds,omitempty"`
	NextRuleIndex  int                          `json:"nextRuleIndex,omitempty"`
	FieldBindings  []ImportedSourceFieldBinding `json:"fieldBindings,omitempty"`
	RuleAdditions  []ImportedSourceRuleAddition `json:"ruleAdditions,omitempty"`
	Steps          []ImportedSourceStep         `json:"steps"`
	Relationships  *ImportedRelationshipOverlay `json:"relationships,omitempty"`
}

type ImportedSourceRuleAddition struct {
	Kind         string                    `json:"kind"`
	OccurrenceID string                    `json:"occurrenceId,omitempty"`
	StepID       string                    `json:"stepId"`
	Parameters   *ImportedSourceParameters `json:"parameters"`
	MaxColumns   *int                      `json:"maxColumns,omitempty"`
}

type ImportedSourceStep struct {
	Parameters   *ImportedSourceParameters `json:"parameters,omitempty"`
	ID           string                    `json:"id"`
	SourceKey    string                    `json:"sourceKey,omitempty"`
	OccurrenceID string                    `json:"occurrenceId,omitempty"`
	MatchMode    RouteMatchMode            `json:"matchMode,omitempty"`
	ParentID     string                    `json:"parentId,omitempty"`
	Kind         string                    `json:"kind"`
	Title        string                    `json:"title"`
	Summary      string                    `json:"summary"`
	Editable     bool                      `json:"editable"`
	MaxColumns   *int                      `json:"maxColumns,omitempty"`
	ValueMode    string                    `json:"valueMode,omitempty"`
}

func (p ImportedSourcePlan) Validate() error {
	if p.Relationships != nil {
		if err := p.Relationships.Validate(); err != nil {
			return err
		}
	}
	seenFields, seenColumns := map[string]bool{}, map[string]bool{}
	for _, binding := range p.FieldBindings {
		if err := validateImportedSourcePlanText("fieldBindings.fieldId", binding.FieldID, maxImportedSourcePlanIDBytes); err != nil {
			return err
		}
		if err := validateImportedSourcePlanText("fieldBindings.columnId", binding.ColumnID, 256); err != nil {
			return err
		}
		if seenFields[binding.FieldID] || seenColumns[binding.ColumnID] {
			return fmt.Errorf("source field bindings require unique field and column identities")
		}
		seenFields[binding.FieldID] = true
		seenColumns[binding.ColumnID] = true
	}
	if len(p.Steps) > maxImportedSourcePlanSteps {
		return fmt.Errorf("imported source plan has more than %d steps", maxImportedSourcePlanSteps)
	}
	if len(p.RuleAdditions) > 128 {
		return fmt.Errorf("source plan has more than 128 rule additions")
	}
	if p.NextRuleIndex < 0 || len(p.RemovedStepIDs) > maxImportedSourcePlanSteps {
		return fmt.Errorf("invalid imported source removal metadata")
	}
	removedSteps := make(map[string]bool, len(p.RemovedStepIDs))
	for _, id := range p.RemovedStepIDs {
		if err := validateImportedSourcePlanText("removedStepIds", id, maxImportedSourcePlanIDBytes); err != nil {
			return err
		}
		if removedSteps[id] {
			return fmt.Errorf("duplicate removed step %q", id)
		}
		removedSteps[id] = true
	}
	rules := make(map[string]ImportedSourceRuleAddition, len(p.RuleAdditions))
	lastRuleIndex := -1
	for index, addition := range p.RuleAdditions {
		ruleIndex, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(addition.StepID, "ruleAdditions["), "]"))
		if err != nil || ruleIndex <= lastRuleIndex || addition.StepID != fmt.Sprintf("ruleAdditions[%d]", ruleIndex) ||
			(p.NextRuleIndex == 0 && ruleIndex != index) || (p.NextRuleIndex != 0 && ruleIndex >= p.NextRuleIndex) {
			return fmt.Errorf("ruleAdditions[%d].stepId must be a stable append-order identity", index)
		}
		lastRuleIndex = ruleIndex
		if err := (SourceRuleCreate{Kind: addition.Kind, OccurrenceID: addition.OccurrenceID, Parameters: addition.Parameters, MaxColumns: addition.MaxColumns}).Validate(); err != nil {
			return fmt.Errorf("ruleAdditions[%d]: %w", index, err)
		}
		rules[addition.StepID] = addition
	}

	removed := map[string]bool{}
	if p.Relationships != nil {
		for _, occurrence := range p.Relationships.Removals {
			removed[occurrence] = true
		}
	}
	available := map[string]bool{}
	disabled := map[string]bool{}
	seen := make(map[string]struct{}, len(p.Steps))
	for index, step := range p.Steps {
		path := fmt.Sprintf("steps[%d]", index)
		if err := step.Parameters.Validate(step.Kind); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		if err := validateImportedSourcePlanText(path+".id", step.ID, maxImportedSourcePlanIDBytes); err != nil {
			return err
		}
		if removedSteps[step.ID] {
			return fmt.Errorf("removed source step %q is still present", step.ID)
		}
		if _, duplicate := seen[step.ID]; duplicate {
			return fmt.Errorf("%s.id duplicates %q", path, step.ID)
		}
		if step.ParentID != "" {
			if err := validateImportedSourcePlanText(path+".parentId", step.ParentID, maxImportedSourcePlanIDBytes); err != nil {
				return err
			}
			if _, exists := seen[step.ParentID]; !exists {
				return fmt.Errorf("%s.parentId %q must identify an earlier source-plan step", path, step.ParentID)
			}
		}
		if err := validateImportedSourcePlanText(path+".kind", step.Kind, 64); err != nil {
			return err
		}
		if err := validateImportedSourcePlanText(path+".title", step.Title, 256); err != nil {
			return err
		}
		if err := validateImportedSourcePlanText(path+".summary", step.Summary, 2048); err != nil {
			return err
		}
		if step.SourceKey != "" {
			if err := validateImportedSourcePlanText(path+".sourceKey", step.SourceKey, 256); err != nil {
				return err
			}
		}
		if step.OccurrenceID != "" {
			if err := validateImportedSourcePlanText(path+".occurrenceId", step.OccurrenceID, 256); err != nil {
				return err
			}
			if step.Kind == "TRAVERSAL" {
				disabled[step.ID] = disabled[step.ParentID] || removed[step.OccurrenceID]
				available[step.OccurrenceID] = !disabled[step.ID]
			} else if rule, exists := rules[step.ID]; !exists || rule.OccurrenceID != step.OccurrenceID {
				return fmt.Errorf("%s.occurrenceId requires a traversal or source rule descriptor", path)
			}
		}
		if step.MatchMode != "" && (step.Kind != "TRAVERSAL" || step.MatchMode != RouteMatchOptional && step.MatchMode != RouteMatchRequired) {
			return fmt.Errorf("%s.matchMode requires a traversal OPTIONAL or REQUIRED descriptor", path)
		}
		if step.MaxColumns != nil && *step.MaxColumns < 1 {
			return fmt.Errorf("%s.maxColumns must be positive", path)
		}
		if step.ValueMode != "" && !validImportedSourceValueMode(step.ValueMode) {
			return fmt.Errorf("%s.valueMode %q is unsupported", path, step.ValueMode)
		}
		if !step.Editable && (step.MaxColumns != nil || step.ValueMode != "") {
			return fmt.Errorf("%s cannot override a read-only step", path)
		}
		seen[step.ID] = struct{}{}
	}
	for _, rule := range p.RuleAdditions {
		if rule.OccurrenceID != "" && !available[rule.OccurrenceID] {
			return &UnavailableSourceRuleOccurrence{StepID: rule.StepID, OccurrenceID: rule.OccurrenceID}
		}
	}
	return nil
}

func validateImportedSourcePlanText(path, value string, maxLength int) error {
	if strings.TrimSpace(value) == "" || value != strings.TrimSpace(value) {
		return fmt.Errorf("%s must be non-empty and trimmed", path)
	}
	if len(value) > maxLength {
		return fmt.Errorf("%s exceeds %d bytes", path, maxLength)
	}
	return nil
}

func validImportedSourceValueMode(value string) bool {
	switch value {
	case "AUTO", "FIRST", "ALL", "DISTINCT", "VALUE":
		return true
	default:
		return false
	}
}
