package authoringv2

import (
	"fmt"
	"strings"
)

const maxStoredTableShapeDerivedDefinitions = 64

// storedTableShape and its component types are private wire representations for
// one-time persisted-data conversion. They are not part of the current authoring
// or request model.
type storedTableShape struct {
	Reshape *storedTableReshape         `json:"reshape,omitempty"`
	Derived []storedDerivedConstruction `json:"derived,omitempty"`
}

type storedTableReshape struct {
	Kind    string                     `json:"kind"`
	Pivot   *storedPivotConstruction   `json:"pivot,omitempty"`
	Unpivot *storedUnpivotConstruction `json:"unpivot,omitempty"`
}

func (r *storedTableReshape) UnmarshalJSON(raw []byte) error {
	type wire struct {
		Kind    string                     `json:"kind"`
		Pivot   *storedPivotConstruction   `json:"pivot,omitempty"`
		Unpivot *storedUnpivotConstruction `json:"unpivot,omitempty"`
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
	*r = storedTableReshape(value)
	return nil
}

type storedPivotConstruction struct {
	ConstructionID         ConstructionID        `json:"constructionId"`
	GroupKeys              []string              `json:"groupKeys"`
	CategoryColumn         string                `json:"categoryColumn"`
	ValueColumn            string                `json:"valueColumn"`
	Categories             []storedPivotCategory `json:"categories"`
	DuplicatePolicy        string                `json:"duplicatePolicy"`
	MissingCellPolicy      string                `json:"missingCellPolicy"`
	UnlistedCategoryPolicy string                `json:"unlistedCategoryPolicy"`
}

type storedPivotCategory struct {
	Key    TableScalar  `json:"key"`
	Output ColumnOutput `json:"output"`
}

type storedUnpivotConstruction struct {
	ConstructionID ConstructionID       `json:"constructionId"`
	Inputs         []storedUnpivotInput `json:"inputs"`
	KeyOutput      ColumnOutput         `json:"keyOutput"`
	ValueOutput    ColumnOutput         `json:"valueOutput"`
	NullRowPolicy  string               `json:"nullRowPolicy"`
}

type storedUnpivotInput struct {
	Column string      `json:"column"`
	Key    TableScalar `json:"key"`
}

type storedDerivedConstruction struct {
	ConstructionID       ConstructionID          `json:"constructionId"`
	Output               ColumnOutput            `json:"output"`
	Operation            string                  `json:"operation"`
	Left                 storedArithmeticOperand `json:"left"`
	Right                storedArithmeticOperand `json:"right"`
	MissingInputPolicy   string                  `json:"missingInputPolicy"`
	DivisionByZeroPolicy string                  `json:"divisionByZeroPolicy,omitempty"`
}

type storedArithmeticOperand struct {
	Kind    string       `json:"kind"`
	Column  string       `json:"column,omitempty"`
	Literal *TableScalar `json:"literal,omitempty"`
}

func (o *storedArithmeticOperand) UnmarshalJSON(raw []byte) error {
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
	*o = storedArithmeticOperand(value)
	return nil
}

func (s *storedTableShape) Validate(columns []Column) error {
	if s == nil {
		return nil
	}
	if s.Reshape == nil && len(s.Derived) == 0 {
		return fmt.Errorf("reshape or derived definitions are required")
	}
	if len(s.Derived) > maxStoredTableShapeDerivedDefinitions {
		return fmt.Errorf("derived definitions exceed maximum of %d", maxStoredTableShapeDerivedDefinitions)
	}

	known := make(map[string]bool, len(columns)+len(s.Derived))
	columnByName := make(map[string]Column, len(columns))
	publicNames := make(map[string]bool, len(columns)+len(s.Derived))
	for _, column := range columns {
		known[column.Column] = true
		columnByName[column.Column] = column
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
				if scalar, known := constructionPivotTypeIsScalar(columnByName[column].LogicalType); known && !scalar {
					return fmt.Errorf("pivot group key %q type %q is not a scalar", column, columnByName[column].LogicalType)
				}
				seenGroups[column] = true
			}
			if !known[pivot.CategoryColumn] {
				return fmt.Errorf("unknown category column %q", pivot.CategoryColumn)
			}
			if !known[pivot.ValueColumn] {
				return fmt.Errorf("unknown value column %q", pivot.ValueColumn)
			}
			for _, reference := range []struct{ field, column string }{
				{field: "categoryColumn", column: pivot.CategoryColumn},
				{field: "valueColumn", column: pivot.ValueColumn},
			} {
				if scalar, known := constructionPivotTypeIsScalar(columnByName[reference.column].LogicalType); known && !scalar {
					return fmt.Errorf("pivot %s column %q type %q is not a scalar", reference.field, reference.column, columnByName[reference.column].LogicalType)
				}
			}
			for _, column := range pivot.GroupKeys {
				if column == pivot.CategoryColumn {
					return fmt.Errorf("pivot categoryColumn %q cannot also be a group key", column)
				}
				if column == pivot.ValueColumn {
					return fmt.Errorf("pivot valueColumn %q cannot also be a group key", column)
				}
			}
			if pivot.CategoryColumn == pivot.ValueColumn {
				return fmt.Errorf("pivot category and value columns must differ")
			}
			categoryKind, categoryKindKnown := constructionPivotScalarKind(columnByName[pivot.CategoryColumn].LogicalType)
			if pivot.DuplicatePolicy == "SUM" {
				valueColumn := columnByName[pivot.ValueColumn]
				if valueKind, known := constructionPivotScalarKind(valueColumn.LogicalType); known && valueKind != "INTEGER" && valueKind != "DECIMAL" {
					return fmt.Errorf("pivot duplicatePolicy SUM requires an INTEGER or DECIMAL value column, got %q", valueColumn.LogicalType)
				}
			}
			if len(pivot.Categories) == 0 {
				return fmt.Errorf("pivot categories must be non-empty")
			}
			if len(pivot.Categories) > maxPivotCategories {
				return fmt.Errorf("pivot categories exceed maximum of %d", maxPivotCategories)
			}
			seenKeys := map[string]bool{}
			for index, category := range pivot.Categories {
				if err := category.Key.ValidatePivotCategoryKey(); err != nil {
					return fmt.Errorf("categories[%d].key: %w", index, err)
				}
				if categoryKindKnown && category.Key.Kind != TableScalarNull && category.Key.Kind != TableScalarMissing &&
					string(category.Key.Kind) != categoryKind {
					return fmt.Errorf("pivot categories[%d].key type %q does not match category column type %q", index, category.Key.Kind, columnByName[pivot.CategoryColumn].LogicalType)
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
			if !oneOf(pivot.UnlistedCategoryPolicy, "ERROR") {
				return fmt.Errorf("unlistedCategoryPolicy must be ERROR; EXCLUDE_WITH_EVIDENCE cannot be upgraded to a construction sequence")
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
				if err := input.Key.ValidateConcreteValue(); err != nil {
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

	derivedByOutput := make(map[string]storedDerivedConstruction, len(s.Derived))
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
	if err := validateStoredDerivedAcyclic(derivedByOutput); err != nil {
		return err
	}
	return nil
}

func (o storedArithmeticOperand) Validate(known map[string]bool) error {
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
		if err := o.Literal.ValidateConcreteValue(); err != nil {
			return err
		}
	default:
		return fmt.Errorf("operand must contain exactly one payload matching kind")
	}
	return nil
}

func validateStoredDerivedAcyclic(derived map[string]storedDerivedConstruction) error {
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
		for _, operand := range []storedArithmeticOperand{definition.Left, definition.Right} {
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
