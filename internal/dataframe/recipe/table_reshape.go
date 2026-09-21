package recipe

import (
	"fmt"
	"math"
	"strings"
)

// TableReshape is a document-level row operation over the finalized authored
// output columns. It is distinct from Pivot, which projects correlated values
// from one FHIR row.
type TableReshape struct {
	Kind         TableReshapeKind `json:"kind"`
	GroupedPivot *GroupedPivot    `json:"groupedPivot,omitempty"`
	Unpivot      *Unpivot         `json:"unpivot,omitempty"`
}

type TableReshapeKind string

const (
	TableReshapeGroupedPivot TableReshapeKind = "GROUPED_PIVOT"
	TableReshapeUnpivot      TableReshapeKind = "UNPIVOT"
)

type TableScalar struct {
	Kind    TableScalarKind `json:"kind"`
	String  *string         `json:"string,omitempty"`
	Integer *int64          `json:"integer,omitempty"`
	Decimal *float64        `json:"decimal,omitempty"`
	Boolean *bool           `json:"boolean,omitempty"`
}

type TableScalarKind string

const (
	TableScalarString  TableScalarKind = "STRING"
	TableScalarInteger TableScalarKind = "INTEGER"
	TableScalarDecimal TableScalarKind = "DECIMAL"
	TableScalarBoolean TableScalarKind = "BOOLEAN"
)

type GroupedPivot struct {
	ConstructionID         string                      `json:"constructionId"`
	GroupKeys              []string                    `json:"groupKeys"`
	CategoryColumn         string                      `json:"categoryColumn"`
	ValueColumn            string                      `json:"valueColumn"`
	Categories             []GroupedPivotCategory      `json:"categories"`
	DuplicatePolicy        PivotDuplicatePolicy        `json:"duplicatePolicy"`
	MissingCellPolicy      PivotMissingCellPolicy      `json:"missingCellPolicy"`
	UnlistedCategoryPolicy PivotUnlistedCategoryPolicy `json:"unlistedCategoryPolicy"`
}

type GroupedPivotCategory struct {
	Key    TableScalar `json:"key"`
	Output string      `json:"output"`
	Label  string      `json:"label"`
}

type PivotDuplicatePolicy string

const (
	PivotDuplicateError PivotDuplicatePolicy = "ERROR"
	PivotDuplicateSum   PivotDuplicatePolicy = "SUM"
	PivotDuplicateMin   PivotDuplicatePolicy = "MIN"
	PivotDuplicateMax   PivotDuplicatePolicy = "MAX"
)

type PivotMissingCellPolicy string

const (
	PivotMissingCellNull  PivotMissingCellPolicy = "NULL"
	PivotMissingCellError PivotMissingCellPolicy = "ERROR"
)

type PivotUnlistedCategoryPolicy string

const (
	PivotUnlistedCategoryError               PivotUnlistedCategoryPolicy = "ERROR"
	PivotUnlistedCategoryExcludeWithEvidence PivotUnlistedCategoryPolicy = "EXCLUDE_WITH_EVIDENCE"
)

type Unpivot struct {
	ConstructionID string               `json:"constructionId"`
	Inputs         []UnpivotInput       `json:"inputs"`
	KeyOutput      string               `json:"keyOutput"`
	KeyLabel       string               `json:"keyLabel"`
	ValueOutput    string               `json:"valueOutput"`
	ValueLabel     string               `json:"valueLabel"`
	NullRowPolicy  UnpivotNullRowPolicy `json:"nullRowPolicy"`
}

type UnpivotInput struct {
	Column string      `json:"column"`
	Key    TableScalar `json:"key"`
}

type UnpivotNullRowPolicy string

const (
	UnpivotNullDrop     UnpivotNullRowPolicy = "DROP"
	UnpivotNullPreserve UnpivotNullRowPolicy = "PRESERVE"
)

func (reshape TableReshape) Validate() error {
	if reshape.Kind == TableReshapeGroupedPivot && reshape.GroupedPivot != nil && reshape.Unpivot == nil {
		return validateGroupedPivot(*reshape.GroupedPivot)
	}
	if reshape.Kind == TableReshapeUnpivot && reshape.Unpivot != nil && reshape.GroupedPivot == nil {
		return validateUnpivot(*reshape.Unpivot)
	}
	return fmt.Errorf("table reshape must contain exactly one payload matching kind")
}

func validateGroupedPivot(pivot GroupedPivot) error {
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
		if err := category.Key.Validate(); err != nil {
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

func validateUnpivot(unpivot Unpivot) error {
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
		if err := input.Key.Validate(); err != nil {
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

func (scalar TableScalar) Validate() error {
	count := 0
	if scalar.String != nil {
		count++
	}
	if scalar.Integer != nil {
		count++
	}
	if scalar.Decimal != nil {
		count++
	}
	if scalar.Boolean != nil {
		count++
	}
	matching := scalar.Kind == TableScalarString && scalar.String != nil ||
		scalar.Kind == TableScalarInteger && scalar.Integer != nil ||
		scalar.Kind == TableScalarDecimal && scalar.Decimal != nil ||
		scalar.Kind == TableScalarBoolean && scalar.Boolean != nil
	if count != 1 || !matching {
		return fmt.Errorf("table scalar must contain exactly one payload matching kind")
	}
	if scalar.Decimal != nil && (math.IsNaN(*scalar.Decimal) || math.IsInf(*scalar.Decimal, 0)) {
		return fmt.Errorf("decimal scalar must be finite")
	}
	return nil
}

func (scalar TableScalar) identity() string {
	return fmt.Sprintf("%s:%s", scalar.Kind, scalar.valueString())
}

func (scalar TableScalar) valueString() string {
	switch scalar.Kind {
	case TableScalarString:
		return fmt.Sprintf("%q", valueOrZero(scalar.String))
	case TableScalarInteger:
		return fmt.Sprint(valueOrZero(scalar.Integer))
	case TableScalarDecimal:
		value := valueOrZero(scalar.Decimal)
		if value == 0 {
			return "0"
		}
		return fmt.Sprintf("%.17g", value)
	case TableScalarBoolean:
		return fmt.Sprint(valueOrZero(scalar.Boolean))
	default:
		return ""
	}
}

func valueOrZero[T any](value *T) T {
	if value == nil {
		var zero T
		return zero
	}
	return *value
}
