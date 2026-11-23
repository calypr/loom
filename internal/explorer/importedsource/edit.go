package importedsource

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/facade"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/recipeplan"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	capabilityresolver "github.com/calypr/loom/internal/explorer/capability/resolver"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/explorer/recipeimport"
	"github.com/calypr/loom/internal/projectid"
	"github.com/google/uuid"
)

func rebuildImportedDocument(document authoringv2.Document, output recipe.Output, schema []recipe.StageColumn, catalog authoringv2.CatalogSnapshot, previousLabels ...map[string]string) (authoringv2.Document, error) {
	route, err := recipeimport.ImportRoute(output, catalog)
	if err != nil {
		return authoringv2.Document{}, err
	}
	previousImported := make(map[string]authoringv2.Column, len(document.Columns))
	authoredIDs := make(map[string]struct{}, len(document.Columns))
	seenPreviousIDs := make(map[string]struct{}, len(document.Columns))
	for index, column := range document.Columns {
		if column.ColumnID != "" {
			if _, duplicate := seenPreviousIDs[column.ColumnID]; duplicate {
				return authoringv2.Document{}, fmt.Errorf("document column %d duplicates stable column id %q", index, column.ColumnID)
			}
			seenPreviousIDs[column.ColumnID] = struct{}{}
		}
		if isImportedRecipeColumn(column) {
			if column.ColumnID == "" {
				return authoringv2.Document{}, fmt.Errorf("imported document column %d has no stable column id", index)
			}
			previousImported[column.ColumnID] = column
			continue
		}
		if column.ColumnID != "" {
			authoredIDs[column.ColumnID] = struct{}{}
		}
	}

	refreshed := make(map[string]authoringv2.Column, len(schema))
	schemaOrder := make(map[string]int, len(schema))
	seenSchemaIDs, seenSchemaNames := make(map[string]struct{}, len(schema)), make(map[string]struct{}, len(schema))
	for index, source := range schema {
		if strings.TrimSpace(source.ID) == "" || strings.TrimSpace(source.Name) == "" {
			return authoringv2.Document{}, fmt.Errorf("imported source schema column %d requires a stable id and name", index)
		}
		if _, duplicate := seenSchemaIDs[source.ID]; duplicate {
			return authoringv2.Document{}, fmt.Errorf("imported source schema duplicates column id %q", source.ID)
		}
		if _, duplicate := seenSchemaNames[source.Name]; duplicate {
			return authoringv2.Document{}, fmt.Errorf("imported source schema duplicates column name %q", source.Name)
		}
		seenSchemaIDs[source.ID] = struct{}{}
		seenSchemaNames[source.Name] = struct{}{}
		if _, collides := authoredIDs[source.ID]; collides {
			return authoringv2.Document{}, fmt.Errorf("imported source schema column id %q collides with an authored column", source.ID)
		}
		schemaOrder[source.ID] = index

		visible := true
		label := source.Label
		if label == "" {
			label = source.Name
		}
		column := authoringv2.Column{ColumnID: source.ID, Column: source.Name, Label: label, LogicalType: source.Type,
			OccurrenceID: authoringv2.RootOccurrenceID,
			Source:       authoringv2.ColumnSource{Kind: authoringv2.SourceImported, Imported: &authoringv2.ImportedSource{OutputColumn: source.Name}},
			Table:        &authoringv2.TablePresentation{Visible: &visible}}
		if old, ok := previousImported[source.ID]; ok {
			column = old
			column.Column = source.Name
			column.LogicalType = source.Type
			if column.Source.Imported == nil {
				return authoringv2.Document{}, fmt.Errorf("imported source column id %q lost its imported source identity", source.ID)
			}
			sourceCopy := *column.Source.Imported
			sourceCopy.OutputColumn = source.Name
			column.Source.Imported = &sourceCopy
			previousSourceLabel := ""
			if len(previousLabels) > 0 {
				previousSourceLabel = previousLabels[0][source.ID]
			}
			if old.Label == old.Column || (previousSourceLabel != "" && old.Label == previousSourceLabel) {
				column.Label = label
			}
		}
		refreshed[source.ID] = column
	}

	columns := make([]authoringv2.Column, 0, len(schema)+len(document.Columns))
	inserted := make(map[string]struct{}, len(schema))
	for _, old := range document.Columns {
		if isImportedRecipeColumn(old) {
			if current, exists := refreshed[old.ColumnID]; exists {
				columns = append(columns, current)
				inserted[old.ColumnID] = struct{}{}
			}
			continue
		}
		columns = append(columns, old)
	}
	for index, source := range schema {
		if _, exists := inserted[source.ID]; exists {
			continue
		}
		column := refreshed[source.ID]
		insertAt, nextPinned := len(columns), false
		lastPinned := -1
		for currentIndex, current := range columns {
			if !isImportedRecipeColumn(current) {
				continue
			}
			lastPinned = currentIndex
			if schemaOrder[current.ColumnID] > index {
				insertAt, nextPinned = currentIndex, true
				break
			}
		}
		if !nextPinned {
			if lastPinned >= 0 {
				insertAt = lastPinned + 1
			} else {
				insertAt = 0
			}
		}
		columns = append(columns, authoringv2.Column{})
		copy(columns[insertAt+1:], columns[insertAt:])
		columns[insertAt] = column
		inserted[source.ID] = struct{}{}
	}

	document.Route = route
	document.Rows = recipeimport.ImportRows(output)
	document.Columns = columns
	if err := document.Validate(); err != nil {
		return authoringv2.Document{}, err
	}
	return document, nil
}

func isImportedRecipeColumn(column authoringv2.Column) bool {
	return column.Source.Kind == authoringv2.SourceImported && column.Source.Imported != nil
}

func pinnedFieldBindingIDs(output recipe.Output, columns []facade.CompiledOutputColumn) (map[string]string, error) {
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

type importedProjectionFact struct {
	ColumnID, Owner, Kind, Key, SemanticKey, LeafKey string
}

func importedProjectionFacts(document authoringv2.Document, original recipe.Output, plan *authoringv2.ImportedSourcePlan, compiled facade.CompiledRecipeOutput, native recipeplan.OutputPlan, catalog authoringv2.CatalogSnapshot) ([]importedProjectionFact, error) {
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
		key, semanticKey, leafKey := "", column.SemanticPath, ""
		if suffix := "#" + column.Name; strings.HasSuffix(semanticKey, suffix) {
			semanticKey = strings.TrimSuffix(semanticKey, suffix)
		}
		count := 0
		if origin.SourceKind == "EXTENSION_COLUMN" {

			key = origin.OutputKey
			scope, _ := recipeimport.SourceOriginScope(origin.SourcePath)
			var walk func(recipeplan.SemanticNode, string, string)
			walk = func(node recipeplan.SemanticNode, path, prefix string) {
				occurrenceMatches := node.OccurrenceID == origin.OccurrenceID
				if origin.OccurrenceID == "" {
					occurrenceMatches = path == scope
				}
				if occurrenceMatches {
					delimiter := prefix
					if delimiter != "" {
						delimiter += "__"
					}
					for _, pivot := range node.Pivots {
						if pivot.SourceKind != "EXTENSION_COLUMN" || pivot.SourceKey != origin.SourceKey || pivot.OutputKey != origin.OutputKey || pivot.SourcePath != origin.SourcePath {
							continue
						}
						for _, pivotColumn := range pivot.Columns {
							if facade.NativePivotColumnName(delimiter, pivot.Name, pivotColumn, pivot.ColumnAliases) != column.Name {
								continue
							}
							count++
							leafKey = pivotColumn
						}
					}
				}
				for childIndex, child := range node.Children {
					childPath := fmt.Sprintf("%straversals[%d]", path, childIndex)
					if path != "" {
						childPath = fmt.Sprintf("%s.traversals[%d]", path, childIndex)
					}
					childPrefix := facade.NativeTraversalColumnPrefix(native.TraversalColumnNaming, prefix, child.Alias)
					walk(child, childPath, childPrefix)
				}
			}
			walk(native.Root, "", "")
			if count != 1 || key == "" || leafKey == "" {
				return nil, fmt.Errorf("typed extension column %q requires one exact correlated native pivot", column.Name)
			}
		} else if origin.SourceKind == "DYNAMIC_COLUMN" {
			for _, dynamic := range compiled.DynamicColumns {
				if dynamic.Name == column.Name && dynamic.SourceOrigin != nil && *dynamic.SourceOrigin == *origin {
					count++
					key = dynamic.SourceKey
					semanticKey = dynamic.SemanticPath
				}
			}
		} else {
			scope, _ := recipeimport.SourceOriginScope(origin.SourcePath)
			var walk func(recipeplan.SemanticNode, string, string)
			walk = func(node recipeplan.SemanticNode, path, prefix string) {
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
								if facade.NativePivotColumnName(delimiter, pivot.Name, value, pivot.ColumnAliases) == column.Name {
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
					walk(child, childPath, facade.NativeTraversalColumnPrefix(native.TraversalColumnNaming, prefix, child.Alias))
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

func preserveImportedGeneratedIDs(document authoringv2.Document, original recipe.Output, plan *authoringv2.ImportedSourcePlan, baseline facade.CompiledRecipeOutput, baselineNative recipeplan.OutputPlan, candidate *facade.CompiledRecipeOutput, candidateNative recipeplan.OutputPlan, catalog authoringv2.CatalogSnapshot) error {
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

func rebindImportedGeneratedProjectionIDs(candidate *facade.CompiledRecipeOutput, before, after []importedProjectionFact) error {
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
	if len(plan.RemovedStepIDs) > 0 || plan.NextRuleIndex > len(plan.RuleAdditions) {
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
		if !reflect.DeepEqual(step.MaxColumns, original.MaxColumns) || step.ValueMode != original.ValueMode || (step.Parameters != nil && !reflect.DeepEqual(step.Parameters, original.Parameters)) {
			return true
		}
	}
	return false
}

func (r Sources) Edit(ctx context.Context, project string, authorized ports.AuthorizedCapability, document authoringv2.Document, change *authoringv2.ImportedSourceStepChange) (authoringv2.Document, error) {
	return r.editSource(ctx, project, authorized, document, change, nil)
}

func (r Sources) CreateSourceRule(ctx context.Context, project string, authorized ports.AuthorizedCapability, document authoringv2.Document, rule authoringv2.SourceRuleCreate) (authoringv2.Document, error) {
	if err := rule.Validate(); err != nil {
		return authoringv2.Document{}, err
	}
	return r.editSources(ctx, project, authorized, document, nil, nil, &rule)
}

func (r Sources) EditRelationship(ctx context.Context, project string, authorized ports.AuthorizedCapability, document authoringv2.Document, change authoringv2.ImportedRelationshipChange) (authoringv2.Document, error) {
	if err := change.Validate(); err != nil {
		return authoringv2.Document{}, err
	}
	return r.editSource(ctx, project, authorized, document, nil, &change)
}

func (r Sources) EditBatch(ctx context.Context, project string, authorized ports.AuthorizedCapability, document authoringv2.Document, changes []authoringv2.ImportedSourceStepChange) (authoringv2.Document, error) {
	if err := authoringv2.ValidateImportedSourceStepBatch(changes); err != nil {
		return authoringv2.Document{}, err
	}
	return r.editSources(ctx, project, authorized, document, changes, nil, nil)
}
func (r Sources) editSource(ctx context.Context, project string, authorized ports.AuthorizedCapability, document authoringv2.Document, change *authoringv2.ImportedSourceStepChange, relationship *authoringv2.ImportedRelationshipChange) (authoringv2.Document, error) {
	var changes []authoringv2.ImportedSourceStepChange
	if change != nil {
		changes = []authoringv2.ImportedSourceStepChange{*change}
	}
	return r.editSources(ctx, project, authorized, document, changes, relationship, nil)
}
func (r Sources) editSources(ctx context.Context, project string, authorized ports.AuthorizedCapability, document authoringv2.Document, changes []authoringv2.ImportedSourceStepChange, relationship *authoringv2.ImportedRelationshipChange, rule *authoringv2.SourceRuleCreate) (authoringv2.Document, error) {
	if len(changes) > 0 {
		if err := authoringv2.ValidateImportedSourceStepBatch(changes); err != nil {
			return authoringv2.Document{}, err
		}
	}
	if document.ImportedRecipe == nil {
		return authoringv2.Document{}, fmt.Errorf("an imported source is required")
	}
	ref := *document.ImportedRecipe
	if err := ref.Validate(); err != nil {
		return authoringv2.Document{}, err
	}
	if r.registry == nil || r.engine == nil || ref.Name != r.defaultName {
		return authoringv2.Document{}, &ports.Error{Class: ports.ClassUnavailable, Stage: "imported-source", Code: "IMPORTED_SOURCE_UNAVAILABLE", Message: "imported source recipes are not configured"}
	}
	if projectid.Canonical(project) != projectid.Canonical(authorized.Snapshot.Identity.Project) {
		return authoringv2.Document{}, capability.ErrStaleSnapshot
	}
	if err := capabilityresolver.ValidateAuthorizedReadScope(authorized.Scope, authorized.Snapshot.Identity.AuthorizationScopeDigest); err != nil {
		return authoringv2.Document{}, err
	}
	entry, err := r.registry.LoadRecipeVersion(ctx, ref.Name, ref.TranslationVersion)
	if err != nil {
		return authoringv2.Document{}, &ports.Error{Class: ports.ClassUnavailable, Stage: "imported-source", Code: "IMPORTED_SOURCE_UNAVAILABLE", Message: "the pinned dataframe recipe could not be loaded", Cause: err}
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
	catalog := authoringv2.CatalogFromCapability(authorized.Snapshot, "")
	input := recipeimport.PlanEditInput{}
	previousSourceLabels := map[string]string{}
	for _, change := range changes {
		if change.Parameters == nil || change.Parameters.FieldGroup == nil {
			continue
		}
		if input.PinnedFieldIDs == nil {
			pristine, err := r.load(ctx, project, ref.Name, ref.TranslationVersion, ref.Digest, authorized)
			if err != nil {
				return authoringv2.Document{}, err
			}
			var columns []facade.CompiledOutputColumn
			for _, output := range pristine.resolved.Compiled.Outputs {
				if output.Name == ref.Output {
					columns = output.OutputSchema
				}
			}
			input.PinnedFieldIDs, err = pinnedFieldBindingIDs(*original, columns)
			if err != nil {
				return authoringv2.Document{}, err
			}
		}
		for _, step := range plan.Steps {
			if step.ID != change.StepID || step.Parameters == nil || step.Parameters.FieldGroup == nil {
				continue
			}
			for _, field := range step.Parameters.FieldGroup.Fields {
				if id := input.PinnedFieldIDs[field.FieldID]; id != "" && field.Label != "" {
					previousSourceLabels[id] = field.Label
				}
			}
		}
	}
	if relationship != nil {
		network, err := recipeimport.HydrateNetwork(document, *original, catalog)
		if err != nil {
			return authoringv2.Document{}, err
		}
		input.Occurrences = network.Occurrences
		if relationship.Action == "ADD" {
			input.NewOccurrenceID = "project_" + strings.ReplaceAll(uuid.NewString(), "-", "")
		}
	}
	plan, err = recipeimport.EditPlan(*original, plan, catalog, recipeimport.PlanEdit{
		StepChanges: changes, Relationship: relationship, RuleCreate: rule,
	}, input)
	if err != nil {
		return authoringv2.Document{}, err
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
	return rebuildImportedDocument(updated, effective, schema, catalog, previousSourceLabels)
}
