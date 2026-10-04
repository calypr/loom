package lower

import (
	"fmt"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/semantic"
)

func workspaceOutputIDs(output semantic.OutputPlan) []string {
	if output.Construction == nil {
		return nil
	}
	var ids []string
	for _, step := range output.Construction.Steps {
		for _, input := range step.Inputs {
			if input.Kind == recipe.ConstructionWorkspaceOutputInput {
				ids = append(ids, input.OutputID)
			}
		}
	}
	return ids
}

func workspaceOutputDependencyOrder(outputs []semantic.OutputPlan) ([]int, error) {
	indexes := make(map[string]int, len(outputs))
	for index, output := range outputs {
		if _, duplicate := indexes[output.Name]; duplicate {
			return nil, fmt.Errorf("duplicate workspace output %q", output.Name)
		}
		indexes[output.Name] = index
	}
	state := make([]uint8, len(outputs))
	order := make([]int, 0, len(outputs))
	var visit func(int) error
	visit = func(index int) error {
		switch state[index] {
		case 1:
			return fmt.Errorf("workspace output dependency cycle includes %q", outputs[index].Name)
		case 2:
			return nil
		}
		state[index] = 1
		for _, dependencyID := range workspaceOutputIDs(outputs[index]) {
			dependency, ok := indexes[dependencyID]
			if !ok {
				return fmt.Errorf("output %q references unknown workspace output %q", outputs[index].Name, dependencyID)
			}
			if dependency == index {
				return fmt.Errorf("output %q cannot consume itself", outputs[index].Name)
			}
			if err := visit(dependency); err != nil {
				return err
			}
		}
		state[index] = 2
		order = append(order, index)
		return nil
	}
	for index := range outputs {
		if err := visit(index); err != nil {
			return nil, err
		}
	}
	return order, nil
}

func compiledWorkspaceOutputSources(output semantic.OutputPlan, schemas map[string][]CompiledOutputColumn) []WorkspaceOutputSource {
	if output.Construction == nil {
		return nil
	}
	step, ok := output.Construction.TerminalCombineStep()
	if !ok {
		return nil
	}
	var sources []WorkspaceOutputSource
	for inputIndex, input := range step.Inputs {
		if input.Kind != recipe.ConstructionWorkspaceOutputInput {
			continue
		}
		sources = append(sources, WorkspaceOutputSource{
			InputIndex: inputIndex, OutputID: input.OutputID,
			Schema: CloneCompiledOutputSchema(schemas[input.OutputID]),
		})
	}
	return sources
}
