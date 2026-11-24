package compilerprobe

import (
	"encoding/json"
	"fmt"

	"github.com/calypr/loom/internal/dataframe/recipe"
)

type Bundle struct {
	Version            int                     `json:"recipeSchemaVersion"`
	Name               string                  `json:"name"`
	TranslationVersion string                  `json:"translationVersion"`
	Fragments          *recipe.FragmentLibrary `json:"fragments,omitempty"`
	Outputs            []Output                `json:"outputs"`
}

type Output struct {
	Name     string `json:"name"`
	RowGrain string `json:"rowGrain"`
	Root     Root   `json:"root"`
	Steps    []Step `json:"steps"`
}

type Root struct {
	ResourceType string                       `json:"resourceType"`
	OccurrenceID string                       `json:"occurrenceId,omitempty"`
	Population   *recipe.PopulationConstraint `json:"population,omitempty"`
}

type Step struct {
	HasConstructionSteps bool                          `json:"hasConstructionSteps,omitempty"`
	HasConstruction      bool                          `json:"hasConstruction,omitempty"`
	ID                   string                        `json:"id"`
	Kind                 string                        `json:"kind"`
	Inputs               []recipe.ConstructionInputRef `json:"inputs"`
	Outputs              []recipe.StageColumn          `json:"outputs"`
	RowValues            []recipe.ConstructionRowValue `json:"rowValues,omitempty"`
	Payload              json.RawMessage               `json:"payload"`
}

var operationKeys = map[string]string{
	"PIVOT": "pivot", "DERIVE": "derive", "FILTER": "filter", "UNPIVOT": "unpivot",
	"GROUP": "group", "CODED_GROUP": "codedGroup", "CODED_PIVOT": "codedPivot",
	"EXPAND": "expand", "COMBINE": "combine", "RELATED_SOURCE": "relatedSource",
	"RELATED_EXPAND": "relatedExpand", "RELATED_ELIGIBILITY": "relatedEligibility",
	"RELATED_FIELD": "relatedField",
}

var projectionKeys = []string{
	"rootColumnNaming", "traversalColumnNaming", "fields", "filters", "pivots", "ownerRecords",
	"aggregates", "slices", "traversals", "expand", "identity", "dynamicColumns",
	"extensionColumns", "catalogProjections", "columnTransformations", "collisionPolicy",
}

func Translate(v1 recipe.Bundle) (Bundle, error) {
	if err := v1.ValidateArchive(); err != nil {
		return Bundle{}, err
	}
	v2 := Bundle{Version: 2, Name: v1.Name, TranslationVersion: v1.TranslationVersion, Fragments: v1.Fragments}
	for _, output := range v1.Outputs {
		translated, err := translateOutput(output)
		if err != nil {
			return Bundle{}, fmt.Errorf("output %q: %w", output.Name, err)
		}
		v2.Outputs = append(v2.Outputs, translated)
	}
	return v2, nil
}

func translateOutput(v1 recipe.Output) (Output, error) {
	if v1.ArchiveRetiredReshape != nil {
		return Output{}, fmt.Errorf("archived tableReshape requires a separate semantic mapping")
	}
	encoded, err := json.Marshal(v1)
	if err != nil {
		return Output{}, err
	}
	fields := map[string]json.RawMessage{}
	if err = json.Unmarshal(encoded, &fields); err != nil {
		return Output{}, err
	}
	projection := map[string]json.RawMessage{}
	for _, key := range projectionKeys {
		if raw, ok := fields[key]; ok {
			projection[key] = raw
			delete(fields, key)
		}
	}
	for _, key := range []string{"name", "rowGrain", "rootResourceType", "rootOccurrenceId", "population", "construction", "groupRows", "derivedColumns"} {
		delete(fields, key)
	}
	if len(fields) != 0 {
		return Output{}, fmt.Errorf("unmapped output fields: %v", fields)
	}
	projected, err := json.Marshal(projection)
	if err != nil {
		return Output{}, err
	}
	v2 := Output{Name: v1.Name, RowGrain: v1.RowGrain, Root: Root{
		ResourceType: v1.RootResourceType, OccurrenceID: v1.RootOccurrenceID, Population: v1.Population,
	}}
	project := Step{ID: recipe.ConstructionSourceProjectionID, Kind: "PROJECT", Payload: projected}
	if v1.Construction != nil {
		project.Outputs = v1.Construction.SourceColumns
		project.HasConstruction = true
		project.HasConstructionSteps = v1.Construction.Steps != nil
	}
	v2.Steps = append(v2.Steps, project)

	addGroup := func() error {
		group := *v1.GroupRows
		group.AfterStepID = ""
		payload, err := json.Marshal(group)
		if err != nil {
			return err
		}
		prior := v2.Steps[len(v2.Steps)-1]
		input := recipe.ConstructionInputRef{Kind: recipe.ConstructionStepOutputInput, StepID: prior.ID}
		if prior.Kind == "PROJECT" {
			input = recipe.ConstructionInputRef{Kind: recipe.ConstructionSourceProjectionInput}
		}
		v2.Steps = append(v2.Steps, Step{ID: recipe.ConstructionCohortGroupStageID, Kind: "GROUP_ROWS", Inputs: []recipe.ConstructionInputRef{input}, Payload: payload})
		return nil
	}
	if v1.GroupRows != nil && v1.GroupRows.AfterStepID == "" {
		if err := addGroup(); err != nil {
			return Output{}, err
		}
	}
	if v1.Construction != nil {
		for _, step := range v1.Construction.Steps {
			payload, err := constructionPayload(step.Operation)
			if err != nil {
				return Output{}, err
			}
			v2.Steps = append(v2.Steps, Step{ID: step.ID, Kind: string(step.Operation.Kind), Inputs: step.Inputs, Outputs: step.Outputs, RowValues: step.RowValues, Payload: payload})
			if v1.GroupRows != nil && v1.GroupRows.AfterStepID == step.ID {
				if err := addGroup(); err != nil {
					return Output{}, err
				}
			}
		}
	}
	for i, derived := range v1.DerivedColumns {
		payload, err := json.Marshal(derived)
		if err != nil {
			return Output{}, err
		}
		v2.Steps = append(v2.Steps, Step{ID: fmt.Sprintf("legacy_derive_%d", i), Kind: "LEGACY_DERIVE", Payload: payload})
	}
	return v2, nil
}

func constructionPayload(operation recipe.ConstructionOperation) (json.RawMessage, error) {
	key := operationKeys[string(operation.Kind)]
	if key == "" {
		return nil, fmt.Errorf("unknown construction kind %q", operation.Kind)
	}
	encoded, err := json.Marshal(operation)
	if err != nil {
		return nil, err
	}
	parts := map[string]json.RawMessage{}
	if err = json.Unmarshal(encoded, &parts); err != nil {
		return nil, err
	}
	if len(parts) != 2 || len(parts[key]) == 0 {
		return nil, fmt.Errorf("construction kind %q requires one matching payload", operation.Kind)
	}
	return parts[key], nil
}
