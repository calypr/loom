package explorer

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/publication"
	"github.com/calypr/loom/internal/dataset"
)

// ViewBuilderCatalogFromRevision derives the editable frame catalog from one
// immutable execution-backed revision. It never consults a mutable view list
// or promotes physical columns into public fields.
func ViewBuilderCatalogFromRevision(revision Revision) (ViewBuilderCatalog, error) {
	switch revision.Status {
	case RevisionReady, RevisionActive, RevisionSuperseded:
	default:
		return ViewBuilderCatalog{}, invalidViewBuilder("catalog revision %q is not ready", revision.ID)
	}
	if strings.TrimSpace(revision.ID) == "" || strings.TrimSpace(revision.Project) == "" ||
		strings.TrimSpace(revision.Recipe.Name) == "" || strings.TrimSpace(revision.RecipeDigest) == "" ||
		strings.TrimSpace(revision.SourceGeneration) == "" || strings.TrimSpace(revision.Publication.ExecutionID) == "" ||
		revision.Publication.Generation != revision.SourceGeneration || revision.Dataset.Generation != revision.SourceGeneration ||
		strings.TrimSpace(revision.Dataset.SchemaDigest) == "" {
		return ViewBuilderCatalog{}, invalidViewBuilder("catalog revision %q has incomplete publication identity", revision.ID)
	}
	contracts, err := DecodePublicOutputContracts(revision.PublicOutputContract)
	if err != nil {
		return ViewBuilderCatalog{}, invalidViewBuilder("catalog revision %q has an invalid public output contract: %v", revision.ID, err)
	}
	if err := contracts.ValidateAgainst(revision.Recipe, revision.EmittedColumns); err != nil {
		return ViewBuilderCatalog{}, invalidViewBuilder("catalog revision %q does not match its compiler output contract: %v", revision.ID, err)
	}
	if len(revision.Dataset.Outputs) != len(revision.Recipe.Outputs) || len(revision.Materializations) != len(revision.Recipe.Outputs) {
		return ViewBuilderCatalog{}, invalidViewBuilder("catalog revision %q does not contain every recipe output", revision.ID)
	}

	datasetByOutput := make(map[string]DatasetOutput, len(revision.Dataset.Outputs))
	physicalByOutput := make(map[string]map[string]publication.PhysicalColumn, len(revision.Recipe.Outputs))
	for _, output := range revision.Dataset.Outputs {
		if output.Name == "" {
			return ViewBuilderCatalog{}, invalidViewBuilder("catalog revision %q has an unnamed dataset output", revision.ID)
		}
		if _, duplicate := datasetByOutput[output.Name]; duplicate {
			return ViewBuilderCatalog{}, invalidViewBuilder("catalog revision %q duplicates dataset output %q", revision.ID, output.Name)
		}
		datasetByOutput[output.Name] = output
		for _, column := range output.Columns {
			if physicalByOutput[output.Name] == nil {
				physicalByOutput[output.Name] = make(map[string]publication.PhysicalColumn)
			}
			if _, duplicate := physicalByOutput[output.Name][column.Name]; duplicate {
				return ViewBuilderCatalog{}, invalidViewBuilder("catalog output %q duplicates physical column %q", output.Name, column.Name)
			}
			physicalByOutput[output.Name][column.Name] = column
		}
	}

	materializedByOutput := make(map[string]Materialization, len(revision.Materializations))
	for _, materialization := range revision.Materializations {
		outputID := firstNonEmptyString(materialization.OutputID, materialization.Output)
		if outputID == "" {
			return ViewBuilderCatalog{}, invalidViewBuilder("catalog revision %q has an unnamed materialization", revision.ID)
		}
		if _, duplicate := materializedByOutput[outputID]; duplicate {
			return ViewBuilderCatalog{}, invalidViewBuilder("catalog revision %q duplicates materialization for %q", revision.ID, outputID)
		}
		materializedByOutput[outputID] = materialization
		for _, column := range materialization.Columns {
			if physicalByOutput[outputID] == nil {
				physicalByOutput[outputID] = make(map[string]publication.PhysicalColumn)
			}
			if existing, ok := physicalByOutput[outputID][column.Name]; ok && existing != column {
				return ViewBuilderCatalog{}, invalidViewBuilder("catalog output %q has conflicting physical column %q", outputID, column.Name)
			}
			physicalByOutput[outputID][column.Name] = column
		}
	}

	emittedByOutput := make(map[string]map[string]EmittedColumn, len(revision.Recipe.Outputs))
	for _, emitted := range revision.EmittedColumns {
		if emitted.OutputID == "" || emitted.PublicColumn == "" || strings.TrimSpace(emitted.EmissionID) == "" {
			return ViewBuilderCatalog{}, invalidViewBuilder("catalog revision %q has an incomplete compiler emission", revision.ID)
		}
		if emittedByOutput[emitted.OutputID] == nil {
			emittedByOutput[emitted.OutputID] = make(map[string]EmittedColumn)
		}
		if _, duplicate := emittedByOutput[emitted.OutputID][emitted.PublicColumn]; duplicate {
			return ViewBuilderCatalog{}, invalidViewBuilder("catalog output %q duplicates emitted column %q", emitted.OutputID, emitted.PublicColumn)
		}
		emittedByOutput[emitted.OutputID][emitted.PublicColumn] = emitted
	}

	catalog := ViewBuilderCatalog{
		RecipeName: revision.Recipe.Name, RevisionID: revision.ID,
		ExecutionID: revision.Publication.ExecutionID, Generation: revision.SourceGeneration,
		Frames: make([]ViewBuilderFrame, 0, len(revision.Recipe.Outputs)),
	}
	for _, recipeOutput := range revision.Recipe.Outputs {
		selector := dataset.DataframeSelector{
			Recipe: revision.Recipe.Name, TranslationVersion: revision.Recipe.TranslationVersion,
			Output: recipeOutput.Name,
		}
		if err := selector.Validate(); err != nil {
			return ViewBuilderCatalog{}, invalidViewBuilder("catalog output %q has an invalid selector: %v", recipeOutput.Name, err)
		}
		output, ok := datasetByOutput[recipeOutput.Name]
		if !ok || output.Selector == nil || *output.Selector != selector || !output.Queryable {
			return ViewBuilderCatalog{}, invalidViewBuilder("catalog output %q is not active and queryable", recipeOutput.Name)
		}
		materialization, ok := materializedByOutput[recipeOutput.Name]
		if !ok || materialization.MaterializationID != revision.Publication.ExecutionID || materialization.Selector == nil || *materialization.Selector != selector {
			return ViewBuilderCatalog{}, invalidViewBuilder("catalog output %q is not bound to execution %q", recipeOutput.Name, revision.Publication.ExecutionID)
		}
		contract, ok := contracts.Output(recipeOutput.Name)
		if !ok {
			return ViewBuilderCatalog{}, invalidViewBuilder("catalog output %q has no public contract", recipeOutput.Name)
		}
		frame := ViewBuilderFrame{ID: recipeOutput.Name, Title: recipeOutput.Name, Columns: make([]ViewBuilderColumn, 0, len(contract.Columns))}
		for _, column := range contract.Columns {
			if runtimeColumnIsInternal(column.Column) {
				continue
			}
			if _, ok := emittedByOutput[recipeOutput.Name][column.Column]; !ok {
				return ViewBuilderCatalog{}, invalidViewBuilder("catalog output %q column %q has no compiler emission", recipeOutput.Name, column.Column)
			}
			if _, ok := physicalByOutput[recipeOutput.Name][column.Column]; !ok {
				// A physical column that disappeared is not an editable frame field.
				continue
			}
			emission := emittedByOutput[recipeOutput.Name][column.Column]
			frame.Columns = append(frame.Columns, ViewBuilderColumn{
				Name: column.Column, Label: column.Label,
				LogicalType: column.LogicalType, Cardinality: column.Cardinality,
				Filterable: emission.Filterable, Chartable: emission.Chartable,
			})
		}
		catalog.Frames = append(catalog.Frames, frame)
	}
	return catalog, nil
}

func invalidViewBuilder(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidViewBuilder, fmt.Sprintf(format, args...))
}
