package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/calypr/loom/internal/dataframe/semantic"
	"github.com/google/uuid"
	"reflect"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/lower"
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

func (r importedRecipeSources) EditBatch(ctx context.Context, project string, authorized lifecycle.AuthorizedCapability, document authoringv2.Document, changes []authoringv2.ImportedSourceStepChange) (authoringv2.Document, error) {
	if err := authoringv2.ValidateImportedSourceStepBatch(changes); err != nil {
		return authoringv2.Document{}, err
	}
	return r.editSources(ctx, project, authorized, document, changes, nil)
}
func (r importedRecipeSources) editSource(ctx context.Context, project string, authorized lifecycle.AuthorizedCapability, document authoringv2.Document, change *authoringv2.ImportedSourceStepChange, relationship *authoringv2.ImportedRelationshipChange) (authoringv2.Document, error) {
	var changes []authoringv2.ImportedSourceStepChange
	if change != nil {
		changes = []authoringv2.ImportedSourceStepChange{*change}
	}
	return r.editSources(ctx, project, authorized, document, changes, relationship)
}
func (r importedRecipeSources) editSources(ctx context.Context, project string, authorized lifecycle.AuthorizedCapability, document authoringv2.Document, changes []authoringv2.ImportedSourceStepChange, relationship *authoringv2.ImportedRelationshipChange) (authoringv2.Document, error) {
	if len(changes) > 0 {
		if err := authoringv2.ValidateImportedSourceStepBatch(changes); err != nil {
			return authoringv2.Document{}, err
		}
	}
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
	var expected map[string]string
	previousSourceLabels := map[string]string{}
	for i := range changes {
		change := &changes[i]
		if change != nil && change.Parameters != nil && change.Parameters.FieldGroup != nil {
			if expected == nil {
				pristine, err := r.load(ctx, project, ref.Name, ref.TranslationVersion, ref.Digest, authorized)
				if err != nil {
					return authoringv2.Document{}, err
				}
				var columns []lower.CompiledOutputColumn
				for _, output := range pristine.resolved.Compiled.Outputs {
					if output.Name == ref.Output {
						columns = output.OutputSchema
					}
				}
				expected, err = pinnedFieldBindingIDs(*original, columns)
				if err != nil {
					return authoringv2.Document{}, err
				}
			}
			if err := verifyPinnedFieldBindings(plan.FieldBindings, expected); err != nil {
				return authoringv2.Document{}, err
			}
			for _, oldStep := range plan.Steps {
				if oldStep.ID != change.StepID || oldStep.Parameters == nil || oldStep.Parameters.FieldGroup == nil {
					continue
				}
				for _, field := range oldStep.Parameters.FieldGroup.Fields {
					if id := expected[field.FieldID]; id != "" && field.Label != "" {
						previousSourceLabels[id] = field.Label
					}
				}
			}
			bound := map[string]bool{}
			for _, binding := range plan.FieldBindings {
				bound[binding.FieldID] = true
			}
			for _, field := range change.Parameters.FieldGroup.Fields {
				id := expected[field.FieldID]
				if id == "" {
					return authoringv2.Document{}, fmt.Errorf("source field %q has no unambiguous emitted identity in the pinned projection; resolve its source collision before editing", field.FieldID)
				}
				if !bound[field.FieldID] {
					plan.FieldBindings = append(plan.FieldBindings, authoringv2.ImportedSourceFieldBinding{FieldID: field.FieldID, ColumnID: id})
					bound[field.FieldID] = true
				}
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
				if change.Parameters != nil {
					if err := change.Parameters.Validate(step.Kind); err != nil {
						return authoringv2.Document{}, err
					}
					step.Parameters = change.Parameters
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
	effectiveCandidate, err := recipeimport.ApplySourcePlan(*original, &plan)
	if err != nil {
		return authoringv2.Document{}, err
	}
	for i := range changes {
		change := &changes[i]
		if change != nil && change.Parameters != nil && change.Parameters.Traversal != nil {
			catalog := authoringV2Catalog(authorized.Snapshot, "")
			route, err := recipeimport.ImportRoute(effectiveCandidate, catalog)
			if err != nil {
				return authoringv2.Document{}, fmt.Errorf("edited source traversal: %w", err)
			}
			occurrence := ""
			for _, step := range plan.Steps {
				if step.ID == change.StepID {
					occurrence = step.OccurrenceID
				}
			}
			var selected *authoringv2.RouteNode
			var find func(*authoringv2.RouteNode)
			find = func(n *authoringv2.RouteNode) {
				if n.OccurrenceID == occurrence {
					selected = n
				}
				for i := range n.Children {
					find(&n.Children[i])
				}
			}
			find(&route)
			if selected == nil || selected.CatalogEdgeID == "" {
				return authoringv2.Document{}, fmt.Errorf("edited source traversal %q must resolve to exactly one authorized catalog relationship", change.StepID)
			}
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
	return rebuildImportedDocument(updated, effective, schema, authoringV2Catalog(authorized.Snapshot, ""), previousSourceLabels)
}

func rebuildImportedDocument(document authoringv2.Document, output recipe.Output, schema []recipe.StageColumn, catalog authoringv2.CatalogSnapshot, previousLabels ...map[string]string) (authoringv2.Document, error) {
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
		if old, ok := previous[source.ID]; ok {
			column = old
			column.Column = source.Name
			column.LogicalType = source.Type
			if column.Source.Imported != nil {
				sourceCopy := *column.Source.Imported
				sourceCopy.OutputColumn = source.Name
				column.Source.Imported = &sourceCopy
			}
			previousSourceLabel := ""
			if len(previousLabels) > 0 {
				previousSourceLabel = previousLabels[0][source.ID]
			}
			if old.Label == old.Column || (previousSourceLabel != "" && old.Label == previousSourceLabel) {
				column.Label = label
			}
		}
		document.Columns = append(document.Columns, column)
	}
	return document, document.Validate()
}

// pinnedFieldBindingIDs uses structural compiler provenance plus the exact
// authored symbol. It never maps by labels, sampled values, or first match.
func pinnedFieldBindingIDs(output recipe.Output, columns []lower.CompiledOutputColumn) (map[string]string, error) {
	result := map[string]string{}
	for _, slot := range recipeimport.ImportedFieldSlots(output) {
		matches := 0
		for _, column := range columns {
			origin := column.SourceOrigin
			if column.Internal || column.Identity || origin == nil || origin.SourceKind != "FIELD_GROUP" || origin.SourcePath != slot.SourceStepID || column.Name != slot.Name {
				continue
			}
			matches++
			result[slot.FieldID] = column.ID
		}
		if matches > 1 {
			return nil, fmt.Errorf("pinned field %q has ambiguous compiled provenance", slot.FieldID)
		}
		if matches == 0 {
			// A source field hidden by the pin's collision policy has no emitted
			// column to preserve. Give that exact immutable slot its own dormant ID;
			// never borrow the winner's identity. Renaming it can expose this field
			// without retargeting the column that previously shadowed it.
			raw, err := json.Marshal(struct {
				Output, FieldID string
				Field           recipe.Field
			}{output.Name, slot.FieldID, slot.Field})
			if err != nil {
				return nil, err
			}
			sum := sha256.Sum256(raw)
			result[slot.FieldID] = "source_field_" + hex.EncodeToString(sum[:12])
		}
	}
	return result, nil
}
func verifyPinnedFieldBindings(bindings []authoringv2.ImportedSourceFieldBinding, expected map[string]string) error {
	for _, binding := range bindings {
		if id := expected[binding.FieldID]; id == "" || id != binding.ColumnID {
			return fmt.Errorf("source field binding %q does not match its immutable pinned field identity", binding.FieldID)
		}
	}
	return nil
}

type importedProjectionFact struct {
	ColumnID, Owner, Kind, Key, SemanticKey, LeafKey string
}

// importedProjectionFacts combines compiler-proven ownership with native value
// keys. Names are used only to locate a projection inside its proven native
// owner; they never serve as a persisted identity or select an arbitrary owner.
func importedProjectionFacts(document authoringv2.Document, original recipe.Output, plan *authoringv2.ImportedSourcePlan, compiled lower.CompiledRecipeOutput, native semantic.OutputPlan, catalog authoringv2.CatalogSnapshot) ([]importedProjectionFact, error) {
	document.ImportedSource = plan
	document.Columns = nil
	for _, column := range compiled.OutputSchema {
		if !column.Internal && !column.Identity {
			document.Columns = append(document.Columns, authoringv2.Column{ColumnID: column.ID, Column: column.Name})
		}
	}
	network, err := recipeimport.HydrateNetwork(document, original, catalog)
	if err != nil {
		return nil, err
	}
	network, err = recipeimport.BindCompiledNetwork(network, compiled.OutputSchema, plan)
	if err != nil {
		return nil, err
	}
	owners := map[string]string{}
	for _, binding := range network.Bindings {
		if binding.Status == "EXACT" {
			owners[binding.ColumnID] = binding.SourceStepID
		}
	}
	var facts []importedProjectionFact
	for _, column := range compiled.OutputSchema {
		origin := column.SourceOrigin
		if column.Internal || column.Identity || origin == nil || origin.SourceKind == "FIELD_GROUP" {
			continue
		}
		owner := owners[column.ID]
		if owner == "" {
			continue
		}
		key, semanticKey, leafKey, dynamicRuntimeName := "", column.SemanticPath, "", ""
		if suffix := "#" + column.Name; strings.HasSuffix(semanticKey, suffix) {
			semanticKey = strings.TrimSuffix(semanticKey, suffix)
		}
		count := 0
		if origin.SourceKind == "DYNAMIC_COLUMN" || origin.SourceKind == "EXTENSION_COLUMN" {
			for _, dynamic := range compiled.DynamicColumns {
				if dynamic.Name == column.Name && dynamic.SourceOrigin != nil && *dynamic.SourceOrigin == *origin {
					count++
					key = dynamic.SourceKey
					semanticKey = dynamic.SemanticPath
					dynamicRuntimeName = dynamic.DynamicName
				}
			}
			if origin.SourceKind == "EXTENSION_COLUMN" {
				leafCount := 0
				var leaves func(semantic.SemanticNode)
				leaves = func(node semantic.SemanticNode) {
					for _, dynamic := range node.DynamicMaps {
						runtimeName := dynamic.Name
						if dynamic.ScopeAlias != "" && dynamic.ScopeAlias != "root" {
							runtimeName = dynamic.ScopeAlias + "__" + runtimeName
						}
						if strings.TrimSuffix(dynamic.Source.SourcePath, ".source") == origin.SourcePath && dynamic.SourceFamilyKey == origin.SourceKey && len(dynamic.Columns) == 1 && runtimeName == dynamicRuntimeName {
							leafCount++
							leafKey = dynamic.Columns[0]
						}
					}
					for _, child := range node.Children {
						leaves(child)
					}
				}
				leaves(native.Root)
				if leafCount != 1 || leafKey == "" {
					return nil, fmt.Errorf("typed extension column %q requires one structural native leaf", column.Name)
				}
			}
		} else {
			scope, _ := recipeimport.SourceOriginScope(origin.SourcePath)
			var walk func(semantic.SemanticNode, string, string)
			walk = func(node semantic.SemanticNode, path, prefix string) {
				occurrenceMatches := node.OccurrenceID == origin.OccurrenceID
				if origin.OccurrenceID == "" {
					occurrenceMatches = path == scope
				}
				delimiter := prefix
				if delimiter != "" {
					delimiter += "__"
				}
				if occurrenceMatches {
					switch origin.SourceKind {
					case "CATALOG_PROJECTION":
						for _, field := range node.Fields {
							if field.Discovered && delimiter+field.Name == column.Name && field.FieldRef != "" {
								count++
								key = field.FieldRef
							}
						}
					case "PIVOT":
						for _, pivot := range node.Pivots {
							if pivot.Name != origin.SourceKey {
								continue
							}
							for _, value := range pivot.Columns {
								if lower.NativePivotColumnName(delimiter, pivot.Name, value, pivot.ColumnAliases) == column.Name {
									count++
									key = value
								}
							}
						}
					}
				}
				for i, child := range node.Children {
					childPath := fmt.Sprintf("%straversals[%d]", path, i)
					if path != "" {
						childPath = fmt.Sprintf("%s.traversals[%d]", path, i)
					}
					walk(child, childPath, lower.NativeTraversalColumnPrefix(native.TraversalColumnNaming, prefix, child.Alias))
				}
			}
			walk(native.Root, "", "")
		}
		if count != 1 || key == "" {
			return nil, fmt.Errorf("source column %q requires exactly one compiler-proven native key for operation %q", column.Name, owner)
		}
		facts = append(facts, importedProjectionFact{ColumnID: column.ID, Owner: owner, Kind: origin.SourceKind, Key: key, SemanticKey: semanticKey, LeafKey: leafKey})
	}
	return facts, nil
}

func preserveImportedGeneratedIDs(document authoringv2.Document, original recipe.Output, plan *authoringv2.ImportedSourcePlan, baseline lower.CompiledRecipeOutput, baselineNative semantic.OutputPlan, candidate *lower.CompiledRecipeOutput, candidateNative semantic.OutputPlan, catalog authoringv2.CatalogSnapshot) error {
	pristinePlan := recipeimport.SourcePlan(original)
	before, err := importedProjectionFacts(document, original, &pristinePlan, baseline, baselineNative, catalog)
	if err != nil {
		return err
	}
	after, err := importedProjectionFacts(document, original, plan, *candidate, candidateNative, catalog)
	if err != nil {
		return err
	}
	return rebindImportedGeneratedProjectionIDs(candidate, before, after)
}

func rebindImportedGeneratedProjectionIDs(candidate *lower.CompiledRecipeOutput, before, after []importedProjectionFact) error {
	identityKey := func(f importedProjectionFact) string { return f.Owner + "\x00" + f.Kind + "\x00" + f.Key }
	oldGroups, newGroups := map[string][]importedProjectionFact{}, map[string][]importedProjectionFact{}
	for _, fact := range before {
		key := identityKey(fact)
		oldGroups[key] = append(oldGroups[key], fact)
	}
	for _, fact := range after {
		key := identityKey(fact)
		newGroups[key] = append(newGroups[key], fact)
	}
	replacements := map[string]string{}
	for key, group := range newGroups {
		old := oldGroups[key]
		if len(old) == 0 {
			// New keys have no pristine legacy ID. Assign their owner/key identity
			// immediately at first creation, before a mutable public name can leak
			// into a downstream binding. Typed extensions always carry their native
			// structural leaf, even before another leaf shares the same lookup key.
			leaves := map[string]bool{}
			for _, fact := range group {
				leaf := ""
				if fact.Kind == "EXTENSION_COLUMN" {
					leaf = fact.LeafKey
					if leaf == "" || leaves[leaf] {
						return fmt.Errorf("new native extension key for source operation %q has no unique structural leaf", fact.Owner)
					}
					leaves[leaf] = true
				} else if len(group) != 1 {
					return fmt.Errorf("new native key for source operation %q is ambiguous", fact.Owner)
				}
				raw, err := json.Marshal(struct{ Output, Owner, Kind, Key, Leaf string }{candidate.Name, fact.Owner, fact.Kind, fact.Key, leaf})
				if err != nil {
					return err
				}
				sum := sha256.Sum256(raw)
				replacements[fact.ColumnID] = "source_key_" + hex.EncodeToString(sum[:12])
			}
			continue
		}
		for _, fact := range group {
			matches := []importedProjectionFact{}
			if len(old) == 1 && len(group) == 1 {
				matches = old
			} else {
				for _, previous := range old {
					if fact.SemanticKey != "" && previous.SemanticKey == fact.SemanticKey {
						matches = append(matches, previous)
					}
				}
			}
			if len(matches) != 1 {
				return fmt.Errorf("source operation %q has an ambiguous native key; its existing column identity cannot be preserved atomically", fact.Owner)
			}
			replacements[fact.ColumnID] = matches[0].ColumnID
		}
	}
	used := map[string]bool{}
	for _, column := range candidate.OutputSchema {
		id := column.ID
		if replacement := replacements[id]; replacement != "" {
			id = replacement
		}
		if !column.Internal && !column.Identity {
			if used[id] {
				return fmt.Errorf("source change aliases distinct native columns to the same identity")
			}
			used[id] = true
		}
	}
	for i := range candidate.OutputSchema {
		column := &candidate.OutputSchema[i]
		if id := replacements[column.ID]; id != "" {
			column.ID = id
		}
	}
	return nil
}

func requiresPristineProjectionIdentity(output recipe.Output, plan *authoringv2.ImportedSourcePlan) bool {
	if plan == nil {
		return false
	}
	if len(plan.FieldBindings) > 0 {
		return true
	}
	if r := plan.Relationships; r != nil && (len(r.Additions) > 0 || len(r.Removals) > 0 || len(r.MatchOverrides) > 0) {
		return true
	}
	originals := map[string]authoringv2.ImportedSourceStep{}
	for _, step := range recipeimport.SourcePlan(output).Steps {
		originals[step.ID] = step
	}
	for _, step := range plan.Steps {
		original := originals[step.ID]
		if step.Enabled != original.Enabled || !reflect.DeepEqual(step.MaxColumns, original.MaxColumns) || step.ValueMode != original.ValueMode || (step.Parameters != nil && !reflect.DeepEqual(step.Parameters, original.Parameters)) {
			return true
		}
	}
	return false
}
