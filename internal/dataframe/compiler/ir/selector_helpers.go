package ir

import "github.com/calypr/loom/internal/dataframe/spec"

func selectorHasNoArrays(sel spec.Selector) bool {
	for _, step := range sel.Steps {
		if step.Iterate || step.Index != nil {
			return false
		}
	}
	return true
}

func selectorHasIteratedArray(sel spec.Selector) bool {
	for _, step := range sel.Steps {
		if step.Iterate {
			return true
		}
	}
	return false
}
