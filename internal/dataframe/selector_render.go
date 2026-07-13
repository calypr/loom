package dataframe

import (
	"fmt"
)

// selectorHasNoArrays and the helpers below are shared by the physical
// renderer and typed-filter compiler. They deliberately render only validated
// Selector values and do not know anything about storage-plan representation.
func selectorHasNoArrays(sel Selector) bool {
	for _, step := range sel.Steps {
		if step.Iterate || step.Index != nil {
			return false
		}
	}
	return true
}

func selectorHasIteratedArray(sel Selector) bool {
	for _, step := range sel.Steps {
		if step.Iterate {
			return true
		}
	}
	return false
}

func compileDirectExpr(rootVar string, steps []SelectorStep) string {
	cur := rootVar
	for _, step := range steps {
		if step.Index != nil {
			cur = fmt.Sprintf("((%s.%s ? %s.%s : [])[%d])", cur, step.Field, cur, step.Field, *step.Index)
			continue
		}
		cur = fmt.Sprintf("%s.%s", cur, step.Field)
	}
	return cur
}

func extractFinalExpr(cur string, step SelectorStep) string {
	switch {
	case step.Iterate:
		return fmt.Sprintf("(%s.%s ? %s.%s : [])", cur, step.Field, cur, step.Field)
	case step.Index != nil:
		return fmt.Sprintf("((%s.%s ? %s.%s : [])[%d])", cur, step.Field, cur, step.Field, *step.Index)
	default:
		return fmt.Sprintf("%s.%s", cur, step.Field)
	}
}
