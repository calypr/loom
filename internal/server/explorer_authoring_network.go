package server

import (
	"context"
	"fmt"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/lifecycle"
	"github.com/calypr/loom/internal/explorer/recipeimport"
)

func (r importedRecipeSources) Network(ctx context.Context, project string, authorized lifecycle.AuthorizedCapability, document authoringv2.Document) (authoringv2.AuthoringNetwork, error) {
	catalog := authoringV2Catalog(authorized.Snapshot, "")
	if document.ImportedRecipe == nil {
		return authoringv2.NetworkForDocument(document, catalog)
	}
	ref := document.ImportedRecipe
	source, err := r.load(ctx, project, ref.Name, ref.TranslationVersion, ref.Digest, authorized, document)
	if err != nil {
		return authoringv2.AuthoringNetwork{}, err
	}
	for _, output := range source.bundle.Outputs {
		if output.Name == ref.Output {
			network, err := recipeimport.HydrateNetwork(document, output, catalog)
			if err != nil {
				return authoringv2.AuthoringNetwork{}, err
			}
			for _, compiled := range source.resolved.Compiled.Outputs {
				if compiled.Name == ref.Output {
					plan, err := recipeimport.HydrateSourcePlan(output, document.ImportedSource)
					if err != nil {
						return authoringv2.AuthoringNetwork{}, err
					}
					return recipeimport.BindCompiledNetwork(network, compiled.OutputSchema, &plan)
				}
			}
			return authoringv2.AuthoringNetwork{}, fmt.Errorf("compiled source output was not found")
		}
	}
	return authoringv2.AuthoringNetwork{}, fmt.Errorf("pinned source output was not found")
}
