package recipe

import (
	"fmt"
	"strings"
)

// archivedTableReshape freezes the retired stored wire for identity verification only.
// Current validation rejects this payload; it has no lowering path.
type archivedTableReshape struct {
	Kind         archivedReshapeKind   `json:"kind"`
	GroupedPivot *archivedGroupedPivot `json:"groupedPivot,omitempty"`
	Unpivot      *archivedUnpivot      `json:"unpivot,omitempty"`
}

type archivedReshapeKind string

const (
	archivedReshapeGroupedPivot archivedReshapeKind = "GROUPED_PIVOT"
	archivedReshapeUnpivot      archivedReshapeKind = "UNPIVOT"
)

type archivedGroupedPivot struct {
	ConstructionID         string                         `json:"constructionId"`
	GroupKeys              []string                       `json:"groupKeys"`
	CategoryColumn         string                         `json:"categoryColumn"`
	ValueColumn            string                         `json:"valueColumn"`
	Categories             []archivedGroupedPivotCategory `json:"categories"`
	DuplicatePolicy        PivotDuplicatePolicy           `json:"duplicatePolicy"`
	MissingCellPolicy      PivotMissingCellPolicy         `json:"missingCellPolicy"`
	UnlistedCategoryPolicy PivotUnlistedCategoryPolicy    `json:"unlistedCategoryPolicy"`
}

type archivedGroupedPivotCategory struct {
	Key    archivedTableScalar `json:"key"`
	Output string              `json:"output"`
	Label  string              `json:"label"`
}

type archivedUnpivot struct {
	ConstructionID string                 `json:"constructionId"`
	Inputs         []archivedUnpivotInput `json:"inputs"`
	KeyOutput      string                 `json:"keyOutput"`
	KeyLabel       string                 `json:"keyLabel"`
	ValueOutput    string                 `json:"valueOutput"`
	ValueLabel     string                 `json:"valueLabel"`
	NullRowPolicy  UnpivotNullRowPolicy   `json:"nullRowPolicy"`
}

type archivedUnpivotInput struct {
	Column string              `json:"column"`
	Key    archivedTableScalar `json:"key"`
}

func (reshape archivedTableReshape) Validate() error {
	if reshape.Kind == archivedReshapeGroupedPivot && reshape.GroupedPivot != nil && reshape.Unpivot == nil {
		return validateArchivedGroupedPivot(*reshape.GroupedPivot)
	}
	if reshape.Kind == archivedReshapeUnpivot && reshape.Unpivot != nil && reshape.GroupedPivot == nil {
		return validateArchivedUnpivot(*reshape.Unpivot)
	}
	return fmt.Errorf("table reshape must contain exactly one payload matching kind")
}

func validateArchivedGroupedPivot(pivot archivedGroupedPivot) error {
	if strings.TrimSpace(pivot.ConstructionID) == "" {
		return fmt.Errorf("grouped pivot constructionId is required")
	}
	if len(pivot.GroupKeys) == 0 {
		return fmt.Errorf("grouped pivot groupKeys must be non-empty")
	}
	if strings.TrimSpace(pivot.CategoryColumn) == "" || strings.TrimSpace(pivot.ValueColumn) == "" {
		return fmt.Errorf("grouped pivot categoryColumn and valueColumn are required")
	}
	if pivot.CategoryColumn == pivot.ValueColumn {
		return fmt.Errorf("grouped pivot categoryColumn and valueColumn must differ")
	}
	seenColumns := make(map[string]bool, len(pivot.GroupKeys)+2)
	for _, key := range pivot.GroupKeys {
		if strings.TrimSpace(key) == "" || seenColumns[key] {
			return fmt.Errorf("grouped pivot groupKeys must be non-empty and unique")
		}
		seenColumns[key] = true
	}
	if seenColumns[pivot.CategoryColumn] || seenColumns[pivot.ValueColumn] {
		return fmt.Errorf("grouped pivot group keys must differ from category and value columns")
	}
	if len(pivot.Categories) == 0 || len(pivot.Categories) > 256 {
		return fmt.Errorf("grouped pivot categories must contain between 1 and 256 entries")
	}
	categoryKeys := make(map[string]bool, len(pivot.Categories))
	outputs := make(map[string]bool, len(pivot.Categories))
	for index, category := range pivot.Categories {
		if err := category.Key.ValidatePivotCategoryKey(); err != nil {
			return fmt.Errorf("categories[%d].key: %w", index, err)
		}
		identity := category.Key.identity()
		if categoryKeys[identity] {
			return fmt.Errorf("grouped pivot category keys must be unique")
		}
		categoryKeys[identity] = true
		if err := validateRecipeName(category.Output, fmt.Sprintf("categories[%d].output", index)); err != nil {
			return err
		}
		if strings.TrimSpace(category.Label) == "" {
			return fmt.Errorf("categories[%d].label is required", index)
		}
		if outputs[category.Output] {
			return fmt.Errorf("grouped pivot output %q is duplicated", category.Output)
		}
		if seenColumns[category.Output] || category.Output == pivot.CategoryColumn || category.Output == pivot.ValueColumn {
			return fmt.Errorf("grouped pivot output %q collides with a group key, category, or value column", category.Output)
		}
		outputs[category.Output] = true
	}
	switch pivot.DuplicatePolicy {
	case PivotDuplicateError, PivotDuplicateSum, PivotDuplicateMin, PivotDuplicateMax:
	default:
		return fmt.Errorf("unsupported grouped pivot duplicate policy %q", pivot.DuplicatePolicy)
	}
	switch pivot.MissingCellPolicy {
	case PivotMissingCellNull, PivotMissingCellError:
	default:
		return fmt.Errorf("unsupported grouped pivot missing-cell policy %q", pivot.MissingCellPolicy)
	}
	switch pivot.UnlistedCategoryPolicy {
	case PivotUnlistedCategoryError, PivotUnlistedCategoryExcludeWithEvidence:
	default:
		return fmt.Errorf("unsupported grouped pivot unlisted-category policy %q", pivot.UnlistedCategoryPolicy)
	}
	return nil
}

func validateArchivedUnpivot(unpivot archivedUnpivot) error {
	if strings.TrimSpace(unpivot.ConstructionID) == "" {
		return fmt.Errorf("unpivot constructionId is required")
	}
	if len(unpivot.Inputs) == 0 {
		return fmt.Errorf("unpivot inputs must be non-empty")
	}
	if err := validateRecipeName(unpivot.KeyOutput, "keyOutput"); err != nil {
		return err
	}
	if err := validateRecipeName(unpivot.ValueOutput, "valueOutput"); err != nil {
		return err
	}
	if unpivot.KeyOutput == unpivot.ValueOutput {
		return fmt.Errorf("unpivot keyOutput and valueOutput must differ")
	}
	if strings.TrimSpace(unpivot.KeyLabel) == "" || strings.TrimSpace(unpivot.ValueLabel) == "" {
		return fmt.Errorf("unpivot keyLabel and valueLabel are required")
	}
	columns := make(map[string]bool, len(unpivot.Inputs))
	keys := make(map[string]bool, len(unpivot.Inputs))
	for index, input := range unpivot.Inputs {
		if err := validateRecipeName(input.Column, fmt.Sprintf("inputs[%d].column", index)); err != nil {
			return err
		}
		if columns[input.Column] {
			return fmt.Errorf("unpivot input column %q is duplicated", input.Column)
		}
		columns[input.Column] = true
		if err := input.Key.ValidateConcreteValue(); err != nil {
			return fmt.Errorf("inputs[%d].key: %w", index, err)
		}
		identity := input.Key.identity()
		if keys[identity] {
			return fmt.Errorf("unpivot keys must be unique")
		}
		keys[identity] = true
	}
	switch unpivot.NullRowPolicy {
	case UnpivotNullDrop, UnpivotNullPreserve:
	default:
		return fmt.Errorf("unsupported unpivot null-row policy %q", unpivot.NullRowPolicy)
	}
	return nil
}
