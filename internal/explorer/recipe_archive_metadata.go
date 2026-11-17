package explorer

import "github.com/calypr/loom/internal/dataframe/recipe"

// recipeIdentityMetadata copies only identity facts already used by immutable
// Viewer/contract readers. It never converts an archive into executable intent.
func recipeIdentityMetadata(bundle recipe.Bundle) recipe.ArchivedMetadata {
	metadata := recipe.ArchivedMetadata{RecipeSchemaVersion: bundle.RecipeSchemaVersion, Name: bundle.Name, TranslationVersion: bundle.TranslationVersion, Outputs: make([]recipe.ArchivedOutputDescriptor, len(bundle.Outputs))}
	for i, o := range bundle.Outputs {
		metadata.Outputs[i] = recipe.ArchivedOutputDescriptor{Name: o.Name, RootResourceType: o.RootResourceType, RootOccurrenceID: o.RootOccurrenceID, RowGrain: o.RowGrain}
	}
	return metadata
}
