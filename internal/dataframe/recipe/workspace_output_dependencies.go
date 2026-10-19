package recipe

import "fmt"

// OutputDependencyOrder returns bundle output indexes in stable dependency
// order. The normal Validate path is closed over this bundle and therefore
// rejects missing sibling IDs rather than consulting a mutable publication.
func (b Bundle) OutputDependencyOrder() ([]int, error) {
	return b.outputDependencyOrder(nil)
}

func (b Bundle) outputDependencyOrder(allowedExternal map[string]struct{}) ([]int, error) {
	indexes := make(map[string]int, len(b.Outputs))
	for index, output := range b.Outputs {
		if prior, duplicate := indexes[output.Name]; duplicate {
			return nil, validationError("duplicate_name", fmt.Sprintf("$.outputs[%d].name", index), fmt.Sprintf("duplicate output name also used at $.outputs[%d].name", prior))
		}
		indexes[output.Name] = index
	}
	state := make([]uint8, len(b.Outputs))
	order := make([]int, 0, len(b.Outputs))
	var visit func(int) error
	visit = func(index int) error {
		switch state[index] {
		case 1:
			return validationError("workspace_output_cycle", fmt.Sprintf("$.outputs[%d]", index), fmt.Sprintf("output %q participates in a workspace output dependency cycle", b.Outputs[index].Name))
		case 2:
			return nil
		}
		state[index] = 1
		output := b.Outputs[index]
		if output.Construction != nil {
			if step, ok := output.Construction.TerminalCombineStep(); ok {
				for inputIndex, input := range step.Inputs {
					if input.Kind != ConstructionWorkspaceOutputInput {
						continue
					}
					path := fmt.Sprintf("$.outputs[%d].construction.steps[0].inputs[%d]", index, inputIndex)
					dependency, found := indexes[input.OutputID]
					if !found {
						if _, allowed := allowedExternal[input.OutputID]; allowed {
							continue
						}
						return validationError("workspace_output_not_found", path, fmt.Sprintf("outputId %q does not name an output in this bundle", input.OutputID))
					}
					if dependency == index {
						return validationError("workspace_output_self_reference", path, fmt.Sprintf("output %q cannot consume itself", output.Name))
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
	for index := range b.Outputs {
		if err := visit(index); err != nil {
			return nil, err
		}
	}
	return order, nil
}
