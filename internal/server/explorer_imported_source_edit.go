package server

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle"
	"github.com/calypr/loom/internal/explorer/recipeimport"
	"github.com/calypr/loom/internal/projectid"
)

func (r importedRecipeSources) Edit(ctx context.Context, project string, authorized lifecycle.AuthorizedCapability, document authoringv2.Document, change *authoringv2.ImportedSourceStepChange) (authoringv2.Document, error) {
	if document.ImportedRecipe == nil || r.registry == nil {
		return authoringv2.Document{}, fmt.Errorf("an imported source is required")
	}
	ref := *document.ImportedRecipe
	if err := ref.Validate(); err != nil {
		return authoringv2.Document{}, err
	}
	if ref.Name != r.defaultName {
		return authoringv2.Document{}, fmt.Errorf("the imported recipe is not a configured dataframe source")
	}
	if projectid.Canonical(project) != projectid.Canonical(authorized.Snapshot.Identity.Project) {
		return authoringv2.Document{}, capability.ErrStaleSnapshot
	}
	if err := validateAuthorizedReadScope(authorized.Scope, authorized.Snapshot.Identity.AuthorizationScopeDigest); err != nil {
		return authoringv2.Document{}, err
	}
	entry, err := r.registry.LoadRecipeVersion(ctx, ref.Name, ref.TranslationVersion)
	if err != nil {
		return authoringv2.Document{}, err
	}
	bundle, err := entry.Bundle.CanonicalBundle()
	if err != nil {
		return authoringv2.Document{}, err
	}
	digest, err := bundle.Digest()
	if err != nil {
		return authoringv2.Document{}, err
	}
	if digest != ref.Digest || entry.Digest != digest {
		return authoringv2.Document{}, fmt.Errorf("the imported dataframe recipe changed; reload its exact source version")
	}
	var original *recipe.Output
	for i := range bundle.Outputs {
		if bundle.Outputs[i].Name == ref.Output {
			original = &bundle.Outputs[i]
			break
		}
	}
	if original == nil {
		return authoringv2.Document{}, fmt.Errorf("pinned source output was not found")
	}
	plan := recipeimport.SourcePlan(*original)
	if document.ImportedSource != nil {
		if _, err := recipeimport.ApplySourcePlan(*original, document.ImportedSource); err != nil {
			return authoringv2.Document{}, err
		}
		raw, err := json.Marshal(document.ImportedSource)
		if err != nil {
			return authoringv2.Document{}, err
		}
		if err := json.Unmarshal(raw, &plan); err != nil {
			return authoringv2.Document{}, err
		}
	}
	if change != nil {
		found := false
		for i := range plan.Steps {
			step := &plan.Steps[i]
			if step.ID != change.StepID {
				continue
			}
			if !step.Editable {
				return authoringv2.Document{}, fmt.Errorf("this source step cannot be changed")
			}
			if change.MaxColumns != nil && step.MaxColumns == nil {
				return authoringv2.Document{}, fmt.Errorf("this source step has no column limit")
			}
			if change.ValueMode != "" && step.ValueMode == "" {
				return authoringv2.Document{}, fmt.Errorf("this source step has no value policy")
			}
			step.Enabled = change.Enabled
			if change.MaxColumns != nil {
				value := *change.MaxColumns
				step.MaxColumns = &value
			}
			if change.ValueMode != "" {
				step.ValueMode = change.ValueMode
			}
			found = true
		}
		if !found {
			return authoringv2.Document{}, fmt.Errorf("source step was not found")
		}
	}
	updated := document
	updated.ImportedSource = &plan
	source, err := r.load(ctx, project, ref.Name, ref.TranslationVersion, ref.Digest, authorized, updated)
	if err != nil {
		return authoringv2.Document{}, err
	}
	effective, err := recipeimport.ApplySourcePlan(*original, &plan)
	if err != nil {
		return authoringv2.Document{}, err
	}
	var schema []recipe.StageColumn
	for _, output := range source.resolved.Compiled.Outputs {
		if output.Name != ref.Output {
			continue
		}
		columns, err := importedSourceSchema(output.OutputSchema)
		if err != nil {
			return authoringv2.Document{}, err
		}
		for _, column := range columns {
			schema = append(schema, column.Column)
		}
	}
	if schema == nil {
		return authoringv2.Document{}, fmt.Errorf("source change produced no public columns")
	}
	return rebuildImportedDocument(updated, effective, schema, authoringV2Catalog(authorized.Snapshot, ""))
}

func rebuildImportedDocument(document authoringv2.Document, output recipe.Output, schema []recipe.StageColumn, catalog authoringv2.CatalogSnapshot) (authoringv2.Document, error) {
	route, err := recipeimport.ImportRoute(output, catalog)
	if err != nil {
		return authoringv2.Document{}, err
	}
	previous := make(map[string]authoringv2.Column, len(document.Columns))
	for _, column := range document.Columns {
		previous[column.ColumnID] = column
	}
	document.Route = route
	document.Rows = recipeimport.ImportRows(output)
	document.Columns = make([]authoringv2.Column, 0, len(schema))
	for _, source := range schema {
		visible := true
		label := source.Label
		if label == "" {
			label = source.Name
		}
		column := authoringv2.Column{ColumnID: source.ID, Column: source.Name, Label: label, LogicalType: source.Type,
			OccurrenceID: authoringv2.RootOccurrenceID,
			Source:       authoringv2.ColumnSource{Kind: authoringv2.SourceImported, Imported: &authoringv2.ImportedSource{OutputColumn: source.Name}},
			Table:        &authoringv2.TablePresentation{Visible: &visible}}
		if old, ok := previous[source.ID]; ok && old.Column == source.Name {
			column = old
			column.LogicalType = source.Type
		}
		document.Columns = append(document.Columns, column)
	}
	return document, document.Validate()
}
