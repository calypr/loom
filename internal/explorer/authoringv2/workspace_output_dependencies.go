package authoringv2

import "fmt"

// OutputDependencyOrder returns document indexes in deterministic dependency
// order. References are to stable output IDs in this exact workspace; it does
// not consult publications or mutable table heads.
func (w Workspace) OutputDependencyOrder() ([]int, error) {
	indexes := make(map[string]int, len(w.Documents))
	for index, document := range w.Documents {
		if prior, duplicate := indexes[document.Output.ID]; duplicate {
			return nil, fmt.Errorf("DUPLICATE_OUTPUT_ID: documents[%d].output.id duplicates documents[%d]", index, prior)
		}
		indexes[document.Output.ID] = index
	}
	state := make([]uint8, len(w.Documents))
	order := make([]int, 0, len(w.Documents))
	var visit func(int) error
	visit = func(index int) error {
		switch state[index] {
		case 1:
			return fmt.Errorf("WORKSPACE_OUTPUT_CYCLE: output %q participates in a workspace output dependency cycle", w.Documents[index].Output.ID)
		case 2:
			return nil
		}
		state[index] = 1
		document := w.Documents[index]
		if document.Construction != nil {
			for stepIndex, step := range document.Construction.Steps {
				for inputIndex, input := range step.Inputs {
					if input.Kind != ConstructionInputWorkspaceOutput {
						continue
					}
					dependency, ok := indexes[input.OutputID]
					if !ok {
						return fmt.Errorf("WORKSPACE_OUTPUT_NOT_FOUND: documents[%d].construction.steps[%d].inputs[%d] references unknown output %q", index, stepIndex, inputIndex, input.OutputID)
					}
					if dependency == index {
						return fmt.Errorf("WORKSPACE_OUTPUT_SELF_REFERENCE: output %q cannot consume itself", document.Output.ID)
					}
					if err := visit(dependency); err != nil {
						return err
					}
				}
			}
		}
		state[index] = 2
		order = append(order, index)
		return nil
	}
	for index := range w.Documents {
		if err := visit(index); err != nil {
			return nil, err
		}
	}
	return order, nil
}
