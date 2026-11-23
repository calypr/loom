package recipeimport

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/dataframe/recipe"
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

func SourcePlan(output recipe.Output) ws.ImportedSourcePlan {
	plan := ws.ImportedSourcePlan{Steps: []ws.ImportedSourceStep{{ID: "output", Kind: "OUTPUT", Title: "Output settings", Summary: "Row grain and output column collision policy.", Editable: true}}}
	if output.Expand != nil {
		plan.Steps = append(plan.Steps, ws.ImportedSourceStep{
			ID: "expand", Kind: SourcePlanExpansion, Title: "Expand source rows",
			Summary: expansionSummary(*output.Expand), Editable: true,
		})
	}
	if output.Identity != nil {
		plan.Steps = append(plan.Steps, ws.ImportedSourceStep{
			ID: "identity", Kind: SourcePlanIdentity, Title: "Source row identity",
			Summary: "The pinned recipe defines how source rows receive stable identities.", Editable: output.Identity.Expansion == nil,
		})
	}
	appendSourceScope(&plan.Steps, "", output.RootResourceType, output.Fields, output.CatalogProjections,
		output.DynamicColumns, output.ExtensionColumns, output.Pivots, output.Traversals)
	parameters := sourceParameters(output)
	for i := range plan.Steps {
		plan.Steps[i].Parameters = parameters[plan.Steps[i].ID]
	}
	return plan
}

func expansionSummary(expansion recipe.Expansion) string {
	selector := expansion.From.Select
	if selector == "" {
		selector = "the configured repeated value"
	}
	return fmt.Sprintf("Create one row per value from %s; when empty, %s.", selector, strings.ToLower(string(expansion.EmptyPolicy.Normalized())))
}

func appendSourceScope(steps *[]ws.ImportedSourceStep, parentID, resourceType string,
	fields []recipe.Field, catalogProjections []recipe.CatalogProjection, dynamicColumns []recipe.DynamicColumn,
	extensionColumns []recipe.ExtensionColumn, pivots []recipe.Pivot, traversals []recipe.Traversal,
) {
	if len(fields) > 0 {
		label := strings.TrimSpace(resourceType)
		if label == "" {
			label = "source"
		}
		*steps = append(*steps, ws.ImportedSourceStep{
			ID: sourcePath(parentID, "fields"), ParentID: parentID, Kind: SourcePlanFieldGroup,
			Title:    "Fields from " + label,
			Summary:  fieldGroupSummary(fields, label),
			Editable: true,
		})
	}
	for index, projection := range catalogProjections {
		maxColumns := projection.MaxColumns
		*steps = append(*steps, ws.ImportedSourceStep{
			ID: sourceIndexPath(parentID, "catalogProjections", index), ParentID: parentID,
			Kind: SourcePlanCatalogProjection, SourceKey: projection.Name, Title: "Catalog fields: " + projection.Name,
			Summary:  "Discover populated source fields.",
			Editable: true, MaxColumns: &maxColumns,
			ValueMode: string(projection.ValueMode.Normalized()),
		})
	}
	for index, dynamic := range dynamicColumns {
		step := ws.ImportedSourceStep{
			ID: sourceIndexPath(parentID, "dynamicColumns", index), ParentID: parentID,
			Kind: SourcePlanDynamicColumn, SourceKey: dynamic.Name, Title: "Dynamic columns: " + dynamic.Name,
			Summary:   "Discover columns from this keyed source.",
			Editable:  true,
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
		*steps = append(*steps, ws.ImportedSourceStep{
			ID: sourceIndexPath(parentID, "extensionColumns", index), ParentID: parentID,
			Kind: SourcePlanExtensionColumn, SourceKey: extension.Name, Title: "Extension columns: " + extension.Name,
			Summary:  "Discover typed columns from this extension source.",
			Editable: true, MaxColumns: &maxColumns,
		})
	}
	for index, pivot := range pivots {
		step := ws.ImportedSourceStep{
			ID: sourceIndexPath(parentID, "pivots", index), ParentID: parentID,
			Kind: SourcePlanPivot, SourceKey: pivot.Name, Title: "Pivot: " + pivot.Name,
			Summary: pivotSummary(pivot), Editable: true,
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
		*steps = append(*steps, ws.ImportedSourceStep{
			ID: traversalID, ParentID: parentID, Kind: SourcePlanTraversal, OccurrenceID: occurrenceID, MatchMode: ws.RouteMatchMode(traversal.MatchMode.Normalized()),
			Title: title, Summary: summary,
			Editable: true,
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

func ApplySourcePlan(output recipe.Output, plan *ws.ImportedSourcePlan) (recipe.Output, error) {
	if plan == nil {
		return cloneOutput(output)
	}
	if err := plan.Validate(); err != nil {
		return recipe.Output{}, fmt.Errorf("validate imported source plan: %w", err)
	}
	expected := SourcePlan(output)
	expectedSteps, err := retainedPinnedSteps(expected.Steps, plan.RemovedStepIDs)
	if err != nil {
		return recipe.Output{}, err
	}
	expected.Steps = expectedSteps
	if len(plan.Steps) == len(expected.Steps)-1 && (len(plan.Steps) == 0 || plan.Steps[0].ID != "output") {
		copyPlan := *plan
		copyPlan.Steps = append([]ws.ImportedSourceStep{expected.Steps[0]}, plan.Steps...)
		plan = &copyPlan
	}
	extraSteps, err := sourceRuleSteps(expected.Steps, plan)
	if err != nil {
		return recipe.Output{}, err
	}
	if len(plan.Steps) != len(expected.Steps)+len(extraSteps) {
		return recipe.Output{}, fmt.Errorf("imported source plan does not match the pinned source step set")
	}
	steps := make(map[string]ws.ImportedSourceStep, len(plan.Steps))
	for index, step := range plan.Steps {
		if index >= len(expected.Steps) {
			if !reflect.DeepEqual(step, extraSteps[index-len(expected.Steps)]) {
				return recipe.Output{}, fmt.Errorf("imported source plan step %d does not match its rule or relationship", index)
			}
			continue
		}
		base := expected.Steps[index]
		if step.ID != base.ID || step.ParentID != base.ParentID || step.Kind != base.Kind ||
			step.Title != base.Title || step.Summary != base.Summary || (step.Editable != base.Editable && !(base.Editable && (step.Kind == SourcePlanExpansion || step.Kind == SourcePlanIdentity))) ||
			(step.SourceKey != "" && step.SourceKey != base.SourceKey) || (step.OccurrenceID != "" && step.OccurrenceID != base.OccurrenceID) || (step.MatchMode != "" && step.MatchMode != base.MatchMode) {
			return recipe.Output{}, fmt.Errorf("imported source plan step %d does not match the pinned source", index)
		}
		if err := validateMaxColumnsOverride(step, base); err != nil {
			return recipe.Output{}, err
		}
		if err := validateValueModeOverride(step, base); err != nil {
			return recipe.Output{}, err
		}
		if step.Parameters != nil && !reflect.DeepEqual(step.Parameters, base.Parameters) {
			if err := step.Parameters.Validate(""); err != nil {
				return recipe.Output{}, fmt.Errorf("source step %q: %w", step.ID, err)
			}
			if err := validateSourceParameterExpressionEdits(base.Parameters, step.Parameters); err != nil {
				return recipe.Output{}, fmt.Errorf("source step %q: %w", step.ID, err)
			}
		}
		steps[step.ID] = step
	}

	cloned, err := cloneOutput(output)
	if err != nil {
		return recipe.Output{}, err
	}
	cloned = freezeOccurrences(cloned)
	if err := applyFieldBindings(&cloned, plan.FieldBindings); err != nil {
		return recipe.Output{}, err
	}
	if err := applyParameterOverrides(&cloned, steps); err != nil {
		return recipe.Output{}, err
	}
	if _, present := steps["fields"]; len(output.Fields) > 0 && !present {
		cloned.Fields = nil
	}
	cloned.CatalogProjections = applyCatalogProjections(cloned.CatalogProjections, steps, "")
	cloned.DynamicColumns = applyDynamicColumns(cloned.DynamicColumns, steps, "")
	cloned.ExtensionColumns = applyExtensionColumns(cloned.ExtensionColumns, steps, "")
	cloned.Pivots = applyPivots(cloned.Pivots, steps, "")
	cloned.Traversals = applyTraversals(cloned.Traversals, steps, "")
	cloned, err = applyRelationshipOverlay(cloned, plan.Relationships)
	if err != nil {
		return recipe.Output{}, err
	}
	if err := applySourceRules(&cloned, plan.RuleAdditions); err != nil {
		return recipe.Output{}, err
	}
	return cloned, nil
}

func applySourceRules(output *recipe.Output, additions []ws.ImportedSourceRuleAddition) error {
	if len(additions) == 0 {
		return nil
	}
	occurrences := indexTraversalOccurrences(output.Traversals)
	for _, addition := range additions {
		resourceType, alias := output.RootResourceType, "root"
		dynamics, extensions, catalogs := &output.DynamicColumns, &output.ExtensionColumns, &output.CatalogProjections
		if addition.OccurrenceID != "" {
			traversal := occurrences[addition.OccurrenceID]
			if traversal == nil {
				return &ws.UnavailableSourceRuleOccurrence{StepID: addition.StepID, OccurrenceID: addition.OccurrenceID}
			}
			resourceType, alias = traversal.ToResourceType, strings.TrimSpace(traversal.Alias)
			dynamics, extensions, catalogs = &traversal.DynamicColumns, &traversal.ExtensionColumns, &traversal.CatalogProjections
			if alias == "" {
				alias = traversal.Name
			}
		}
		params := relativeSourceRuleParameters(addition.OccurrenceID, addition.Parameters)
		if err := validateRuleAddition(addition.Kind, resourceType, params); err != nil {
			return fmt.Errorf("source step %q: %w", addition.StepID, err)
		}
		limit := ws.MaxImportedRuleColumns
		if addition.MaxColumns != nil {
			limit = *addition.MaxColumns
		}
		switch addition.Kind {
		case SourcePlanDynamicColumn:
			dynamic := params.Dynamic
			column := recipe.DynamicColumn{
				Name: dynamic.Name, ColumnPrefix: dynamic.ColumnPrefix,
				Source: recipe.Expression{Select: alias + "." + dynamic.Source.Select},
				Key:    dynamic.Key, Value: dynamic.Value, MaxColumns: limit,
			}
			for _, override := range dynamic.Outputs {
				column.OutputOverrides = append(column.OutputOverrides, recipe.DynamicColumnOutputOverride{Key: override.Key, Enabled: override.Enabled})
			}
			*dynamics = append(*dynamics, column)
		case SourcePlanExtensionColumn:
			extension := params.Extension
			*extensions = append(*extensions, recipe.ExtensionColumn{
				Name: extension.Name, ColumnPrefix: extension.ColumnPrefix,
				Source: recipe.Expression{Select: alias + "." + extension.Source.Select}, MaxColumns: limit,
			})
		case SourcePlanCatalogProjection:
			*catalogs = append(*catalogs, catalogRuleProjection(params.Catalog, limit))
		}
	}
	return nil
}

func relativeSourceRuleParameters(occurrenceID string, params *ws.ImportedSourceParameters) *ws.ImportedSourceParameters {
	if occurrenceID != "" || params.Dynamic == nil || params.Dynamic.Source.Select != "root.identifier[]" {
		return params
	}
	dynamic := *params.Dynamic
	dynamic.Source.Select = "identifier[]"
	result := *params
	result.Dynamic = &dynamic
	return &result
}

func ruleAdditionStep(addition ws.ImportedSourceRuleAddition, parentID string) ws.ImportedSourceStep {
	name, title, summary := "", "", "Generate columns from this source rule."
	switch addition.Kind {
	case SourcePlanDynamicColumn:
		name, title, summary = addition.Parameters.Dynamic.Name, "Dynamic columns", "Discover columns from this keyed source."
	case SourcePlanExtensionColumn:
		name, title = addition.Parameters.Extension.Name, "Extension columns"
	case SourcePlanCatalogProjection:
		name, title = addition.Parameters.Catalog.Name, "Catalog columns"
	}
	return ws.ImportedSourceStep{
		ID: addition.StepID, Kind: addition.Kind, SourceKey: name, ParentID: parentID, OccurrenceID: addition.OccurrenceID,
		Title: title + ": " + name, Summary: summary,
		Editable: true, Parameters: addition.Parameters, MaxColumns: addition.MaxColumns,
	}
}

func sourceRuleSteps(base []ws.ImportedSourceStep, plan *ws.ImportedSourcePlan) ([]ws.ImportedSourceStep, error) {
	parents := map[string]string{"": ""}
	for _, step := range base {
		if step.Kind == SourcePlanTraversal {
			parents[step.OccurrenceID] = step.ID
		}
	}
	var steps []ws.ImportedSourceStep
	if plan.Relationships != nil && len(plan.RuleAdditions) > 0 {
		needed := make(map[string]bool, len(plan.RuleAdditions))
		for _, rule := range plan.RuleAdditions {
			needed[rule.OccurrenceID] = true
		}
		for i := len(plan.Relationships.Additions) - 1; i >= 0; i-- {
			addition := plan.Relationships.Additions[i]
			if needed[addition.OccurrenceID] {
				needed[addition.ParentOccurrenceID] = true
			}
		}
		for _, addition := range plan.Relationships.Additions {
			if !needed[addition.OccurrenceID] {
				continue
			}
			id := "relationships." + addition.OccurrenceID
			steps = append(steps, ws.ImportedSourceStep{
				ID: id, ParentID: parents[addition.ParentOccurrenceID], Kind: SourcePlanTraversal,
				OccurrenceID: addition.OccurrenceID, Title: "Related " + addition.ResourceType,
				Summary: "Connected through " + addition.Relationship + ".",
			})
			parents[addition.OccurrenceID] = id
		}
	}
	for _, addition := range plan.RuleAdditions {
		parent, ok := parents[addition.OccurrenceID]
		if !ok {
			return nil, &ws.UnavailableSourceRuleOccurrence{StepID: addition.StepID, OccurrenceID: addition.OccurrenceID}
		}
		steps = append(steps, ruleAdditionStep(addition, parent))
	}
	return steps, nil
}

func validateMaxColumnsOverride(step, base ws.ImportedSourceStep) error {
	if (step.MaxColumns == nil) != (base.MaxColumns == nil) {
		return fmt.Errorf("imported source plan step %q changed maxColumns support", step.ID)
	}
	if step.MaxColumns != nil && (*step.MaxColumns < 1 || *step.MaxColumns > *base.MaxColumns) {
		return fmt.Errorf("imported source plan step %q maxColumns must be between 1 and its pinned limit %d", step.ID, *base.MaxColumns)
	}
	return nil
}

func validateValueModeOverride(step, base ws.ImportedSourceStep) error {
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

func applyCatalogProjections(input []recipe.CatalogProjection, steps map[string]ws.ImportedSourceStep, parentID string) []recipe.CatalogProjection {
	result := make([]recipe.CatalogProjection, 0, len(input))
	for index, item := range input {
		step := steps[sourceIndexPath(parentID, "catalogProjections", index)]
		if step.ID == "" {
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

func applyDynamicColumns(input []recipe.DynamicColumn, steps map[string]ws.ImportedSourceStep, parentID string) []recipe.DynamicColumn {
	result := make([]recipe.DynamicColumn, 0, len(input))
	for index, item := range input {
		step := steps[sourceIndexPath(parentID, "dynamicColumns", index)]
		if step.ID == "" {
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

func applyExtensionColumns(input []recipe.ExtensionColumn, steps map[string]ws.ImportedSourceStep, parentID string) []recipe.ExtensionColumn {
	result := make([]recipe.ExtensionColumn, 0, len(input))
	for index, item := range input {
		step := steps[sourceIndexPath(parentID, "extensionColumns", index)]
		if step.ID == "" {
			continue
		}
		if step.MaxColumns != nil {
			item.MaxColumns = *step.MaxColumns
		}
		result = append(result, item)
	}
	return result
}

func applyPivots(input []recipe.Pivot, steps map[string]ws.ImportedSourceStep, parentID string) []recipe.Pivot {
	result := make([]recipe.Pivot, 0, len(input))
	for index, item := range input {
		step := steps[sourceIndexPath(parentID, "pivots", index)]
		if step.ID == "" {
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

func applyTraversals(input []recipe.Traversal, steps map[string]ws.ImportedSourceStep, parentID string) []recipe.Traversal {
	result := make([]recipe.Traversal, 0, len(input))
	for index, item := range input {
		traversalID := sourceIndexPath(parentID, "traversals", index)
		step := steps[traversalID]
		if step.ID == "" {
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

func applyFields(input []recipe.Field, steps map[string]ws.ImportedSourceStep, parentID string) []recipe.Field {
	if len(input) == 0 || steps[sourcePath(parentID, "fields")].ID != "" {
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

func HydrateSourcePlan(output recipe.Output, plan *ws.ImportedSourcePlan) (ws.ImportedSourcePlan, error) {
	pristine := SourcePlan(output)
	if plan == nil {
		return pristine, nil
	}
	if _, err := ApplySourcePlan(output, plan); err != nil {
		return ws.ImportedSourcePlan{}, err
	}
	raw, err := json.Marshal(plan)
	if err != nil {
		return ws.ImportedSourcePlan{}, err
	}
	var cloned ws.ImportedSourcePlan
	if err := json.Unmarshal(raw, &cloned); err != nil {
		return ws.ImportedSourcePlan{}, err
	}
	pristine.Steps, err = retainedPinnedSteps(pristine.Steps, cloned.RemovedStepIDs)
	if err != nil {
		return ws.ImportedSourcePlan{}, err
	}
	for _, step := range cloned.Steps {
		for i := range pristine.Steps {
			if pristine.Steps[i].ID != step.ID {
				continue
			}
			pristine.Steps[i].MaxColumns = step.MaxColumns
			pristine.Steps[i].ValueMode = step.ValueMode
			if step.Parameters != nil {
				pristine.Steps[i].Parameters = step.Parameters
			}
		}
	}
	ruleSteps, err := sourceRuleSteps(pristine.Steps, &cloned)
	if err != nil {
		return ws.ImportedSourcePlan{}, err
	}
	pristine.Steps = append(pristine.Steps, ruleSteps...)
	pristine.FieldBindings = cloned.FieldBindings
	pristine.RuleAdditions = cloned.RuleAdditions
	pristine.Relationships = cloned.Relationships
	pristine.RemovedStepIDs = cloned.RemovedStepIDs
	pristine.NextRuleIndex = cloned.NextRuleIndex
	return pristine, nil
}

func retainedPinnedSteps(steps []ws.ImportedSourceStep, removals []string) ([]ws.ImportedSourceStep, error) {
	explicit := make(map[string]bool, len(removals))
	for _, id := range removals {
		explicit[id] = true
	}
	removed := make(map[string]bool, len(removals))
	retained := make([]ws.ImportedSourceStep, 0, len(steps))
	for _, step := range steps {
		if explicit[step.ID] {
			delete(explicit, step.ID)
			if !step.Editable || step.Kind == "OUTPUT" || step.Kind == SourcePlanExpansion || step.Kind == SourcePlanIdentity {
				return nil, fmt.Errorf("source step %q cannot be removed", step.ID)
			}
			removed[step.ID] = true
		}
		if removed[step.ID] || removed[step.ParentID] {
			removed[step.ID] = true
			continue
		}
		retained = append(retained, step)
	}
	for id := range explicit {
		return nil, fmt.Errorf("removed source step %q was not found in the pinned recipe", id)
	}
	return retained, nil
}
