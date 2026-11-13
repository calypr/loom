package recipeimport

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer/authoringv2"
)

const (
	SourcePlanFieldGroup        = "FIELD_GROUP"
	SourcePlanCatalogProjection = "CATALOG_PROJECTION"
	SourcePlanDynamicColumn     = "DYNAMIC_COLUMN"
	SourcePlanExtensionColumn   = "EXTENSION_COLUMN"
	SourcePlanPivot             = "PIVOT"
	SourcePlanTraversal         = "TRAVERSAL"
	SourcePlanExpansion         = "EXPANSION"
	SourcePlanIdentity          = "IDENTITY"
)

// SourcePlan describes the source operations in one recipe output without
// sending executable expressions to the Builder client. IDs use original
// recipe paths, so disabling a traversal does not renumber its saved children.
func SourcePlan(output recipe.Output) authoringv2.ImportedSourcePlan {
	plan := authoringv2.ImportedSourcePlan{Steps: make([]authoringv2.ImportedSourceStep, 0)}
	if output.Expand != nil {
		plan.Steps = append(plan.Steps, authoringv2.ImportedSourceStep{
			ID: "expand", Kind: SourcePlanExpansion, Title: "Expand source rows",
			Summary: expansionSummary(*output.Expand), Enabled: true,
		})
	}
	if output.Identity != nil {
		plan.Steps = append(plan.Steps, authoringv2.ImportedSourceStep{
			ID: "identity", Kind: SourcePlanIdentity, Title: "Source row identity",
			Summary: "The pinned recipe defines how source rows receive stable identities.", Enabled: true,
		})
	}
	appendSourceScope(&plan.Steps, "", output.RootResourceType, output.Fields, output.CatalogProjections,
		output.DynamicColumns, output.ExtensionColumns, output.Pivots, output.Traversals)
	return plan
}

func expansionSummary(expansion recipe.Expansion) string {
	selector := expansion.From.Select
	if selector == "" {
		selector = "the configured repeated value"
	}
	return fmt.Sprintf("Create one row per value from %s; when empty, %s.", selector, strings.ToLower(string(expansion.EmptyPolicy.Normalized())))
}

func appendSourceScope(steps *[]authoringv2.ImportedSourceStep, parentID, resourceType string,
	fields []recipe.Field, catalogProjections []recipe.CatalogProjection, dynamicColumns []recipe.DynamicColumn,
	extensionColumns []recipe.ExtensionColumn, pivots []recipe.Pivot, traversals []recipe.Traversal,
) {
	if len(fields) > 0 {
		label := strings.TrimSpace(resourceType)
		if label == "" {
			label = "source"
		}
		*steps = append(*steps, authoringv2.ImportedSourceStep{
			ID: sourcePath(parentID, "fields"), ParentID: parentID, Kind: SourcePlanFieldGroup,
			Title:   "Fields from " + label,
			Summary: fieldGroupSummary(fields, label),
			Enabled: true, Editable: true,
		})
	}
	for index, projection := range catalogProjections {
		maxColumns := projection.MaxColumns
		*steps = append(*steps, authoringv2.ImportedSourceStep{
			ID: sourceIndexPath(parentID, "catalogProjections", index), ParentID: parentID,
			Kind: SourcePlanCatalogProjection, SourceKey: projection.Name, Title: "Catalog fields: " + projection.Name,
			Summary: "Discover populated source fields.",
			Enabled: true, Editable: true, MaxColumns: &maxColumns,
			ValueMode: string(projection.ValueMode.Normalized()),
		})
	}
	for index, dynamic := range dynamicColumns {
		step := authoringv2.ImportedSourceStep{
			ID: sourceIndexPath(parentID, "dynamicColumns", index), ParentID: parentID,
			Kind: SourcePlanDynamicColumn, SourceKey: dynamic.Name, Title: "Dynamic columns: " + dynamic.Name,
			Summary: "Discover columns from this keyed source.",
			Enabled: true, Editable: true,
			ValueMode: string(dynamic.ValueMode.Normalized()),
		}
		if dynamic.MaxColumns > 0 {
			maxColumns := dynamic.MaxColumns
			step.MaxColumns = &maxColumns
		}
		*steps = append(*steps, step)
	}
	for index, extension := range extensionColumns {
		maxColumns := extension.MaxColumns
		*steps = append(*steps, authoringv2.ImportedSourceStep{
			ID: sourceIndexPath(parentID, "extensionColumns", index), ParentID: parentID,
			Kind: SourcePlanExtensionColumn, SourceKey: extension.Name, Title: "Extension columns: " + extension.Name,
			Summary: "Discover typed columns from this extension source.",
			Enabled: true, Editable: true, MaxColumns: &maxColumns,
		})
	}
	for index, pivot := range pivots {
		step := authoringv2.ImportedSourceStep{
			ID: sourceIndexPath(parentID, "pivots", index), ParentID: parentID,
			Kind: SourcePlanPivot, SourceKey: pivot.Name, Title: "Pivot: " + pivot.Name,
			Summary: pivotSummary(pivot), Enabled: true, Editable: true,
			ValueMode: recipe.NormalizedPivotProjectionMode(pivot.ProjectionMode),
		}
		if pivot.Discovery != nil {
			maxColumns := pivot.Discovery.MaxColumns
			step.MaxColumns = &maxColumns
		}
		*steps = append(*steps, step)
	}
	for index, traversal := range traversals {
		traversalID := sourceIndexPath(parentID, "traversals", index)
		occurrenceID := traversal.OccurrenceID
		if occurrenceID == "" {
			occurrenceID = stableOccurrence(sourcePath(parentID, "traversals"), index, traversal.Name)
		}
		name := strings.TrimSpace(traversal.ToResourceType)
		if name == "" {
			name = strings.TrimSpace(traversal.Name)
		}
		if name == "" {
			name = "related resource"
		}
		via := strings.TrimSpace(traversal.Name)
		title := "Traversal to " + name
		if via != "" {
			title += " via " + via
		}
		summary := "Optional relationship with nested source steps."
		if traversal.MatchMode.Normalized() == recipe.MatchRequired {
			summary = "Required relationship with nested source steps."
		}
		*steps = append(*steps, authoringv2.ImportedSourceStep{
			ID: traversalID, ParentID: parentID, Kind: SourcePlanTraversal, OccurrenceID: occurrenceID, MatchMode: authoringv2.RouteMatchMode(traversal.MatchMode.Normalized()),
			Title: title, Summary: summary,
			Enabled: true, Editable: true,
		})
		appendSourceScope(steps, traversalID, traversal.ToResourceType, traversal.Fields,
			traversal.CatalogProjections, traversal.DynamicColumns, traversal.ExtensionColumns,
			traversal.Pivots, traversal.Traversals)
	}
}

func fieldGroupSummary(fields []recipe.Field, resourceType string) string {
	const maxFieldNames = 8
	names := make([]string, 0, min(len(fields), maxFieldNames))
	for index, field := range fields {
		if index == maxFieldNames {
			break
		}
		name := strings.TrimSpace(field.Name)
		if name == "" {
			name = "unnamed field"
		}
		names = append(names, name)
	}
	summary := fmt.Sprintf("Project %s fields from %s", strings.Join(names, ", "), resourceType)
	if len(fields) > maxFieldNames {
		summary += fmt.Sprintf(" and %d more", len(fields)-maxFieldNames)
	}
	return boundSourceSummary(summary)
}

func boundSourceSummary(summary string) string {
	const maxSummaryBytes = 2048
	if len(summary) <= maxSummaryBytes {
		return summary
	}
	cutoff := 0
	for index := range summary {
		if index > maxSummaryBytes-3 {
			break
		}
		cutoff = index
	}
	return summary[:cutoff] + "..."
}

func pivotSummary(pivot recipe.Pivot) string {
	if pivot.Discovery != nil {
		return "Discover columns from this pivot source."
	}
	return fmt.Sprintf("Project %d configured pivot columns.", len(pivot.Columns))
}

func sourcePath(parentID, name string) string {
	if parentID == "" {
		return name
	}
	return parentID + "." + name
}

func sourceIndexPath(parentID, name string, index int) string {
	return sourcePath(parentID, fmt.Sprintf("%s[%d]", name, index))
}

// ApplySourcePlan applies bounded source-stage overrides to an exact recipe
// output. The caller must pass the pristine output loaded from the pinned
// recipe; this function never mutates that input or accepts recipe expressions.
func ApplySourcePlan(output recipe.Output, plan *authoringv2.ImportedSourcePlan) (recipe.Output, error) {
	if plan == nil {
		return cloneOutput(output)
	}
	if err := plan.Validate(); err != nil {
		return recipe.Output{}, fmt.Errorf("validate imported source plan: %w", err)
	}
	expected := SourcePlan(output)
	if len(plan.Steps) != len(expected.Steps) {
		return recipe.Output{}, fmt.Errorf("imported source plan does not match the pinned source step set")
	}
	steps := make(map[string]authoringv2.ImportedSourceStep, len(plan.Steps))
	for index, step := range plan.Steps {
		base := expected.Steps[index]
		if step.ID != base.ID || step.ParentID != base.ParentID || step.Kind != base.Kind ||
			step.Title != base.Title || step.Summary != base.Summary || step.Editable != base.Editable ||
			(step.SourceKey != "" && step.SourceKey != base.SourceKey) || (step.OccurrenceID != "" && step.OccurrenceID != base.OccurrenceID) || (step.MatchMode != "" && step.MatchMode != base.MatchMode) {
			return recipe.Output{}, fmt.Errorf("imported source plan step %d does not match the pinned source", index)
		}
		if !step.Editable && !step.Enabled {
			return recipe.Output{}, fmt.Errorf("imported source plan step %q is read-only", step.ID)
		}
		if err := validateMaxColumnsOverride(step, base); err != nil {
			return recipe.Output{}, err
		}
		if err := validateValueModeOverride(step, base); err != nil {
			return recipe.Output{}, err
		}
		steps[step.ID] = step
	}

	cloned, err := cloneOutput(output)
	if err != nil {
		return recipe.Output{}, err
	}
	cloned = freezeOccurrences(cloned)
	if len(output.Fields) > 0 && !steps["fields"].Enabled {
		cloned.Fields = nil
	}
	cloned.CatalogProjections = applyCatalogProjections(cloned.CatalogProjections, steps, "")
	cloned.DynamicColumns = applyDynamicColumns(cloned.DynamicColumns, steps, "")
	cloned.ExtensionColumns = applyExtensionColumns(cloned.ExtensionColumns, steps, "")
	cloned.Pivots = applyPivots(cloned.Pivots, steps, "")
	cloned.Traversals = applyTraversals(cloned.Traversals, steps, "")
	return applyRelationshipOverlay(cloned, plan.Relationships)
}

func validateMaxColumnsOverride(step, base authoringv2.ImportedSourceStep) error {
	if (step.MaxColumns == nil) != (base.MaxColumns == nil) {
		return fmt.Errorf("imported source plan step %q changed maxColumns support", step.ID)
	}
	if step.MaxColumns != nil && (*step.MaxColumns < 1 || *step.MaxColumns > *base.MaxColumns) {
		return fmt.Errorf("imported source plan step %q maxColumns must be between 1 and its pinned limit %d", step.ID, *base.MaxColumns)
	}
	return nil
}

func validateValueModeOverride(step, base authoringv2.ImportedSourceStep) error {
	if base.ValueMode == "" {
		if step.ValueMode != "" {
			return fmt.Errorf("imported source plan step %q does not support valueMode", step.ID)
		}
		return nil
	}
	if !validSourceValueMode(step.Kind, step.ValueMode) {
		return fmt.Errorf("imported source plan step %q has an unsupported valueMode %q", step.ID, step.ValueMode)
	}
	return nil
}

func validSourceValueMode(kind, value string) bool {
	switch kind {
	case SourcePlanCatalogProjection, SourcePlanDynamicColumn:
		return value == "AUTO" || value == "FIRST" || value == "ALL" || value == "DISTINCT"
	case SourcePlanPivot:
		return value == recipe.PivotProjectionValue || value == recipe.PivotProjectionFirst ||
			value == recipe.PivotProjectionAll || value == recipe.PivotProjectionDistinct
	default:
		return value == ""
	}
}

func applyCatalogProjections(input []recipe.CatalogProjection, steps map[string]authoringv2.ImportedSourceStep, parentID string) []recipe.CatalogProjection {
	result := make([]recipe.CatalogProjection, 0, len(input))
	for index, item := range input {
		step := steps[sourceIndexPath(parentID, "catalogProjections", index)]
		if !step.Enabled {
			continue
		}
		if step.MaxColumns != nil {
			item.MaxColumns = *step.MaxColumns
		}
		if step.ValueMode != "" {
			item.ValueMode = recipe.ValueMode(step.ValueMode)
		}
		result = append(result, item)
	}
	return result
}

func applyDynamicColumns(input []recipe.DynamicColumn, steps map[string]authoringv2.ImportedSourceStep, parentID string) []recipe.DynamicColumn {
	result := make([]recipe.DynamicColumn, 0, len(input))
	for index, item := range input {
		step := steps[sourceIndexPath(parentID, "dynamicColumns", index)]
		if !step.Enabled {
			continue
		}
		if step.MaxColumns != nil {
			item.MaxColumns = *step.MaxColumns
		}
		if step.ValueMode != "" {
			item.ValueMode = recipe.ValueMode(step.ValueMode)
		}
		result = append(result, item)
	}
	return result
}

func applyExtensionColumns(input []recipe.ExtensionColumn, steps map[string]authoringv2.ImportedSourceStep, parentID string) []recipe.ExtensionColumn {
	result := make([]recipe.ExtensionColumn, 0, len(input))
	for index, item := range input {
		step := steps[sourceIndexPath(parentID, "extensionColumns", index)]
		if !step.Enabled {
			continue
		}
		if step.MaxColumns != nil {
			item.MaxColumns = *step.MaxColumns
		}
		result = append(result, item)
	}
	return result
}

func applyPivots(input []recipe.Pivot, steps map[string]authoringv2.ImportedSourceStep, parentID string) []recipe.Pivot {
	result := make([]recipe.Pivot, 0, len(input))
	for index, item := range input {
		step := steps[sourceIndexPath(parentID, "pivots", index)]
		if !step.Enabled {
			continue
		}
		if step.MaxColumns != nil && item.Discovery != nil {
			item.Discovery.MaxColumns = *step.MaxColumns
		}
		if step.ValueMode != "" {
			item.ProjectionMode = step.ValueMode
		}
		result = append(result, item)
	}
	return result
}

func applyTraversals(input []recipe.Traversal, steps map[string]authoringv2.ImportedSourceStep, parentID string) []recipe.Traversal {
	result := make([]recipe.Traversal, 0, len(input))
	for index, item := range input {
		traversalID := sourceIndexPath(parentID, "traversals", index)
		step := steps[traversalID]
		if !step.Enabled {
			continue
		}
		item.Fields = applyFields(item.Fields, steps, traversalID)
		item.CatalogProjections = applyCatalogProjections(item.CatalogProjections, steps, traversalID)
		item.DynamicColumns = applyDynamicColumns(item.DynamicColumns, steps, traversalID)
		item.ExtensionColumns = applyExtensionColumns(item.ExtensionColumns, steps, traversalID)
		item.Pivots = applyPivots(item.Pivots, steps, traversalID)
		item.Traversals = applyTraversals(item.Traversals, steps, traversalID)
		result = append(result, item)
	}
	return result
}

func applyFields(input []recipe.Field, steps map[string]authoringv2.ImportedSourceStep, parentID string) []recipe.Field {
	if len(input) == 0 || steps[sourcePath(parentID, "fields")].Enabled {
		return input
	}
	return nil
}

func cloneOutput(output recipe.Output) (recipe.Output, error) {
	raw, err := json.Marshal(output)
	if err != nil {
		return recipe.Output{}, fmt.Errorf("encode pinned source output for editing: %w", err)
	}
	var clone recipe.Output
	if err := json.Unmarshal(raw, &clone); err != nil {
		return recipe.Output{}, fmt.Errorf("copy pinned source output for editing: %w", err)
	}
	return clone, nil
}

// HydrateSourcePlan migrates accepted legacy descriptors by adding only
// pristine server-owned metadata. Bounded settings and project overlays retain
// their authored meaning; supplied metadata must already match the exact pin.
func HydrateSourcePlan(output recipe.Output, plan *authoringv2.ImportedSourcePlan) (authoringv2.ImportedSourcePlan, error) {
	pristine := SourcePlan(output)
	if plan == nil {
		return pristine, nil
	}
	if _, err := ApplySourcePlan(output, plan); err != nil {
		return authoringv2.ImportedSourcePlan{}, err
	}
	raw, err := json.Marshal(plan)
	if err != nil {
		return authoringv2.ImportedSourcePlan{}, err
	}
	var cloned authoringv2.ImportedSourcePlan
	if err := json.Unmarshal(raw, &cloned); err != nil {
		return authoringv2.ImportedSourcePlan{}, err
	}
	for i, step := range cloned.Steps {
		pristine.Steps[i].Enabled = step.Enabled
		pristine.Steps[i].MaxColumns = step.MaxColumns
		pristine.Steps[i].ValueMode = step.ValueMode
	}
	pristine.Relationships = cloned.Relationships
	return pristine, nil
}
