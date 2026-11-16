package authoringv2

import (
	"fmt"
	"strings"
)

// ConstructionPivotDiscoveryCategory is one category observed for a PIVOT's
// exact category/value pair. Name and Label are the public output name and
// label the resolver should use; they are not persisted evidence.
type ConstructionPivotDiscoveryCategory struct {
	Key   TableScalar
	Name  string
	Label string
}

// ResolveConstructionPivotDiscovery freezes a PIVOT discovery declaration into
// concrete categories and stage outputs. The caller must supply complete,
// value-bearing evidence for the pivot's exact category/value pair, usually
// from the compiler category scan. The resolver derives stable output IDs and
// clears the declaration before validation and lowering.
func ResolveConstructionPivotDiscovery(step ConstructionStep, input []StageColumn, evidence []ConstructionPivotDiscoveryCategory) (ConstructionStep, error) {
	if step.Operation.Kind != ConstructionOperationPivot || step.Operation.Pivot == nil {
		return ConstructionStep{}, fmt.Errorf("discovery resolution requires a PIVOT step")
	}
	pivot := step.Operation.Pivot
	if pivot.Discovery == nil {
		return ConstructionStep{}, fmt.Errorf("pivot has no discovery declaration")
	}
	if len(pivot.Categories) != 0 {
		return ConstructionStep{}, fmt.Errorf("pivot cannot combine explicit categories with discovery")
	}
	if pivot.Discovery.MaxCategories <= 0 || pivot.Discovery.MaxCategories > maxPivotCategories {
		return ConstructionStep{}, fmt.Errorf("pivot discovery maxCategories must be 1..%d", maxPivotCategories)
	}
	inputByID, err := stageColumnIndex(input)
	if err != nil {
		return ConstructionStep{}, fmt.Errorf("pivot input schema: %w", err)
	}
	category, categoryOK := inputByID[pivot.CategoryColumnID]
	value, valueOK := inputByID[pivot.ValueColumnID]
	if !categoryOK || !valueOK || pivot.CategoryColumnID == pivot.ValueColumnID {
		return ConstructionStep{}, fmt.Errorf("pivot discovery requires a distinct, present category/value pair")
	}
	categoryKind, categoryKnown := constructionPivotScalarKind(category.Type)
	if !categoryKnown {
		return ConstructionStep{}, fmt.Errorf("pivot discovery category column %q type %q is not a supported scalar pair", pivot.CategoryColumnID, category.Type)
	}
	if _, valueKnown := constructionPivotScalarKind(value.Type); !valueKnown {
		return ConstructionStep{}, fmt.Errorf("pivot discovery value column %q type %q is not a supported scalar pair", pivot.ValueColumnID, value.Type)
	}
	if len(evidence) == 0 {
		return ConstructionStep{}, fmt.Errorf("pivot discovery requires at least one value-bearing category")
	}
	if len(evidence) > pivot.Discovery.MaxCategories {
		return ConstructionStep{}, fmt.Errorf("pivot discovery found %d categories, exceeding maxCategories %d", len(evidence), pivot.Discovery.MaxCategories)
	}

	outputs := make([]StageColumn, 0, len(pivot.GroupKeyIDs)+len(evidence))
	for _, id := range pivot.GroupKeyIDs {
		column, exists := inputByID[id]
		if !exists {
			return ConstructionStep{}, missingConstructionColumn("groupKeyIds", id)
		}
		outputs = append(outputs, column)
	}
	categories := make([]ConstructionPivotCategory, 0, len(evidence))
	seenKeys, seenNames, seenIDs := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for index, item := range evidence {
		if err := item.Key.ValidatePivotCategoryKey(); err != nil {
			return ConstructionStep{}, fmt.Errorf("categories[%d].key: %w", index, err)
		}
		if item.Key.Kind != TableScalarNull && item.Key.Kind != TableScalarMissing && string(item.Key.Kind) != categoryKind {
			return ConstructionStep{}, fmt.Errorf("categories[%d].key type %q does not match category column type %q", index, item.Key.Kind, category.Type)
		}
		identity := item.Key.identity()
		if seenKeys[identity] {
			return ConstructionStep{}, fmt.Errorf("pivot discovery contains duplicate category key at index %d", index)
		}
		seenKeys[identity] = true
		name := strings.TrimSpace(item.Name)
		if name == "" {
			return ConstructionStep{}, fmt.Errorf("categories[%d].name is required", index)
		}
		if seenNames[name] {
			return ConstructionStep{}, fmt.Errorf("pivot discovery contains duplicate output name %q", name)
		}
		seenNames[name] = true
		label := strings.TrimSpace(item.Label)
		if label == "" {
			label = name
		}
		outputID := stableConstructionID("column", step.ID, "pivot_discovery", identity)
		if seenIDs[outputID] || inputByID[outputID].ID != "" {
			return ConstructionStep{}, fmt.Errorf("pivot discovery generated duplicate output id %q", outputID)
		}
		seenIDs[outputID] = true
		categories = append(categories, ConstructionPivotCategory{Key: cloneTableScalar(item.Key), OutputColumnID: outputID})
		outputs = append(outputs, StageColumn{ID: outputID, Name: name, Label: label, Type: value.Type})
	}

	resolved := step
	operation := *step.Operation.Pivot
	operation.Categories = categories
	operation.Discovery = nil
	resolved.Operation.Pivot = &operation
	resolved.Outputs = outputs
	if err := validateConstructionPivot(resolved, inputByID); err != nil {
		return ConstructionStep{}, err
	}
	return resolved, nil
}
