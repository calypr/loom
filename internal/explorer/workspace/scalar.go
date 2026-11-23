package workspace

import (
	"encoding/json"
	"fmt"
	"math"
)

const maxPivotCategories = 256

type ConstructionID string

type ColumnOutput struct {
	Column string `json:"column"`
	Label  string `json:"label"`
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

type TableScalar struct {
	Kind    TableScalarKind `json:"kind"`
	String  *string         `json:"string,omitempty"`
	Integer *int64          `json:"integer,omitempty"`
	Decimal *float64        `json:"decimal,omitempty"`
	Boolean *bool           `json:"boolean,omitempty"`
}

func (s *TableScalar) UnmarshalJSON(raw []byte) error {
	var value struct {
		Kind    TableScalarKind `json:"kind"`
		String  json.RawMessage `json:"string,omitempty"`
		Integer json.RawMessage `json:"integer,omitempty"`
		Decimal json.RawMessage `json:"decimal,omitempty"`
		Boolean json.RawMessage `json:"boolean,omitempty"`
	}
	if err := strictDecode(raw, &value); err != nil {
		return err
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
	scalar := TableScalar{Kind: value.Kind}
	switch value.Kind {
	case TableScalarNull, TableScalarMissing:
		if payloads != 0 {
			return fmt.Errorf("%s scalar does not accept a value payload", value.Kind)
		}
	case TableScalarString:
		if len(value.String) == 0 {
			return fmt.Errorf("STRING scalar must contain exactly one string payload")
		}
		if err := json.Unmarshal(value.String, &scalar.String); err != nil {
			return err
		}
	case TableScalarInteger:
		if len(value.Integer) == 0 {
			return fmt.Errorf("INTEGER scalar must contain exactly one integer payload")
		}
		if err := json.Unmarshal(value.Integer, &scalar.Integer); err != nil {
			return err
		}
	case TableScalarDecimal:
		if len(value.Decimal) == 0 {
			return fmt.Errorf("DECIMAL scalar must contain exactly one decimal payload")
		}
		if err := json.Unmarshal(value.Decimal, &scalar.Decimal); err != nil {
			return err
		}
	case TableScalarBoolean:
		if len(value.Boolean) == 0 {
			return fmt.Errorf("BOOLEAN scalar must contain exactly one boolean payload")
		}
		if err := json.Unmarshal(value.Boolean, &scalar.Boolean); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported scalar kind %q", value.Kind)
	}
	if err := scalar.ValidateStructure(); err != nil {
		return err
	}
	*s = scalar
	return nil
}

func (s TableScalar) ValidateStructure() error {
	payloads := 0
	if s.String != nil {
		payloads++
	}
	if s.Integer != nil {
		payloads++
	}
	if s.Decimal != nil {
		payloads++
	}
	if s.Boolean != nil {
		payloads++
	}
	switch s.Kind {
	case TableScalarNull, TableScalarMissing:
		if payloads != 0 {
			return fmt.Errorf("%s scalar does not accept a value payload", s.Kind)
		}
	case TableScalarString:
		if payloads != 1 || s.String == nil {
			return fmt.Errorf("STRING scalar must contain exactly one string payload")
		}
	case TableScalarInteger:
		if payloads != 1 || s.Integer == nil {
			return fmt.Errorf("INTEGER scalar must contain exactly one integer payload")
		}
	case TableScalarDecimal:
		if payloads != 1 || s.Decimal == nil {
			return fmt.Errorf("DECIMAL scalar must contain exactly one decimal payload")
		}
	case TableScalarBoolean:
		if payloads != 1 || s.Boolean == nil {
			return fmt.Errorf("BOOLEAN scalar must contain exactly one boolean payload")
		}
	default:
		return fmt.Errorf("unsupported scalar kind %q", s.Kind)
	}
	if s.Decimal != nil && (math.IsNaN(*s.Decimal) || math.IsInf(*s.Decimal, 0)) {
		return fmt.Errorf("DECIMAL scalar must be finite")
	}
	return nil
}

func (s TableScalar) ValidateConcreteValue() error {
	if err := s.ValidateStructure(); err != nil {
		return err
	}
	if s.Kind == TableScalarNull || s.Kind == TableScalarMissing {
		return fmt.Errorf("sentinel scalar is not a concrete value")
	}
	return nil
}

func (s TableScalar) ValidatePivotCategoryKey() error {
	return s.ValidateStructure()
}

func (s TableScalar) identity() string {
	canonical := s
	if s.Kind == TableScalarDecimal && s.Decimal != nil && *s.Decimal == 0 {
		zero := 0.0
		canonical.Decimal = &zero
	}
	raw, _ := json.Marshal(canonical)
	return string(raw)
}

func oneOf(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}
