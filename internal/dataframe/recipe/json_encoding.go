// Package recipe defines the persistence-neutral recipe document used by the
// dataframe compiler. A recipe describes semantic row shaping only; it never
// carries database collection, table, AQL, or SQL details.
package recipe

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

func (b Bundle) ExpandFragments() (Bundle, error) {
	if b.Fragments == nil {
		return b, nil
	}
	expanded, err := b.Fragments.ExpandBundle(b)
	if err != nil {
		return Bundle{}, err
	}
	expanded.Fragments = nil
	return expanded, nil
}

// Explanation is a storage-independent summary of a validated recipe.
type Explanation struct {
	RecipeSchemaVersion int                 `json:"recipeSchemaVersion"`
	Name                string              `json:"name"`
	TranslationVersion  string              `json:"translationVersion"`
	Digest              string              `json:"digest"`
	Outputs             []OutputExplanation `json:"outputs"`
}

// OutputExplanation intentionally omits physical implementation details.
type OutputExplanation struct {
	Name               string   `json:"name"`
	RootResourceType   string   `json:"rootResourceType"`
	RowGrain           string   `json:"rowGrain"`
	FieldNames         []string `json:"fieldNames,omitempty"`
	TraversalNames     []string `json:"traversalNames,omitempty"`
	Expanded           bool     `json:"expanded,omitempty"`
	DynamicColumns     []string `json:"dynamicColumns,omitempty"`
	CatalogProjections []string `json:"catalogProjections,omitempty"`
}

// Expression is the generic recipe expression input AST. Exactly one of
// Select, Literal, or Call is set; Call nodes may contain Args.
type Expression struct {
	Select  string
	Literal json.RawMessage
	Call    string
	Args    []Expression
}

func (e Expression) MarshalJSON() ([]byte, error) {
	switch {
	case e.Select != "" && e.Call == "" && e.Literal == nil:
		return json.Marshal(struct {
			Select string `json:"select"`
		}{e.Select})
	case e.Call != "" && e.Select == "" && e.Literal == nil:
		if len(e.Args) == 0 {
			return json.Marshal(struct {
				Call string `json:"call"`
			}{e.Call})
		}
		return json.Marshal(struct {
			Call string       `json:"call"`
			Args []Expression `json:"args"`
		}{e.Call, e.Args})
	case e.Literal != nil && e.Select == "" && e.Call == "":
		if !json.Valid(e.Literal) {
			return nil, fmt.Errorf("literal is not valid JSON")
		}
		return append([]byte(`{"literal":`), append(e.Literal, '}')...), nil
	default:
		return nil, fmt.Errorf("expression must contain exactly one of select, literal, or call")
	}
}

func (e *Expression) UnmarshalJSON(data []byte) error {
	var object map[string]json.RawMessage
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&object); err != nil {
		return fmt.Errorf("expression must be an object: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err == nil {
		return fmt.Errorf("expression contains trailing JSON")
	}
	if len(object) == 0 {
		return fmt.Errorf("expression must contain an operator")
	}
	for key := range object {
		if key != "select" && key != "literal" && key != "call" && key != "args" {
			return fmt.Errorf("unknown expression field %q", key)
		}
	}
	_, hasSelect := object["select"]
	_, hasLiteral := object["literal"]
	_, hasCall := object["call"]
	_, hasArgs := object["args"]
	operatorCount := 0
	if hasSelect {
		operatorCount++
	}
	if hasLiteral {
		operatorCount++
	}
	if hasCall {
		operatorCount++
	}
	if operatorCount != 1 {
		return fmt.Errorf("expression must contain exactly one operator")
	}
	if hasArgs && !hasCall {
		return fmt.Errorf("args is only valid with call")
	}
	*e = Expression{}
	if hasSelect {
		if err := json.Unmarshal(object["select"], &e.Select); err != nil || strings.TrimSpace(e.Select) == "" {
			return fmt.Errorf("select must be a non-empty string")
		}
		return nil
	}
	if hasLiteral {
		if !json.Valid(object["literal"]) {
			return fmt.Errorf("literal is not valid JSON")
		}
		e.Literal = append(json.RawMessage(nil), object["literal"]...)
		return nil
	}
	if err := json.Unmarshal(object["call"], &e.Call); err != nil || strings.TrimSpace(e.Call) == "" {
		return fmt.Errorf("call must be a non-empty string")
	}
	if hasArgs {
		if err := json.Unmarshal(object["args"], &e.Args); err != nil {
			return fmt.Errorf("args must be an array: %w", err)
		}
	}
	return nil
}

// Parse strictly decodes and validates a recipe document.
func Parse(data []byte) (Bundle, error) {
	if err := detectDuplicateKeys(data); err != nil {
		return Bundle{}, err
	}
	var b Bundle
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&b); err != nil {
		return Bundle{}, validationError("parse_error", "$", err.Error())
	}
	var trailing any
	if err := dec.Decode(&trailing); err == nil {
		return Bundle{}, validationError("parse_error", "$", "multiple JSON values")
	}
	if err := b.Validate(); err != nil {
		return Bundle{}, err
	}
	return b, nil
}

// Validate checks semantic document invariants and resource-independent
// expression safety.
