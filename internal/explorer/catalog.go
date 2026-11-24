package explorer

import (
	"fmt"
	"strings"

	loomtext "github.com/calypr/loom/internal/foundation/text"

	"github.com/calypr/loom/internal/dataframe/publication"
	"github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/explorer/receiptmodel"
	"github.com/calypr/loom/internal/explorer/viewer"
)

func ViewBuilderCatalogFromRevision(revision Revision) (viewer.ViewBuilderCatalog, error) {
	switch revision.Status {
	case RevisionReady, RevisionActive, RevisionSuperseded:
	default:
		return viewer.ViewBuilderCatalog{}, invalidViewBuilder("catalog revision %q is not ready", revision.ID)
	}
	if strings.TrimSpace(revision.ID) == "" || strings.TrimSpace(revision.Project) == "" ||
		strings.TrimSpace(revision.Recipe.Name) == "" || strings.TrimSpace(revision.RecipeDigest) == "" ||
		strings.TrimSpace(revision.SourceGeneration) == "" || strings.TrimSpace(revision.Publication.ExecutionID) == "" ||
		revision.Publication.Generation != revision.SourceGeneration || revision.Dataset.Generation != revision.SourceGeneration ||
		strings.TrimSpace(revision.Dataset.SchemaDigest) == "" {
		return viewer.ViewBuilderCatalog{}, invalidViewBuilder("catalog revision %q has incomplete publication identity", revision.ID)
	}
	contracts, err := receiptmodel.DecodePublicOutputContracts(revision.PublicOutputContract)
	if err != nil {
		return viewer.ViewBuilderCatalog{}, invalidViewBuilder("catalog revision %q has an invalid public output contract: %v", revision.ID, err)
	}
	if err := contracts.ValidateAgainst(revision.Recipe, revision.EmittedColumns); err != nil {
		return viewer.ViewBuilderCatalog{}, invalidViewBuilder("catalog revision %q does not match its compiler output contract: %v", revision.ID, err)
	}
	if len(revision.Dataset.Outputs) != len(revision.Recipe.Outputs) || len(revision.Materializations) != len(revision.Recipe.Outputs) {
		return viewer.ViewBuilderCatalog{}, invalidViewBuilder("catalog revision %q does not contain every recipe output", revision.ID)
	}

	datasetByOutput := make(map[string]viewer.DatasetOutput, len(revision.Dataset.Outputs))
	physicalByOutput := make(map[string]map[string]publication.PhysicalColumn, len(revision.Recipe.Outputs))
	for _, output := range revision.Dataset.Outputs {
		if output.Name == "" {
			return viewer.ViewBuilderCatalog{}, invalidViewBuilder("catalog revision %q has an unnamed dataset output", revision.ID)
		}
		if _, duplicate := datasetByOutput[output.Name]; duplicate {
			return viewer.ViewBuilderCatalog{}, invalidViewBuilder("catalog revision %q duplicates dataset output %q", revision.ID, output.Name)
		}
		datasetByOutput[output.Name] = output
		for _, column := range output.Columns {
			if physicalByOutput[output.Name] == nil {
				physicalByOutput[output.Name] = make(map[string]publication.PhysicalColumn)
			}
			if _, duplicate := physicalByOutput[output.Name][column.Name]; duplicate {
				return viewer.ViewBuilderCatalog{}, invalidViewBuilder("catalog output %q duplicates physical column %q", output.Name, column.Name)
			}
			physicalByOutput[output.Name][column.Name] = column
		}
	}

	materializedByOutput := make(map[string]viewer.Materialization, len(revision.Materializations))
	for _, materialization := range revision.Materializations {
		outputID := loomtext.FirstNonBlank(materialization.OutputID, materialization.Output)
		if outputID == "" {
			return viewer.ViewBuilderCatalog{}, invalidViewBuilder("catalog revision %q has an unnamed materialization", revision.ID)
		}
		if _, duplicate := materializedByOutput[outputID]; duplicate {
			return viewer.ViewBuilderCatalog{}, invalidViewBuilder("catalog revision %q duplicates materialization for %q", revision.ID, outputID)
		}
		materializedByOutput[outputID] = materialization
		for _, column := range materialization.Columns {
			if physicalByOutput[outputID] == nil {
				physicalByOutput[outputID] = make(map[string]publication.PhysicalColumn)
			}
			if existing, ok := physicalByOutput[outputID][column.Name]; ok && existing != column {
				return viewer.ViewBuilderCatalog{}, invalidViewBuilder("catalog output %q has conflicting physical column %q", outputID, column.Name)
			}
			physicalByOutput[outputID][column.Name] = column
		}
	}

	emittedByOutput := make(map[string]map[string]viewer.EmittedColumn, len(revision.Recipe.Outputs))
	for _, emitted := range revision.EmittedColumns {
		if emitted.OutputID == "" || emitted.PublicColumn == "" || strings.TrimSpace(emitted.EmissionID) == "" {
			return viewer.ViewBuilderCatalog{}, invalidViewBuilder("catalog revision %q has an incomplete compiler emission", revision.ID)
		}
		if emittedByOutput[emitted.OutputID] == nil {
			emittedByOutput[emitted.OutputID] = make(map[string]viewer.EmittedColumn)
		}
		if _, duplicate := emittedByOutput[emitted.OutputID][emitted.PublicColumn]; duplicate {
			return viewer.ViewBuilderCatalog{}, invalidViewBuilder("catalog output %q duplicates emitted column %q", emitted.OutputID, emitted.PublicColumn)
		}
		emittedByOutput[emitted.OutputID][emitted.PublicColumn] = emitted
	}

	catalog := viewer.ViewBuilderCatalog{
		RecipeName: revision.Recipe.Name, RevisionID: revision.ID,
		ExecutionID: revision.Publication.ExecutionID, Generation: revision.SourceGeneration,
		Frames: make([]viewer.ViewBuilderFrame, 0, len(revision.Recipe.Outputs)),
	}
	for _, recipeOutput := range revision.Recipe.Outputs {
		selector := dataset.DataframeSelector{
			Recipe: revision.Recipe.Name, TranslationVersion: revision.Recipe.TranslationVersion,
			Output: recipeOutput.Name,
		}
		if err := selector.Validate(); err != nil {
			return viewer.ViewBuilderCatalog{}, invalidViewBuilder("catalog output %q has an invalid selector: %v", recipeOutput.Name, err)
		}
		output, ok := datasetByOutput[recipeOutput.Name]
		if !ok || output.Selector == nil || *output.Selector != selector || !output.Queryable {
			return viewer.ViewBuilderCatalog{}, invalidViewBuilder("catalog output %q is not active and queryable", recipeOutput.Name)
		}
		materialization, ok := materializedByOutput[recipeOutput.Name]
		if !ok || materialization.MaterializationID != revision.Publication.ExecutionID || materialization.Selector == nil || *materialization.Selector != selector {
			return viewer.ViewBuilderCatalog{}, invalidViewBuilder("catalog output %q is not bound to execution %q", recipeOutput.Name, revision.Publication.ExecutionID)
		}
		contract, ok := contracts.Output(recipeOutput.Name)
		if !ok {
			return viewer.ViewBuilderCatalog{}, invalidViewBuilder("catalog output %q has no public contract", recipeOutput.Name)
		}
		frame := viewer.ViewBuilderFrame{ID: recipeOutput.Name, Title: recipeOutput.Name, Columns: make([]viewer.ViewBuilderColumn, 0, len(contract.Columns))}
		for _, column := range contract.Columns {
			if runtimeColumnIsInternal(column.Column) {
				continue
			}
			if _, ok := emittedByOutput[recipeOutput.Name][column.Column]; !ok {
				return viewer.ViewBuilderCatalog{}, invalidViewBuilder("catalog output %q column %q has no compiler emission", recipeOutput.Name, column.Column)
			}
			if _, ok := physicalByOutput[recipeOutput.Name][column.Column]; !ok {

				continue
			}
			emission := emittedByOutput[recipeOutput.Name][column.Column]
			frame.Columns = append(frame.Columns, viewer.ViewBuilderColumn{
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
	return fmt.Errorf("%w: %s", viewer.ErrInvalidViewBuilder, fmt.Sprintf(format, args...))
}
