package recipe

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

type RootColumnNaming string

const (
	RootColumnNamingPrefixed RootColumnNaming = "PREFIXED"
	RootColumnNamingExact    RootColumnNaming = "EXACT"
)

func (n RootColumnNaming) Valid() bool {
	return n == "" || n == RootColumnNamingPrefixed || n == RootColumnNamingExact
}

func (n RootColumnNaming) Normalized() RootColumnNaming {
	if n == "" {
		return RootColumnNamingPrefixed
	}
	return n
}

type TraversalColumnNaming string

const (
	TraversalColumnNamingPath  TraversalColumnNaming = "PATH"
	TraversalColumnNamingAlias TraversalColumnNaming = "ALIAS"
	TraversalColumnNamingExact TraversalColumnNaming = "EXACT"
)

func (n TraversalColumnNaming) Valid() bool {
	return n == "" || n == TraversalColumnNamingPath || n == TraversalColumnNamingAlias || n == TraversalColumnNamingExact
}

func (n TraversalColumnNaming) Normalized() TraversalColumnNaming {
	if n == "" {
		return TraversalColumnNamingPath
	}
	return n
}

type Field struct {
	Name string `json:"name"`

	ColumnID   string       `json:"columnId,omitempty"`
	Label      string       `json:"label,omitempty"`
	FieldRef   string       `json:"fieldRef,omitempty"`
	Expr       Expression   `json:"expr"`
	Fallbacks  []Expression `json:"fallbacks,omitempty"`
	ValueMode  ValueMode    `json:"valueMode,omitempty"`
	Discovered bool         `json:"-"`

	SourceKey string `json:"-"`
}

type ValueMode string

const (
	ValueModeAuto     ValueMode = "AUTO"
	ValueModeFirst    ValueMode = "FIRST"
	ValueModeAll      ValueMode = "ALL"
	ValueModeDistinct ValueMode = "DISTINCT"
)

func (m ValueMode) Valid() bool {
	return m == "" || m == ValueModeAuto || m == ValueModeFirst || m == ValueModeAll || m == ValueModeDistinct
}

func (m ValueMode) Normalized() ValueMode {
	if m == "" {
		return ValueModeAuto
	}
	return m
}

func (f *Field) UnmarshalJSON(data []byte) error {
	type fieldJSON struct {
		Name      string       `json:"name"`
		ColumnID  string       `json:"columnId,omitempty"`
		Label     string       `json:"label,omitempty"`
		FieldRef  string       `json:"fieldRef,omitempty"`
		Expr      Expression   `json:"expr"`
		Fallbacks []Expression `json:"fallbacks,omitempty"`
		ValueMode ValueMode    `json:"valueMode,omitempty"`

		LogicalType   string `json:"logicalType,omitempty"`
		Repeated      bool   `json:"repeated,omitempty"`
		Family        string `json:"family,omitempty"`
		SelectionKey  string `json:"selectionKey,omitempty"`
		ValueSelector string `json:"valueSelector,omitempty"`
		FamilyName    string `json:"familyName,omitempty"`
		FamilyKind    string `json:"familyKind,omitempty"`
	}
	var value fieldJSON
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("field contains trailing JSON")
		}
		return err
	}
	*f = Field{
		Name: value.Name, ColumnID: value.ColumnID, Label: value.Label,
		FieldRef: value.FieldRef, Expr: value.Expr,
		Fallbacks: value.Fallbacks, ValueMode: value.ValueMode,
	}
	return nil
}
