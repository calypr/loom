package recipe

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

type archivedTableScalar struct {
	Kind    TableScalarKind `json:"kind"`
	String  *string         `json:"string,omitempty"`
	Integer *int64          `json:"integer,omitempty"`
	Decimal *float64        `json:"decimal,omitempty"`
	Boolean *bool           `json:"boolean,omitempty"`
}

func (scalar *archivedTableScalar) UnmarshalJSON(raw []byte) error {
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

	parsed := archivedTableScalar{Kind: value.Kind}
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

func (scalar archivedTableScalar) ValidateStructure() error {
	return TableScalar(scalar).ValidateStructure()
}

func (scalar archivedTableScalar) ValidateConcreteValue() error {
	if err := scalar.ValidateStructure(); err != nil {
		return err
	}
	if scalar.Kind == TableScalarNull || scalar.Kind == TableScalarMissing {
		return fmt.Errorf("sentinel scalar is not a concrete value")
	}
	return nil
}

func (scalar archivedTableScalar) ValidatePivotCategoryKey() error {
	return scalar.ValidateStructure()
}

func (scalar archivedTableScalar) identity() string {
	return TableScalar(scalar).identity()
}
