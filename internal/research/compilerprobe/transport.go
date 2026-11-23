package compilerprobe

import (
	"encoding/json"
	"fmt"

	"github.com/calypr/loom/internal/dataframe/recipe"
)

func RestoreTransport(v2 Bundle) (recipe.Bundle, error) {
	if v2.Version != 2 {
		return recipe.Bundle{}, fmt.Errorf("unexpected prototype version %d", v2.Version)
	}
	v1 := recipe.Bundle{RecipeSchemaVersion: 1, Name: v2.Name, TranslationVersion: v2.TranslationVersion, Fragments: v2.Fragments}
	for _, output := range v2.Outputs {
		restored, err := restoreOutput(output)
		if err != nil {
			return recipe.Bundle{}, fmt.Errorf("output %q: %w", output.Name, err)
		}
		v1.Outputs = append(v1.Outputs, restored)
	}
	return v1, nil
}

func restoreOutput(output Output) (recipe.Output, error) {
	if len(output.Steps) == 0 || output.Steps[0].Kind != "PROJECT" {
		return recipe.Output{}, fmt.Errorf("missing prototype PROJECT step")
	}
	project := output.Steps[0]
	var v1 recipe.Output
	if err := json.Unmarshal(project.Payload, &v1); err != nil {
		return recipe.Output{}, err
	}
	v1.Name, v1.RowGrain = output.Name, output.RowGrain
	v1.RootResourceType, v1.RootOccurrenceID, v1.Population = output.Root.ResourceType, output.Root.OccurrenceID, output.Root.Population
	if project.HasConstruction {
		v1.Construction = &recipe.Construction{Version: 1, SourceColumns: project.Outputs}
		if project.HasConstructionSteps {
			v1.Construction.Steps = []recipe.ConstructionStep{}
		}
	}
	for _, step := range output.Steps[1:] {
		switch step.Kind {
		case "GROUP_ROWS":
			if len(step.Inputs) != 1 || v1.GroupRows != nil {
				return recipe.Output{}, fmt.Errorf("invalid group rows input")
			}
			v1.GroupRows = &recipe.GroupRows{}
			if err := json.Unmarshal(step.Payload, v1.GroupRows); err != nil {
				return recipe.Output{}, err
			}
			v1.GroupRows.AfterStepID = step.Inputs[0].StepID
		case "LEGACY_DERIVE":
			var derived recipe.DerivedColumn
			if err := json.Unmarshal(step.Payload, &derived); err != nil {
				return recipe.Output{}, err
			}
			v1.DerivedColumns = append(v1.DerivedColumns, derived)
		default:
			key := operationKeys[step.Kind]
			if key == "" || v1.Construction == nil {
				return recipe.Output{}, fmt.Errorf("unexpected construction kind %q", step.Kind)
			}
			kind, _ := json.Marshal(step.Kind)
			payload, err := json.Marshal(map[string]json.RawMessage{"kind": kind, key: step.Payload})
			if err != nil {
				return recipe.Output{}, err
			}
			v1Step := recipe.ConstructionStep{ID: step.ID, Inputs: step.Inputs, Outputs: step.Outputs, RowValues: step.RowValues}
			if err = json.Unmarshal(payload, &v1Step.Operation); err != nil {
				return recipe.Output{}, err
			}
			v1.Construction.Steps = append(v1.Construction.Steps, v1Step)
		}
	}
	return v1, nil
}
