package recipe

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
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
	TableScalarNull    TableScalarKind = "NULL"
	TableScalarMissing TableScalarKind = "MISSING"
)

func (scalar *TableScalar) UnmarshalJSON(raw []byte) error {
	var value struct {
		Kind    TableScalarKind `json:"kind"`
		String  json.RawMessage `json:"string,omitempty"`
		Integer json.RawMessage `json:"integer,omitempty"`
		Decimal json.RawMessage `json:"decimal,omitempty"`
		Boolean json.RawMessage `json:"boolean,omitempty"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("multiple JSON values")
		}
		return fmt.Errorf("invalid trailing JSON: %w", err)
	}

	payloads := 0
	for _, payload := range []json.RawMessage{value.String, value.Integer, value.Decimal, value.Boolean} {
		if len(payload) != 0 {
			payloads++
		}
	}
	if value.Kind != TableScalarNull && value.Kind != TableScalarMissing && payloads > 1 {
		return fmt.Errorf("%s scalar cannot contain multiple value payloads", value.Kind)
	}

	parsed := TableScalar{Kind: value.Kind}
	switch value.Kind {
	case TableScalarNull, TableScalarMissing:
		if payloads != 0 {
			return fmt.Errorf("%s scalar does not accept a value payload", value.Kind)
		}
	case TableScalarString:
		if len(value.String) == 0 {
			return fmt.Errorf("STRING scalar must contain exactly one string payload")
		}
		if err := json.Unmarshal(value.String, &parsed.String); err != nil {
			return err
		}
	case TableScalarInteger:
		if len(value.Integer) == 0 {
			return fmt.Errorf("INTEGER scalar must contain exactly one integer payload")
		}
		if err := json.Unmarshal(value.Integer, &parsed.Integer); err != nil {
			return err
		}
	case TableScalarDecimal:
		if len(value.Decimal) == 0 {
			return fmt.Errorf("DECIMAL scalar must contain exactly one decimal payload")
		}
		if err := json.Unmarshal(value.Decimal, &parsed.Decimal); err != nil {
			return err
		}
	case TableScalarBoolean:
		if len(value.Boolean) == 0 {
			return fmt.Errorf("BOOLEAN scalar must contain exactly one boolean payload")
		}
		if err := json.Unmarshal(value.Boolean, &parsed.Boolean); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported table scalar kind %q", value.Kind)
	}
	if err := parsed.ValidateStructure(); err != nil {
		return err
	}
	*scalar = parsed
	return nil
}

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

func (scalar TableScalar) ValidateStructure() error {
	payloads := 0
	if scalar.String != nil {
		payloads++
	}
	if scalar.Integer != nil {
		payloads++
	}
	if scalar.Decimal != nil {
		payloads++
	}
	if scalar.Boolean != nil {
		payloads++
	}
	switch scalar.Kind {
	case TableScalarNull, TableScalarMissing:
		if payloads != 0 {
			return fmt.Errorf("%s scalar does not accept a value payload", scalar.Kind)
		}
	case TableScalarString:
		if payloads != 1 || scalar.String == nil {
			return fmt.Errorf("STRING scalar must contain exactly one string payload")
		}
	case TableScalarInteger:
		if payloads != 1 || scalar.Integer == nil {
			return fmt.Errorf("INTEGER scalar must contain exactly one integer payload")
		}
	case TableScalarDecimal:
		if payloads != 1 || scalar.Decimal == nil {
			return fmt.Errorf("DECIMAL scalar must contain exactly one decimal payload")
		}
	case TableScalarBoolean:
		if payloads != 1 || scalar.Boolean == nil {
			return fmt.Errorf("BOOLEAN scalar must contain exactly one boolean payload")
		}
	default:
		return fmt.Errorf("unsupported table scalar kind %q", scalar.Kind)
	}
	if scalar.Decimal != nil && (math.IsNaN(*scalar.Decimal) || math.IsInf(*scalar.Decimal, 0)) {
		return fmt.Errorf("decimal scalar must be finite")
	}
	return nil
}

func (scalar TableScalar) ValidateConcreteValue() error {
	if err := scalar.ValidateStructure(); err != nil {
		return err
	}
	if scalar.Kind == TableScalarNull || scalar.Kind == TableScalarMissing {
		return fmt.Errorf("sentinel scalar is not a concrete value")
	}
	return nil
}

func (scalar TableScalar) ValidatePivotCategoryKey() error {
	return scalar.ValidateStructure()
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
	case TableScalarNull, TableScalarMissing:
		return ""
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
