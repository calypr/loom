package server

import (
	"context"
	"fmt"
	"github.com/google/uuid"
	"strings"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle"
	"github.com/calypr/loom/internal/explorer/recipeimport"
	"github.com/calypr/loom/internal/projectid"
)

func (r importedRecipeSources) Edit(ctx context.Context, project string, authorized lifecycle.AuthorizedCapability, document authoringv2.Document, change *authoringv2.ImportedSourceStepChange) (authoringv2.Document, error) {
	return r.editSource(ctx, project, authorized, document, change, nil)
}

func (r importedRecipeSources) EditRelationship(ctx context.Context, project string, authorized lifecycle.AuthorizedCapability, document authoringv2.Document, change authoringv2.ImportedRelationshipChange) (authoringv2.Document, error) {
	if err := change.Validate(); err != nil {
		return authoringv2.Document{}, err
	}
	return r.editSource(ctx, project, authorized, document, nil, &change)
}

func (r importedRecipeSources) editSource(ctx context.Context, project string, authorized lifecycle.AuthorizedCapability, document authoringv2.Document, change *authoringv2.ImportedSourceStepChange, relationship *authoringv2.ImportedRelationshipChange) (authoringv2.Document, error) {
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
	plan, err := recipeimport.HydrateSourcePlan(*original, document.ImportedSource)
	if err != nil {
		return authoringv2.Document{}, err
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

	if relationship != nil {
		catalog := authoringV2Catalog(authorized.Snapshot, "")
		network, err := recipeimport.HydrateNetwork(document, *original, catalog)
		if err != nil {
			return authoringv2.Document{}, err
		}
		if plan.Relationships == nil {
			plan.Relationships = &authoringv2.ImportedRelationshipOverlay{}
		}
		overlay := plan.Relationships
		occurrences := map[string]authoringv2.NetworkOccurrence{}
		for _, o := range network.Occurrences {
			if o.Enabled {
				occurrences[o.OccurrenceID] = o
			}
		}
		switch relationship.Action {
		case "ADD":
			parent, ok := occurrences[relationship.ParentOccurrenceID]
			if !ok {
				return authoringv2.Document{}, fmt.Errorf("selected parent occurrence is unavailable")
			}
			var edge *authoringv2.CatalogEdge
			for i := range catalog.Edges {
				if catalog.Edges[i].ID == relationship.EdgeID {
					edge = &catalog.Edges[i]
					break
				}
			}
			if edge == nil {
				return authoringv2.Document{}, fmt.Errorf("selected catalog edge is unavailable")
			}
			var from, to *authoringv2.CatalogNode
			for i := range catalog.Nodes {
				node := &catalog.Nodes[i]
				if node.ID == edge.FromNodeID {
					from = node
				}
				if node.ID == edge.ToNodeID {
					to = node
				}
			}
			if from == nil || to == nil || from.ResourceType != parent.ResourceType {
				return authoringv2.Document{}, fmt.Errorf("selected catalog edge does not extend this occurrence")
			}
			// Reject a resource repeated along this ancestor path. Independent paths
			// to the same resource type retain separate occurrence identities.
			for ancestor := parent; ; {
				if ancestor.ResourceType == to.ResourceType {
					return authoringv2.Document{}, fmt.Errorf("cyclic source relationship is unsupported")
				}
				if ancestor.ParentOccurrenceID == "" {
					break
				}
				next, ok := occurrences[ancestor.ParentOccurrenceID]
				if !ok {
					return authoringv2.Document{}, fmt.Errorf("source ancestor is unavailable")
				}
				ancestor = next
			}
			id := "project_" + strings.ReplaceAll(uuid.NewString(), "-", "")
			addition := authoringv2.ImportedRelationship{OccurrenceID: id, ParentOccurrenceID: parent.OccurrenceID, CatalogEdgeID: edge.ID, Relationship: edge.Label, ResourceType: to.ResourceType, StorageDirection: edge.StorageDirection, MatchMode: relationship.MatchMode}
			// Related identifiers remain lists even when the preview happens to have
			// one match. Catalog authority is required for the copied scalar field.
			for _, field := range catalog.Candidates {
				if field.NodeID == to.ID && (field.FieldPath == "id" || field.FieldPath == to.ResourceType+".id") && !field.Repeated {
					addition.Fields = []authoringv2.ImportedRelationshipField{{CandidateID: field.ID, ColumnID: id + "_id", Column: id + "_id", FieldPath: field.FieldPath, Label: field.Label, ValueMode: "ALL"}}
					break
				}
			}
			if len(addition.Fields) == 0 {
				return authoringv2.Document{}, fmt.Errorf("related source requires an authorized scalar identifier field")
			}
			overlay.Additions = append(overlay.Additions, addition)
		case "MATCH", "REMOVE":
			occurrence, ok := occurrences[relationship.OccurrenceID]
			if !ok || occurrence.OccurrenceID == authoringv2.RootOccurrenceID {
				return authoringv2.Document{}, fmt.Errorf("selected relationship occurrence is unavailable")
			}
			if relationship.Action == "REMOVE" {
				for _, o := range network.Occurrences {
					if o.Enabled && o.ParentOccurrenceID == occurrence.OccurrenceID {
						return authoringv2.Document{}, fmt.Errorf("remove dependent source relationships explicitly before their parent")
					}
				}
				overlay.Removals = append(overlay.Removals, occurrence.OccurrenceID)
				matches := overlay.MatchOverrides[:0]
				for _, m := range overlay.MatchOverrides {
					if m.OccurrenceID != occurrence.OccurrenceID {
						matches = append(matches, m)
					}
				}
				overlay.MatchOverrides = matches
			} else {
				found := false
				for i := range overlay.MatchOverrides {
					if overlay.MatchOverrides[i].OccurrenceID == occurrence.OccurrenceID {
						overlay.MatchOverrides[i].MatchMode = relationship.MatchMode
						found = true
					}
				}
				if !found {
					overlay.MatchOverrides = append(overlay.MatchOverrides, authoringv2.ImportedRelationshipMatch{OccurrenceID: occurrence.OccurrenceID, MatchMode: relationship.MatchMode})
				}
			}
		}
		if err := recipeimport.ValidateRelationshipOverlay(*original, &plan, catalog); err != nil {
			return authoringv2.Document{}, err
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
