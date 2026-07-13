package ir

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
