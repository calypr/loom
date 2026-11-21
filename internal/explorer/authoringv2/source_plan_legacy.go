package authoringv2

import "slices"

// Essential: Drafts from earlier releases stored removed rules as disabled steps.
func (p *ImportedSourcePlan) UnmarshalJSON(raw []byte) error {
	type plan ImportedSourcePlan
	var wire struct {
		*plan
		Steps []struct {
			ImportedSourceStep
			LegacyEnabled *bool `json:"enabled"`
		} `json:"steps"`
		RuleAdditions []struct {
			ImportedSourceRuleAddition
			LegacyEnabled *bool `json:"enabled"`
		} `json:"ruleAdditions"`
	}
	var decoded ImportedSourcePlan
	wire.plan = (*plan)(&decoded)
	if err := strictDecode(raw, &wire); err != nil {
		return err
	}
	removed := map[string]bool{}
	for _, addition := range wire.RuleAdditions {
		if addition.LegacyEnabled != nil && !*addition.LegacyEnabled {
			removed[addition.StepID] = true
			if decoded.NextRuleIndex == 0 {
				decoded.NextRuleIndex = len(wire.RuleAdditions)
			}
			continue
		}
		decoded.RuleAdditions = append(decoded.RuleAdditions, addition.ImportedSourceRuleAddition)
	}
	decoded.Steps = make([]ImportedSourceStep, 0, len(wire.Steps))
	for _, step := range wire.Steps {
		if removed[step.ID] {
			continue
		}
		if removed[step.ParentID] {
			removed[step.ID] = true
			continue
		}
		if step.LegacyEnabled != nil && !*step.LegacyEnabled {
			removed[step.ID] = true
			decoded.RemovedStepIDs = append(decoded.RemovedStepIDs, step.ID)
			continue
		}
		decoded.Steps = append(decoded.Steps, step.ImportedSourceStep)
	}
	decoded.RuleAdditions = slices.DeleteFunc(decoded.RuleAdditions, func(addition ImportedSourceRuleAddition) bool {
		if !removed[addition.StepID] {
			return false
		}
		if decoded.NextRuleIndex == 0 {
			decoded.NextRuleIndex = len(wire.RuleAdditions)
		}
		return true
	})
	*p = decoded
	return nil
}
