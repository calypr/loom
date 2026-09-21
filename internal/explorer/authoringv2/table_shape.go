package authoringv2

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
)

const (
	maxPivotCategories    = 256
	maxDerivedDefinitions = 64
)

type ConstructionID string

type TableShape struct {
	Reshape *TableReshape         `json:"reshape,omitempty"`
	Derived []DerivedConstruction `json:"derived,omitempty"`
}

type TableReshape struct {
	Kind    string               `json:"kind"`
	Pivot   *PivotConstruction   `json:"pivot,omitempty"`
	Unpivot *UnpivotConstruction `json:"unpivot,omitempty"`
}

func (r *TableReshape) UnmarshalJSON(raw []byte) error {
	type wire struct {
		Kind    string               `json:"kind"`
		Pivot   *PivotConstruction   `json:"pivot,omitempty"`
		Unpivot *UnpivotConstruction `json:"unpivot,omitempty"`
	}
	var value wire
	if err := strictDecode(raw, &value); err != nil {
		return err
	}
	matching := value.Kind == "PIVOT" && value.Pivot != nil && value.Unpivot == nil ||
		value.Kind == "UNPIVOT" && value.Unpivot != nil && value.Pivot == nil
	if !matching {
		return fmt.Errorf("reshape must contain exactly one payload matching kind")
	}
	*r = TableReshape(value)
	return nil
}

type ColumnOutput struct {
	Column string `json:"column"`
	Label  string `json:"label"`
}

type PivotConstruction struct {
	ConstructionID         ConstructionID  `json:"constructionId"`
	GroupKeys              []string        `json:"groupKeys"`
	CategoryColumn         string          `json:"categoryColumn"`
	ValueColumn            string          `json:"valueColumn"`
	Categories             []PivotCategory `json:"categories"`
	DuplicatePolicy        string          `json:"duplicatePolicy"`
	MissingCellPolicy      string          `json:"missingCellPolicy"`
	UnlistedCategoryPolicy string          `json:"unlistedCategoryPolicy"`
}

type PivotCategory struct {
	Key    TableScalar  `json:"key"`
	Output ColumnOutput `json:"output"`
}

type UnpivotConstruction struct {
	ConstructionID ConstructionID `json:"constructionId"`
	Inputs         []UnpivotInput `json:"inputs"`
	KeyOutput      ColumnOutput   `json:"keyOutput"`
	ValueOutput    ColumnOutput   `json:"valueOutput"`
	NullRowPolicy  string         `json:"nullRowPolicy"`
}

type UnpivotInput struct {
	Column string      `json:"column"`
	Key    TableScalar `json:"key"`
}

type DerivedConstruction struct {
	ConstructionID       ConstructionID    `json:"constructionId"`
	Output               ColumnOutput      `json:"output"`
	Operation            string            `json:"operation"`
	Left                 ArithmeticOperand `json:"left"`
	Right                ArithmeticOperand `json:"right"`
	MissingInputPolicy   string            `json:"missingInputPolicy"`
	DivisionByZeroPolicy string            `json:"divisionByZeroPolicy,omitempty"`
}

type ArithmeticOperand struct {
	Kind    string       `json:"kind"`
	Column  string       `json:"column,omitempty"`
	Literal *TableScalar `json:"literal,omitempty"`
}

func (o *ArithmeticOperand) UnmarshalJSON(raw []byte) error {
	type wire struct {
		Kind    string       `json:"kind"`
		Column  string       `json:"column,omitempty"`
		Literal *TableScalar `json:"literal,omitempty"`
	}
	var value wire
	if err := strictDecode(raw, &value); err != nil {
		return err
	}
	matching := value.Kind == "COLUMN" && strings.TrimSpace(value.Column) != "" && value.Literal == nil ||
		value.Kind == "LITERAL" && strings.TrimSpace(value.Column) == "" && value.Literal != nil
	if !matching {
		return fmt.Errorf("operand must contain exactly one payload matching kind")
	}
	*o = ArithmeticOperand(value)
	return nil
}

type TableScalar struct {
	Kind    string   `json:"kind"`
	String  *string  `json:"string,omitempty"`
	Integer *int64   `json:"integer,omitempty"`
	Decimal *float64 `json:"decimal,omitempty"`
	Boolean *bool    `json:"boolean,omitempty"`
}

func (s *TableScalar) UnmarshalJSON(raw []byte) error {
	var header struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(raw, &header); err != nil {
		return err
	}
	switch header.Kind {
	case "STRING":
		var value struct {
			Kind   string  `json:"kind"`
			String *string `json:"string"`
		}
		if err := strictDecode(raw, &value); err != nil {
			return err
		}
		if value.String == nil {
			return fmt.Errorf("STRING scalar requires string")
		}
		*s = TableScalar{Kind: value.Kind, String: value.String}
	case "INTEGER":
		var value struct {
			Kind    string `json:"kind"`
			Integer *int64 `json:"integer"`
		}
		if err := strictDecode(raw, &value); err != nil {
			return err
		}
		if value.Integer == nil {
			return fmt.Errorf("INTEGER scalar requires integer")
		}
		*s = TableScalar{Kind: value.Kind, Integer: value.Integer}
	case "DECIMAL":
		var value struct {
			Kind    string   `json:"kind"`
			Decimal *float64 `json:"decimal"`
		}
		if err := strictDecode(raw, &value); err != nil {
			return err
		}
		if value.Decimal == nil {
			return fmt.Errorf("DECIMAL scalar requires decimal")
		}
		*s = TableScalar{Kind: value.Kind, Decimal: value.Decimal}
	case "BOOLEAN":
		var value struct {
			Kind    string `json:"kind"`
			Boolean *bool  `json:"boolean"`
		}
		if err := strictDecode(raw, &value); err != nil {
			return err
		}
		if value.Boolean == nil {
			return fmt.Errorf("BOOLEAN scalar requires boolean")
		}
		*s = TableScalar{Kind: value.Kind, Boolean: value.Boolean}
	default:
		return fmt.Errorf("unsupported scalar kind %q", header.Kind)
	}
	return nil
}

func (s TableScalar) Validate() error {
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
	matching := s.Kind == "STRING" && s.String != nil ||
		s.Kind == "INTEGER" && s.Integer != nil ||
		s.Kind == "DECIMAL" && s.Decimal != nil ||
		s.Kind == "BOOLEAN" && s.Boolean != nil
	if payloads != 1 || !matching {
		return fmt.Errorf("scalar must contain exactly one payload matching kind")
	}
	if s.Decimal != nil && (math.IsNaN(*s.Decimal) || math.IsInf(*s.Decimal, 0)) {
		return fmt.Errorf("DECIMAL scalar must be finite")
	}
	return nil
}

func (s TableScalar) identity() string {
	raw, _ := json.Marshal(s)
	return string(raw)
}

func (s *TableShape) Validate(columns []Column) error {
	if s == nil {
		return nil
	}
	if s.Reshape == nil && len(s.Derived) == 0 {
		return fmt.Errorf("reshape or derived definitions are required")
	}
	if len(s.Derived) > maxDerivedDefinitions {
		return fmt.Errorf("derived definitions exceed maximum of %d", maxDerivedDefinitions)
	}

	known := make(map[string]bool, len(columns)+len(s.Derived))
	publicNames := make(map[string]bool, len(columns)+len(s.Derived))
	for _, column := range columns {
		known[column.Column] = true
		publicNames[column.Column] = true
	}
	constructionIDs := map[ConstructionID]bool{}
	registerConstruction := func(id ConstructionID) error {
		if strings.TrimSpace(string(id)) == "" {
			return fmt.Errorf("constructionId is required")
		}
		if constructionIDs[id] {
			return fmt.Errorf("duplicate constructionId %q", id)
		}
		constructionIDs[id] = true
		return nil
	}
	registerOutput := func(output ColumnOutput) error {
		if !physicalColumnPattern.MatchString(output.Column) {
			return fmt.Errorf("output column %q is not a valid public column name", output.Column)
		}
		if strings.TrimSpace(output.Label) == "" {
			return fmt.Errorf("output %q label is required", output.Column)
		}
		if publicNames[output.Column] {
			return fmt.Errorf("duplicate public column name %q", output.Column)
		}
		publicNames[output.Column] = true
		known[output.Column] = true
		return nil
	}

	if s.Reshape != nil {
		switch s.Reshape.Kind {
		case "PIVOT":
			if s.Reshape.Pivot == nil || s.Reshape.Unpivot != nil {
				return fmt.Errorf("reshape must contain exactly one payload matching kind")
			}
			pivot := s.Reshape.Pivot
			if err := registerConstruction(pivot.ConstructionID); err != nil {
				return err
			}
			if len(pivot.GroupKeys) == 0 {
				return fmt.Errorf("pivot groupKeys must be non-empty")
			}
			seenGroups := map[string]bool{}
			for _, column := range pivot.GroupKeys {
				if !known[column] {
					return fmt.Errorf("unknown group key column %q", column)
				}
				if seenGroups[column] {
					return fmt.Errorf("groupKeys contains duplicate column %q", column)
				}
				seenGroups[column] = true
			}
			if !known[pivot.CategoryColumn] {
				return fmt.Errorf("unknown category column %q", pivot.CategoryColumn)
			}
			if !known[pivot.ValueColumn] {
				return fmt.Errorf("unknown value column %q", pivot.ValueColumn)
			}
			if len(pivot.Categories) == 0 {
				return fmt.Errorf("pivot categories must be non-empty")
			}
			if len(pivot.Categories) > maxPivotCategories {
				return fmt.Errorf("pivot categories exceed maximum of %d", maxPivotCategories)
			}
			seenKeys := map[string]bool{}
			for index, category := range pivot.Categories {
				if err := category.Key.Validate(); err != nil {
					return fmt.Errorf("categories[%d].key: %w", index, err)
				}
				key := category.Key.identity()
				if seenKeys[key] {
					return fmt.Errorf("duplicate pivot category key at categories[%d]", index)
				}
				seenKeys[key] = true
				if err := registerOutput(category.Output); err != nil {
					return err
				}
			}
			if !oneOf(pivot.DuplicatePolicy, "ERROR", "SUM", "MIN", "MAX") {
				return fmt.Errorf("duplicatePolicy must be ERROR, SUM, MIN, or MAX")
			}
			if !oneOf(pivot.MissingCellPolicy, "NULL", "ERROR") {
				return fmt.Errorf("missingCellPolicy must be NULL or ERROR")
			}
			if !oneOf(pivot.UnlistedCategoryPolicy, "ERROR", "EXCLUDE_WITH_EVIDENCE") {
				return fmt.Errorf("unlistedCategoryPolicy must be ERROR or EXCLUDE_WITH_EVIDENCE")
			}
		case "UNPIVOT":
			if s.Reshape.Unpivot == nil || s.Reshape.Pivot != nil {
				return fmt.Errorf("reshape must contain exactly one payload matching kind")
			}
			if len(s.Derived) != 0 {
				return fmt.Errorf("cannot combine UNPIVOT with derived definitions")
			}
			unpivot := s.Reshape.Unpivot
			if err := registerConstruction(unpivot.ConstructionID); err != nil {
				return err
			}
			if len(unpivot.Inputs) == 0 {
				return fmt.Errorf("unpivot inputs must be non-empty")
			}
			seenColumns, seenKeys := map[string]bool{}, map[string]bool{}
			for index, input := range unpivot.Inputs {
				if !known[input.Column] {
					return fmt.Errorf("unknown unpivot input column %q", input.Column)
				}
				if seenColumns[input.Column] {
					return fmt.Errorf("inputs contains duplicate column %q", input.Column)
				}
				seenColumns[input.Column] = true
				if err := input.Key.Validate(); err != nil {
					return fmt.Errorf("inputs[%d].key: %w", index, err)
				}
				key := input.Key.identity()
				if seenKeys[key] {
					return fmt.Errorf("duplicate unpivot input key at inputs[%d]", index)
				}
				seenKeys[key] = true
			}
			if err := registerOutput(unpivot.KeyOutput); err != nil {
				return err
			}
			if err := registerOutput(unpivot.ValueOutput); err != nil {
				return err
			}
			if !oneOf(unpivot.NullRowPolicy, "DROP", "PRESERVE") {
				return fmt.Errorf("nullRowPolicy must be DROP or PRESERVE")
			}
		default:
			return fmt.Errorf("unsupported reshape kind %q", s.Reshape.Kind)
		}
	}

	derivedByOutput := make(map[string]DerivedConstruction, len(s.Derived))
	for _, derived := range s.Derived {
		if err := registerConstruction(derived.ConstructionID); err != nil {
			return err
		}
		if err := registerOutput(derived.Output); err != nil {
			return err
		}
		derivedByOutput[derived.Output.Column] = derived
	}
	for _, derived := range s.Derived {
		if !oneOf(derived.Operation, "ADD", "SUBTRACT", "MULTIPLY", "DIVIDE") {
			return fmt.Errorf("derived operation %q is unsupported", derived.Operation)
		}
		if !oneOf(derived.MissingInputPolicy, "PROPAGATE_NULL", "ERROR") {
			return fmt.Errorf("missingInputPolicy must be PROPAGATE_NULL or ERROR")
		}
		if derived.Operation == "DIVIDE" && !oneOf(derived.DivisionByZeroPolicy, "NULL", "ERROR") {
			return fmt.Errorf("DIVIDE requires divisionByZeroPolicy")
		}
		if derived.Operation != "DIVIDE" && derived.DivisionByZeroPolicy != "" {
			return fmt.Errorf("divisionByZeroPolicy is only valid for DIVIDE")
		}
		if err := derived.Left.Validate(known); err != nil {
			return fmt.Errorf("derived %q left: %w", derived.Output.Column, err)
		}
		if err := derived.Right.Validate(known); err != nil {
			return fmt.Errorf("derived %q right: %w", derived.Output.Column, err)
		}
	}
	if err := validateDerivedAcyclic(derivedByOutput); err != nil {
		return err
	}
	return nil
}

func (o ArithmeticOperand) Validate(known map[string]bool) error {
	switch o.Kind {
	case "COLUMN":
		if strings.TrimSpace(o.Column) == "" || o.Literal != nil {
			return fmt.Errorf("operand must contain exactly one payload matching kind")
		}
		if !known[o.Column] {
			return fmt.Errorf("unknown column reference %q", o.Column)
		}
	case "LITERAL":
		if o.Column != "" || o.Literal == nil {
			return fmt.Errorf("operand must contain exactly one payload matching kind")
		}
		if err := o.Literal.Validate(); err != nil {
			return err
		}
	default:
		return fmt.Errorf("operand must contain exactly one payload matching kind")
	}
	return nil
}

func validateDerivedAcyclic(derived map[string]DerivedConstruction) error {
	states := map[string]uint8{}
	var visit func(string) error
	visit = func(column string) error {
		switch states[column] {
		case 1:
			return fmt.Errorf("derived dependency cycle includes %q", column)
		case 2:
			return nil
		}
		states[column] = 1
		definition := derived[column]
		for _, operand := range []ArithmeticOperand{definition.Left, definition.Right} {
			if operand.Kind == "COLUMN" {
				if _, ok := derived[operand.Column]; ok {
					if err := visit(operand.Column); err != nil {
						return err
					}
				}
			}
		}
		states[column] = 2
		return nil
	}
	for column := range derived {
		if err := visit(column); err != nil {
			return err
		}
	}
	return nil
}

func oneOf(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}

func workspaceHasTableShape(workspace Workspace) bool {
	for _, document := range workspace.Documents {
		if document.TableShape != nil {
			return true
		}
	}
	return false
}

func cloneTableShape(shape *TableShape) (*TableShape, error) {
	if shape == nil {
		return nil, nil
	}
	raw, err := json.Marshal(shape)
	if err != nil {
		return nil, fmt.Errorf("clone table shape: %w", err)
	}
	var cloned TableShape
	if err := json.Unmarshal(raw, &cloned); err != nil {
		return nil, fmt.Errorf("clone table shape: %w", err)
	}
	return &cloned, nil
}
