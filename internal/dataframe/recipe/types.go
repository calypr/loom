// Package recipe defines the persistence-neutral recipe document used by the
// dataframe compiler. A recipe describes semantic row shaping only; it never
// carries database collection, table, AQL, or SQL details.
package recipe

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// CurrentSchemaVersion is the first stable recipe document schema.
const CurrentSchemaVersion = 1

const (
	maxExpressionDepth = 64
	maxExpressionNodes = 4096
	maxLiteralArray    = 256
)

// Bundle is an immutable, versioned collection of dataframe outputs.
type Bundle struct {
	RecipeSchemaVersion int              `json:"recipeSchemaVersion"`
	Name                string           `json:"name"`
	TranslationVersion  string           `json:"translationVersion"`
	Fragments           *FragmentLibrary `json:"fragments,omitempty"`
	Outputs             []Output         `json:"outputs"`
}

// Output describes one row-shaped result. Names are semantic names, not
// storage identifiers.
type Output struct {
	Name             string          `json:"name"`
	RootResourceType string          `json:"rootResourceType"`
	RowGrain         string          `json:"rowGrain"`
	Fields           []Field         `json:"fields,omitempty"`
	Traversals       []Traversal     `json:"traversals,omitempty"`
	Expand           *Expansion      `json:"expand,omitempty"`
	Identity         *Identity       `json:"identity,omitempty"`
	DynamicColumns   []DynamicColumn `json:"dynamicColumns,omitempty"`
	CollisionPolicy  string          `json:"collisionPolicy,omitempty"`
}

// Field projects one named semantic value into an output row.
type Field struct {
	Name string     `json:"name"`
	Expr Expression `json:"expr"`
}

// Traversal describes a relationship traversal without naming a physical
// graph collection or edge table.
type Traversal struct {
	Name           string      `json:"name"`
	ToResourceType string      `json:"toResourceType"`
	Alias          string      `json:"alias,omitempty"`
	From           *Expression `json:"from,omitempty"`
	MatchMode      string      `json:"matchMode,omitempty"`
	Fields         []Field     `json:"fields,omitempty"`
	Traversals     []Traversal `json:"traversals,omitempty"`
}

// Expansion turns a repeated expression into one row per element.
type Expansion struct {
	From Expression `json:"from"`
	As   string     `json:"as"`
}

// Identity derives a deterministic row identity.
type Identity struct {
	Name string     `json:"name"`
	Expr Expression `json:"expr"`
}

// DynamicColumn discovers a bounded set of key/value columns. The compiler
// freezes discovered keys before materialization.
type DynamicColumn struct {
	Name       string      `json:"name"`
	Source     Expression  `json:"source"`
	Key        *Expression `json:"key,omitempty"`
	Value      *Expression `json:"value,omitempty"`
	Columns    []string    `json:"columns,omitempty"`
	MaxColumns int         `json:"maxColumns,omitempty"`
}

// RuntimeBindings are request-scoped and deliberately not part of a stored
// recipe digest.
type RuntimeBindings struct {
	Project           string
	DatasetGeneration string
	AuthResourcePaths []string
	PreviewLimit      int
}

// ExpandFragments resolves the optional declarative fragment library and
// returns a standalone bundle suitable for semantic compilation. The stored
// library is omitted from the returned document because the expanded recipe
// is the immutable compiler input and its digest is what runtime plans carry.
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
	Name             string   `json:"name"`
	RootResourceType string   `json:"rootResourceType"`
	RowGrain         string   `json:"rowGrain"`
	FieldNames       []string `json:"fieldNames,omitempty"`
	TraversalNames   []string `json:"traversalNames,omitempty"`
	Expanded         bool     `json:"expanded,omitempty"`
	DynamicColumns   []string `json:"dynamicColumns,omitempty"`
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
func (b Bundle) Validate() error {
	if b.RecipeSchemaVersion != CurrentSchemaVersion {
		return validationError("unsupported_schema_version", "$.recipeSchemaVersion", fmt.Sprintf("must be %d", CurrentSchemaVersion))
	}
	if strings.TrimSpace(b.Name) == "" {
		return validationError("required", "$.name", "name is required")
	}
	if strings.TrimSpace(b.TranslationVersion) == "" {
		return validationError("required", "$.translationVersion", "translationVersion is required")
	}
	if len(b.Outputs) == 0 {
		return validationError("required", "$.outputs", "at least one output is required")
	}
	if b.Fragments != nil {
		if err := b.Fragments.Validate(); err != nil {
			return validationError("invalid_fragments", "$.fragments", err.Error())
		}
	}
	seen := map[string]bool{}
	for i, output := range b.Outputs {
		path := fmt.Sprintf("$.outputs[%d]", i)
		if strings.TrimSpace(output.Name) == "" {
			return validationError("required", path+".name", "name is required")
		}
		if seen[output.Name] {
			return validationError("duplicate_name", path+".name", "duplicate output name")
		}
		seen[output.Name] = true
		if strings.TrimSpace(output.RootResourceType) == "" {
			return validationError("required", path+".rootResourceType", "rootResourceType is required")
		}
		if strings.TrimSpace(output.RowGrain) == "" {
			return validationError("required", path+".rowGrain", "rowGrain is required")
		}
		if output.CollisionPolicy != "" && output.CollisionPolicy != "error" && output.CollisionPolicy != "overwrite" && output.CollisionPolicy != "coalesce" {
			return validationError("invalid_collision_policy", path+".collisionPolicy", "must be error, overwrite, or coalesce")
		}
		if err := validateFields(output.Fields, path+".fields"); err != nil {
			return err
		}
		dynamicNames := map[string]bool{}
		if err := validateTraversals(output.Traversals, path+".traversals", 0); err != nil {
			return err
		}
		if output.Expand != nil {
			if strings.TrimSpace(output.Expand.As) == "" {
				return validationError("required", path+".expand.as", "as is required")
			}
			if err := validateExpression(output.Expand.From, path+".expand.from"); err != nil {
				return err
			}
		}
		if output.Identity != nil {
			if strings.TrimSpace(output.Identity.Name) == "" {
				return validationError("required", path+".identity.name", "name is required")
			}
			if err := validateExpression(output.Identity.Expr, path+".identity.expr"); err != nil {
				return err
			}
		}
		for j, dynamic := range output.DynamicColumns {
			dp := fmt.Sprintf("%s.dynamicColumns[%d]", path, j)
			if strings.TrimSpace(dynamic.Name) == "" {
				return validationError("required", dp+".name", "name is required")
			}
			if dynamicNames[dynamic.Name] {
				return validationError("duplicate_name", dp+".name", "duplicate dynamic column name")
			}
			dynamicNames[dynamic.Name] = true
			if dynamic.MaxColumns < 0 {
				return validationError("invalid_limit", dp+".maxColumns", "must not be negative")
			}
			seenColumns := map[string]bool{}
			for k, column := range dynamic.Columns {
				if strings.TrimSpace(column) == "" {
					return validationError("required", fmt.Sprintf("%s.columns[%d]", dp, k), "column name is required")
				}
				if seenColumns[column] {
					return validationError("duplicate_name", fmt.Sprintf("%s.columns[%d]", dp, k), "duplicate dynamic column")
				}
				seenColumns[column] = true
			}
			if err := validateExpression(dynamic.Source, dp+".source"); err != nil {
				return err
			}
			if dynamic.Key != nil {
				if err := validateExpression(*dynamic.Key, dp+".key"); err != nil {
					return err
				}
			}
			if dynamic.Value != nil {
				if err := validateExpression(*dynamic.Value, dp+".value"); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func validateFields(fields []Field, path string) error {
	seen := map[string]bool{}
	for i, f := range fields {
		p := fmt.Sprintf("%s[%d]", path, i)
		if strings.TrimSpace(f.Name) == "" {
			return validationError("required", p+".name", "name is required")
		}
		if seen[f.Name] {
			return validationError("duplicate_name", p+".name", "duplicate field name")
		}
		seen[f.Name] = true
		if err := validateExpression(f.Expr, p+".expr"); err != nil {
			return err
		}
	}
	return nil
}

func validateTraversals(items []Traversal, path string, depth int) error {
	if depth > maxExpressionDepth {
		return validationError("max_depth", path, "traversal depth exceeds limit")
	}
	seen := map[string]bool{}
	for i, t := range items {
		p := fmt.Sprintf("%s[%d]", path, i)
		if strings.TrimSpace(t.Name) == "" {
			return validationError("required", p+".name", "name is required")
		}
		if seen[t.Name] {
			return validationError("duplicate_name", p+".name", "duplicate traversal name")
		}
		seen[t.Name] = true
		if strings.TrimSpace(t.ToResourceType) == "" {
			return validationError("required", p+".toResourceType", "toResourceType is required")
		}
		if t.MatchMode != "" && t.MatchMode != "optional" && t.MatchMode != "required" {
			return validationError("invalid_match_mode", p+".matchMode", "must be optional or required")
		}
		if t.From != nil {
			if err := validateExpression(*t.From, p+".from"); err != nil {
				return err
			}
		}
		if err := validateFields(t.Fields, p+".fields"); err != nil {
			return err
		}
		if err := validateTraversals(t.Traversals, p+".traversals", depth+1); err != nil {
			return err
		}
	}
	return nil
}

func validateExpression(e Expression, path string) error {
	nodes := 0
	var walk func(Expression, string, int) error
	walk = func(node Expression, p string, depth int) error {
		nodes++
		if nodes > maxExpressionNodes {
			return validationError("max_nodes", p, "expression node count exceeds limit")
		}
		if depth > maxExpressionDepth {
			return validationError("max_depth", p, "expression depth exceeds limit")
		}
		operators := 0
		if node.Select != "" {
			operators++
		}
		if node.Call != "" {
			operators++
		}
		if node.Literal != nil {
			operators++
		}
		if operators != 1 {
			return validationError("invalid_expression", p, "expression must contain exactly one operator")
		}
		if node.Literal != nil {
			if err := validateLiteral(node.Literal, p+".literal"); err != nil {
				return err
			}
			return nil
		}
		if node.Select != "" {
			if strings.TrimSpace(node.Select) == "" {
				return validationError("required", p+".select", "select is required")
			}
			return nil
		}
		arity, ok := callArities[node.Call]
		if strings.HasPrefix(node.Call, "fragment:") {
			if strings.TrimSpace(strings.TrimPrefix(node.Call, "fragment:")) == "" {
				return validationError("required", p+".call", "fragment name is required")
			}
			for i, arg := range node.Args {
				if err := walk(arg, fmt.Sprintf("%s.args[%d]", p, i), depth+1); err != nil {
					return err
				}
			}
			return nil
		}
		if !ok {
			return validationError("unsupported_operation", p+".call", "unsupported call "+strconv.Quote(node.Call))
		}
		if len(node.Args) < arity.min || (arity.max >= 0 && len(node.Args) > arity.max) {
			return validationError("invalid_arity", p+".args", fmt.Sprintf("call %q expects %d..%s arguments", node.Call, arity.min, maxString(arity.max)))
		}
		for i, arg := range node.Args {
			if err := walk(arg, fmt.Sprintf("%s.args[%d]", p, i), depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	return walk(e, path, 0)
}

type arity struct{ min, max int }

var callArities = map[string]arity{
	"coalesce": {1, -1}, "first": {1, 1}, "all": {1, 1}, "distinct": {1, 1},
	"concat": {1, -1}, "join": {2, 2}, "cast": {2, 2}, "reference_id": {1, 1},
	"path_segment": {1, 1}, "basename": {1, 1}, "last_segment": {1, 1},
	"sanitize_name": {1, 1}, "sanitize_graphql_name": {1, 1}, "uuid3": {3, 3}, "uuid5": {3, 3},
	"if": {3, 3}, "case": {2, -1},
}

func maxString(max int) string {
	if max < 0 {
		return "many"
	}
	return strconv.Itoa(max)
}

func validateLiteral(raw json.RawMessage, path string) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var value any
	if err := dec.Decode(&value); err != nil {
		return validationError("invalid_literal", path, err.Error())
	}
	switch v := value.(type) {
	case nil, string, bool, json.Number:
		return nil
	case []any:
		if len(v) > maxLiteralArray {
			return validationError("literal_limit", path, "literal array is too large")
		}
		for i, item := range v {
			switch item.(type) {
			case nil, string, bool, json.Number:
			default:
				return validationError("invalid_literal", fmt.Sprintf("%s[%d]", path, i), "literal arrays may contain only scalar values")
			}
		}
		return nil
	default:
		return validationError("invalid_literal", path, "literal must be a scalar or bounded scalar array")
	}
}

// CanonicalJSON returns stable compact JSON for a validated document.
func (b Bundle) CanonicalJSON() ([]byte, error) {
	if err := b.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(b)
}

// Digest returns the SHA-256 digest of CanonicalJSON encoded as lowercase hex.
func (b Bundle) Digest() (string, error) {
	canonical, err := b.CanonicalJSON()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:]), nil
}

// Explain returns the validated semantic shape and canonical digest.
func (b Bundle) Explain() (Explanation, error) {
	digest, err := b.Digest()
	if err != nil {
		return Explanation{}, err
	}
	exp := Explanation{RecipeSchemaVersion: b.RecipeSchemaVersion, Name: b.Name, TranslationVersion: b.TranslationVersion, Digest: digest, Outputs: make([]OutputExplanation, len(b.Outputs))}
	for i, out := range b.Outputs {
		e := OutputExplanation{Name: out.Name, RootResourceType: out.RootResourceType, RowGrain: out.RowGrain, Expanded: out.Expand != nil}
		for _, f := range out.Fields {
			e.FieldNames = append(e.FieldNames, f.Name)
		}
		for _, t := range out.Traversals {
			e.TraversalNames = append(e.TraversalNames, t.Name)
		}
		for _, d := range out.DynamicColumns {
			e.DynamicColumns = append(e.DynamicColumns, d.Name)
		}
		exp.Outputs[i] = e
	}
	return exp, nil
}

type ValidationError struct {
	Code    string
	Path    string
	Message string
}

func (e *ValidationError) Error() string { return e.Code + " at " + e.Path + ": " + e.Message }
func validationError(code, path, message string) error {
	return &ValidationError{Code: code, Path: path, Message: message}
}
