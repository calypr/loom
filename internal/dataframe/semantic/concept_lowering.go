package semantic

// This file is the authoring-to-recipe boundary for semantic concepts.  A
// client supplies only stable concept identity and a stable column name;
// selector paths and row-shaping constructs always come from the producer's
// resolved Concept metadata.

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/spec"
)

var conceptColumnNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// ConceptSelectionLowering is the executable recipe fragment plus its audit
// records. The slices are in authored selection order and are never rebuilt
// from labels.
type ConceptSelectionLowering struct {
	Fields         []recipe.Field
	Pivots         []recipe.Pivot
	DynamicColumns []recipe.DynamicColumn
	Columns        []ConceptColumn
}

// LowerBundleConceptSelections returns a copy of bundle with authored concept
// selections translated into ordinary fields, pivots, and dynamic maps. The
// authored selections remain on the copy for publication/audit consumers.
func LowerBundleConceptSelections(bundle recipe.Bundle, catalogByResource map[string]Result) (recipe.Bundle, error) {
	copyBundle := bundle
	copyBundle.Outputs = append([]recipe.Output(nil), bundle.Outputs...)
	for index := range copyBundle.Outputs {
		output := copyBundle.Outputs[index]
		if len(output.ConceptSelections) == 0 {
			continue
		}
		catalog, ok := catalogByResource[output.RootResourceType]
		if !ok {
			return recipe.Bundle{}, conceptDiagnostic(fmt.Sprintf("outputs[%d].conceptSelections", index), "CONCEPT_CATALOG_MISSING", fmt.Sprintf("no producer catalog was supplied for resource %q", output.RootResourceType))
		}
		lowered, err := LowerConceptSelections(output.RootResourceType, output.ConceptSelections, catalog)
		if err != nil {
			return recipe.Bundle{}, fmt.Errorf("outputs[%d] %w", index, err)
		}
		used := map[string]struct{}{}
		for _, field := range output.Fields {
			used[field.Name] = struct{}{}
		}
		for _, field := range lowered.Fields {
			if _, exists := used[field.Name]; exists {
				return recipe.Bundle{}, conceptDiagnostic(fmt.Sprintf("outputs[%d].conceptSelections", index), "COLUMN_NAME_COLLISION", fmt.Sprintf("concept column %q collides with an explicit field", field.Name))
			}
		}
		output.Fields = append(lowered.Fields, output.Fields...)
		output.Pivots = append(lowered.Pivots, output.Pivots...)
		output.DynamicColumns = append(lowered.DynamicColumns, output.DynamicColumns...)
		copyBundle.Outputs[index] = output
	}
	return copyBundle, nil
}

// BuildRecipePlanWithConcepts is the catalog-aware counterpart to
// BuildRecipePlan. It is intentionally additive so legacy recipe callers do
// not need to provide a catalog.
func BuildRecipePlanWithConcepts(bundle recipe.Bundle, bindings recipe.RuntimeBindings, catalogByResource map[string]Result) (RecipePlan, error) {
	lowered, err := LowerBundleConceptSelections(bundle, catalogByResource)
	if err != nil {
		return RecipePlan{}, err
	}
	// The authored selections remain in the persisted bundle returned by
	// LowerBundleConceptSelections. The executable plan carries their identity
	// on each generated field, while the ordinary recipe planner consumes only
	// concrete recipe constructs.
	for index := range lowered.Outputs {
		lowered.Outputs[index].ConceptSelections = nil
	}
	plan, err := BuildRecipePlan(lowered, bindings)
	if err != nil {
		return RecipePlan{}, err
	}
	for index := range plan.Outputs {
		if len(bundle.Outputs[index].ConceptSelections) == 0 {
			continue
		}
		metadata, metadataErr := LowerConceptSelections(bundle.Outputs[index].RootResourceType, bundle.Outputs[index].ConceptSelections, catalogByResource[bundle.Outputs[index].RootResourceType])
		if metadataErr != nil {
			return RecipePlan{}, metadataErr
		}
		plan.Outputs[index].ConceptColumns = metadata.Columns
	}
	return plan, nil
}

// LowerConceptSelections verifies authored identity/rule pairs against a
// producer-resolved catalog and lowers them to ordinary recipe constructs.
// Unknown rule/source strings are accepted when the resolved concept carries
// executable selector metadata; they are producer metadata, not enums.
func LowerConceptSelections(resourceType string, authored []recipe.ConceptSelection, catalog Result) (ConceptSelectionLowering, error) {
	resourceType = strings.TrimSpace(resourceType)
	if resourceType == "" {
		return ConceptSelectionLowering{}, fmt.Errorf("concept selections: resource type is required")
	}
	byID := make(map[string][]Concept, len(catalog.Concepts))
	for _, concept := range catalog.Concepts {
		byID[strings.TrimSpace(concept.ID)] = append(byID[strings.TrimSpace(concept.ID)], concept)
	}
	for _, family := range catalog.Families {
		for _, concept := range family.Concepts {
			if _, exists := byID[concept.ID]; !exists {
				byID[concept.ID] = append(byID[concept.ID], concept)
			}
		}
	}

	out := ConceptSelectionLowering{
		Fields: make([]recipe.Field, 0, len(authored)),
		Pivots: make([]recipe.Pivot, 0), DynamicColumns: make([]recipe.DynamicColumn, 0),
		Columns: make([]ConceptColumn, 0, len(authored)),
	}
	seenColumns := map[string]struct{}{}
	seenConceptIDs := map[string]struct{}{}
	for index, selection := range authored {
		path := fmt.Sprintf("conceptSelections[%d]", index)
		id := strings.TrimSpace(selection.ConceptID)
		ruleID := strings.TrimSpace(selection.RuleID)
		column := strings.TrimSpace(selection.ColumnName)
		if id == "" {
			return ConceptSelectionLowering{}, conceptDiagnostic(path, "CONCEPT_ID_REQUIRED", "conceptId is required")
		}
		if _, exists := seenConceptIDs[id]; exists {
			return ConceptSelectionLowering{}, conceptDiagnostic(path, "CONCEPT_AMBIGUOUS", fmt.Sprintf("concept %q is selected more than once", id))
		}
		seenConceptIDs[id] = struct{}{}
		if ruleID == "" {
			return ConceptSelectionLowering{}, conceptDiagnostic(path, "CONCEPT_RULE_REQUIRED", "ruleId is required")
		}
		if !conceptColumnNamePattern.MatchString(column) {
			return ConceptSelectionLowering{}, conceptDiagnostic(path, "COLUMN_NAME_INVALID", fmt.Sprintf("columnName %q is not a safe stable column name", column))
		}
		if _, exists := seenColumns[column]; exists {
			return ConceptSelectionLowering{}, conceptDiagnostic(path, "COLUMN_NAME_COLLISION", fmt.Sprintf("columnName %q is selected more than once", column))
		}
		seenColumns[column] = struct{}{}
		candidates := byID[id]
		if len(candidates) == 0 {
			return ConceptSelectionLowering{}, conceptDiagnostic(path, "CONCEPT_NOT_FOUND", fmt.Sprintf("concept %q is stale or absent from the producer catalog", id))
		}
		if len(candidates) > 1 {
			return ConceptSelectionLowering{}, conceptDiagnostic(path, "CONCEPT_AMBIGUOUS", fmt.Sprintf("concept %q resolves to %d producer definitions", id, len(candidates)))
		}
		concept := candidates[0]
		if strings.TrimSpace(concept.RuleID) != ruleID {
			return ConceptSelectionLowering{}, conceptDiagnostic(path, "CONCEPT_RULE_MISMATCH", fmt.Sprintf("concept %q was authored with ruleId %q but catalog resolves ruleId %q", id, ruleID, concept.RuleID))
		}
		if strings.TrimSpace(concept.Source.ResourceType) != "" && strings.TrimSpace(concept.Source.ResourceType) != resourceType {
			return ConceptSelectionLowering{}, conceptDiagnostic(path, "CONCEPT_RESOURCE_MISMATCH", fmt.Sprintf("concept %q belongs to resource %q, not %q", id, concept.Source.ResourceType, resourceType))
		}
		fragment, err := lowerResolvedConcept(resourceType, selection, concept, path)
		if err != nil {
			return ConceptSelectionLowering{}, err
		}
		out.Fields = append(out.Fields, fragment.Fields...)
		out.Pivots = append(out.Pivots, fragment.Pivots...)
		out.DynamicColumns = append(out.DynamicColumns, fragment.DynamicColumns...)
		out.Columns = append(out.Columns, fragment.Columns...)
	}
	return out, nil
}

func conceptDiagnostic(path, code, message string) error {
	return fmt.Errorf("%s [%s]: %s", path, code, message)
}

func lowerResolvedConcept(resourceType string, authored recipe.ConceptSelection, concept Concept, path string) (ConceptSelectionLowering, error) {
	s := concept.Output.Selection
	mode := strings.TrimSpace(s.Mode)
	if mode == "" {
		mode = strings.TrimSpace(concept.Output.Mode)
	}
	if mode == "" {
		mode = OutputScalar
	}
	column := ConceptColumn{
		Name: authored.ColumnName, ConceptID: authored.ConceptID, RuleID: authored.RuleID,
		Label: firstNonEmpty(authored.Label, concept.Label), Selector: s,
		LogicalType: firstNonEmpty(concept.Output.ValueType, concept.Source.Primitive, "unknown"),
		Repeated:    concept.Output.Cardinality == CardinalityRepeated || concept.Output.Cardinality == CardinalityPivoted || concept.Source.Repeated,
	}
	result := ConceptSelectionLowering{Columns: []ConceptColumn{column}}
	sourcePath := conceptSelectorPath(resourceType, firstNonEmpty(s.SourcePath, concept.Source.Path))
	if sourcePath == "" && strings.TrimSpace(s.ItemSource) == "" && strings.TrimSpace(s.ValueSelector) == "" {
		return ConceptSelectionLowering{}, conceptDiagnostic(path, "CONCEPT_SELECTOR_MISSING", "resolved concept has no executable sourcePath or itemSource")
	}

	switch mode {
	case OutputDynamicFamily:
		dynamic, err := lowerDynamicConcept(resourceType, authored, concept, path)
		if err != nil {
			return ConceptSelectionLowering{}, err
		}
		result.DynamicColumns = append(result.DynamicColumns, dynamic)
		return result, nil
	case OutputMeasurement:
		if concept.Output.Cardinality == CardinalityPivoted && strings.TrimSpace(s.ItemSource) != "" && len(conceptExamples(concept)) > 0 {
			pivot, err := lowerPivotConcept(resourceType, authored, concept, path)
			if err != nil {
				return ConceptSelectionLowering{}, err
			}
			result.Pivots = append(result.Pivots, pivot)
			return result, nil
		}
		// A measurement without bounded pivot keys is still an executable
		// scalar/array projection. This preserves repeated values as arrays.
	}

	valuePath := joinConceptPath(sourcePath, s.ValueSelector)
	if valuePath == "" {
		valuePath = sourcePath
	}
	if valuePath == "" {
		return ConceptSelectionLowering{}, conceptDiagnostic(path, "CONCEPT_SELECTOR_UNSUPPORTED", "resolved concept selector has no executable value path")
	}
	if err := validateConceptExecutableSelector(resourceType, valuePath); err != nil {
		return ConceptSelectionLowering{}, conceptDiagnostic(path, "CONCEPT_SELECTOR_UNSUPPORTED", err.Error())
	}
	field := recipe.Field{
		Name: authored.ColumnName, FieldRef: authored.ConceptID,
		Expr: recipe.Expression{Select: "root." + valuePath}, ConceptID: authored.ConceptID,
		RuleID: authored.RuleID, Label: firstNonEmpty(authored.Label, concept.Label),
	}
	for _, fallback := range s.ValueFallbacks {
		fallbackPath := joinConceptPath(sourcePath, fallback)
		if fallbackPath != "" {
			field.Fallbacks = append(field.Fallbacks, recipe.Expression{Select: "root." + fallbackPath})
		}
	}
	if column.Repeated {
		field.ValueMode = recipe.ValueModeAll
	}
	result.Fields = append(result.Fields, field)
	return result, nil
}

func lowerDynamicConcept(resourceType string, authored recipe.ConceptSelection, concept Concept, path string) (recipe.DynamicColumn, error) {
	s := concept.Output.Selection
	itemSource := conceptSelectorPath(resourceType, firstNonEmpty(s.ItemSource, s.SourcePath, concept.Source.Path))
	if itemSource == "" {
		return recipe.DynamicColumn{}, conceptDiagnostic(path, "CONCEPT_DYNAMIC_SOURCE_MISSING", "dynamic concept has no executable itemSource")
	}
	if !strings.Contains(itemSource, "[]") {
		itemSource += "[]"
	}
	if err := validateConceptExecutableSelector(resourceType, itemSource); err != nil {
		return recipe.DynamicColumn{}, conceptDiagnostic(path, "CONCEPT_DYNAMIC_SOURCE_UNSUPPORTED", err.Error())
	}
	if strings.TrimSpace(s.KeySelector) == "" || strings.TrimSpace(s.ValueSelector) == "" {
		return recipe.DynamicColumn{}, conceptDiagnostic(path, "CONCEPT_DYNAMIC_SELECTOR_MISSING", "dynamic concept requires keySelector and valueSelector")
	}
	keySelector := selectorRelativeToItem(itemSource, s.KeySelector)
	valueSelector := selectorRelativeToItem(itemSource, s.ValueSelector)
	key := recipe.Expression{Select: "item." + keySelector}
	value := recipe.Expression{Select: "item." + valueSelector}
	dynamic := recipe.DynamicColumn{
		Name: authored.ColumnName, Source: recipe.Expression{Select: "root." + itemSource},
		Key: &key, Value: &value, MaxColumns: 256, Discovered: true,
	}
	if values := conceptExamples(concept); len(values) > 0 {
		dynamic.Columns = values
	}
	return dynamic, nil
}

func lowerPivotConcept(resourceType string, authored recipe.ConceptSelection, concept Concept, path string) (recipe.Pivot, error) {
	s := concept.Output.Selection
	itemSource := conceptSelectorPath(resourceType, s.ItemSource)
	if itemSource == "" {
		return recipe.Pivot{}, conceptDiagnostic(path, "CONCEPT_PIVOT_SOURCE_MISSING", "pivot concept has no executable itemSource")
	}
	if !strings.Contains(itemSource, "[]") {
		itemSource += "[]"
	}
	if err := validateConceptExecutableSelector(resourceType, itemSource); err != nil {
		return recipe.Pivot{}, conceptDiagnostic(path, "CONCEPT_PIVOT_SOURCE_UNSUPPORTED", err.Error())
	}
	columns := conceptExamples(concept)
	if len(columns) == 0 {
		return recipe.Pivot{}, conceptDiagnostic(path, "CONCEPT_PIVOT_COLUMNS_MISSING", "pivot concept requires bounded key examples or catalog columns")
	}
	columnSelector := joinConceptPath(itemSource, selectorRelativeToItem(itemSource, s.KeySelector))
	valueSelector := joinConceptPath(itemSource, selectorRelativeToItem(itemSource, s.ValueSelector))
	pivot := recipe.Pivot{
		Name: authored.ColumnName, FieldRef: authored.ConceptID,
		ColumnExpr:       recipe.Expression{Select: "root." + columnSelector},
		ValueExpr:        recipe.Expression{Select: "root." + valueSelector},
		ItemSource:       recipe.Expression{Select: "root." + itemSource},
		ItemResourceType: resourceType, Columns: columns, Discovered: true,
	}
	for _, fallback := range s.ValueFallbacks {
		fallbackPath := joinConceptPath(itemSource, fallback)
		if fallbackPath != "" {
			pivot.ValueFallbacks = append(pivot.ValueFallbacks, recipe.Expression{Select: "root." + fallbackPath})
		}
	}
	return pivot, nil
}

func conceptExamples(concept Concept) []string {
	values := append([]string(nil), concept.Examples.Values...)
	if len(values) == 0 {
		for _, example := range concept.Source.Examples {
			if example.Safe && strings.TrimSpace(example.Value) != "" {
				values = append(values, strings.TrimSpace(example.Value))
			}
		}
	}
	values = uniqueNonEmpty(values)
	sort.Strings(values)
	return values
}

func conceptSelectorPath(resourceType, path string) string {
	path = strings.TrimSpace(strings.TrimPrefix(path, "root."))
	path = strings.TrimPrefix(path, ".")
	resourcePrefix := strings.TrimSpace(resourceType) + "."
	if strings.HasPrefix(path, resourcePrefix) {
		path = strings.TrimPrefix(path, resourcePrefix)
	}
	if path == strings.TrimSpace(resourceType) {
		return ""
	}
	return path
}

func joinConceptPath(source, relative string) string {
	source = strings.TrimSpace(strings.TrimPrefix(source, "root."))
	relative = strings.TrimSpace(strings.TrimPrefix(relative, "."))
	if relative == "" {
		return source
	}
	if source == "" {
		return relative
	}
	if relative == source || strings.HasPrefix(relative, source+".") {
		return relative
	}
	return source + "." + relative
}

// selectorRelativeToItem converts catalog selectors that are recorded from
// the document root (for example identifier[].value) into selectors relative
// to the repeated item binding used by dynamic maps and pivots (value). Older
// producer catalogs may already contain item-relative selectors, which are
// retained unchanged.
func selectorRelativeToItem(itemSource, selector string) string {
	itemSource = strings.TrimSpace(strings.TrimPrefix(itemSource, "root."))
	selector = strings.TrimSpace(strings.TrimPrefix(selector, "root."))
	selector = strings.TrimPrefix(selector, ".")
	if itemSource == "" {
		return selector
	}
	if selector == itemSource {
		return ""
	}
	if strings.HasPrefix(selector, itemSource+".") {
		return strings.TrimPrefix(selector, itemSource+".")
	}
	return selector
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func uniqueNonEmpty(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func validateConceptExecutableSelector(resourceType, path string) error {
	selector, err := spec.ParseSelector(path)
	if err != nil {
		return err
	}
	if _, _, err := spec.SelectorCardinality(resourceType, selector); err != nil {
		return err
	}
	return nil
}
