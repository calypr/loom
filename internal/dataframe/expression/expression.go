// Package expression contains the typed, backend-neutral expression AST used
// by dataframe recipes.  It intentionally has no knowledge of AQL, SQL, or
// output names: a physical compiler consumes the checked tree later.
package expression

import (
	"fmt"
	"reflect"
	"regexp"
	"strings"
)

// ValueKind is the logical type of an expression value. Cardinality is kept
// separately in Type so a repeated string and a scalar string remain visibly
// different to a compiler.
type ValueKind string

const (
	KindNull     ValueKind = "null"
	KindBoolean  ValueKind = "boolean"
	KindInteger  ValueKind = "integer"
	KindDecimal  ValueKind = "decimal"
	KindString   ValueKind = "string"
	KindDate     ValueKind = "date"
	KindDateTime ValueKind = "date_time"
	KindCode     ValueKind = "code"
	KindUUID     ValueKind = "uuid"
	// KindObject represents a structured value whose fields are resolved by a
	// lexical recipe scope. It is intentionally backend-neutral; physical
	// lowerers decide whether the object is a document, object expression, or
	// relationship item.
	KindObject ValueKind = "object"
)

// Cardinality describes the number of values produced for one row context.
type Cardinality string

const (
	RequiredOne Cardinality = "required_one"
	OptionalOne Cardinality = "optional_one"
	Many        Cardinality = "many"
)

func (c Cardinality) Valid() bool {
	return c == RequiredOne || c == OptionalOne || c == Many
}

func (c Cardinality) AllowsMany() bool { return c == Many }
func (c Cardinality) Optional() bool   { return c == OptionalOne || c == Many }

// Type is a logical expression type. Element is reserved for object-like
// types in later recipe versions; scalar arrays are represented by the same
// Kind with Cardinality=Many so functions can reason about them uniformly.
type Type struct {
	Kind        ValueKind   `json:"kind"`
	Cardinality Cardinality `json:"cardinality"`
	Element     *Type       `json:"element,omitempty"`
}

func (t Type) Valid() bool {
	if t.Kind == "" || t.Cardinality == "" || !t.Cardinality.Valid() {
		return false
	}
	switch t.Kind {
	case KindNull, KindBoolean, KindInteger, KindDecimal, KindString,
		KindDate, KindDateTime, KindCode, KindUUID, KindObject:
		return t.Element == nil
	default:
		return false
	}
}

func (t Type) String() string {
	if t.Kind == "" {
		return "<invalid>"
	}
	return string(t.Kind) + "/" + string(t.Cardinality)
}

func scalar(kind ValueKind) Type { return Type{Kind: kind, Cardinality: RequiredOne} }

// NullBehavior makes null handling part of semantic meaning rather than an
// incidental renderer choice.
type NullBehavior string

const (
	NullPropagate NullBehavior = "propagate"
	NullEmpty     NullBehavior = "empty"
	NullError     NullBehavior = "error"
)

func (n NullBehavior) Valid() bool {
	return n == "" || n == NullPropagate || n == NullEmpty || n == NullError
}

// NodeKind identifies a node in the expression tree.
type NodeKind string

const (
	SelectorNode NodeKind = "selector"
	LiteralNode  NodeKind = "literal"
	CallNode     NodeKind = "call"
)

// SelectorRef identifies a value in a named row context. The path is a
// logical selector, not a query-language fragment.
type SelectorRef struct {
	Context string `json:"context,omitempty"`
	Path    string `json:"path"`
}

func (s SelectorRef) String() string {
	if strings.TrimSpace(s.Context) == "" {
		return strings.TrimSpace(s.Path)
	}
	return strings.TrimSpace(s.Context) + "." + strings.TrimPrefix(strings.TrimSpace(s.Path), ".")
}

var selectorNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.\[\]-]*$`)

func (s SelectorRef) Validate() error {
	if strings.TrimSpace(s.Path) == "" {
		return fmt.Errorf("selector path is required")
	}
	if strings.TrimSpace(s.Context) != "" && !selectorNamePattern.MatchString(s.Context) {
		return fmt.Errorf("selector context %q is not a logical name", s.Context)
	}
	path := strings.TrimSpace(s.Path)
	if !selectorNamePattern.MatchString(path) || strings.Contains(path, "..") || strings.Contains(path, "[][") {
		return fmt.Errorf("selector path %q is not a logical selector", s.Path)
	}
	return nil
}

// Literal is a typed constant. Value is intentionally opaque to this package
// after type validation, which keeps physical encodings out of the AST.
type Literal struct {
	Type  Type `json:"type"`
	Value any  `json:"value,omitempty"`
}

// Call is a named generic function. Target is only used by cast and is kept
// as a type rather than accepting a backend or language type name.
type Call struct {
	Name   string       `json:"name"`
	Args   []Expression `json:"args,omitempty"`
	Target *Type        `json:"target,omitempty"`
}

// Expression is a lowering-neutral AST node. Type is an optional annotation;
// Check recomputes it and rejects a conflicting annotation.
type Expression struct {
	Kind         NodeKind     `json:"kind"`
	Type         Type         `json:"type,omitempty"`
	NullBehavior NullBehavior `json:"nullBehavior,omitempty"`
	Selector     *SelectorRef `json:"selector,omitempty"`
	Literal      *Literal     `json:"literal,omitempty"`
	Call         *Call        `json:"call,omitempty"`
}

// SelectorResolver supplies schema-aware types without coupling this package
// to a particular generated FHIR schema or storage backend.
type SelectorResolver func(SelectorRef) (Type, error)

// TypeContext is the environment used to type-check selectors. Selectors map
// keys may be either SelectorRef.String() or the path alone.
type TypeContext struct {
	Selectors map[string]Type
	Resolve   SelectorResolver
	MaxDepth  int
	MaxNodes  int
}

// CheckedExpression is an expression plus its inferred semantic result type.
type CheckedExpression struct {
	Expression Expression
	Type       Type
}

// Check validates an expression tree and returns its inferred type.
func (e Expression) Check(ctx TypeContext) (CheckedExpression, error) {
	if ctx.MaxDepth <= 0 {
		ctx.MaxDepth = 64
	}
	if ctx.MaxNodes <= 0 {
		ctx.MaxNodes = 4096
	}
	state := checkState{ctx: ctx}
	t, err := state.check(e, "expr", 0)
	if err != nil {
		return CheckedExpression{}, err
	}
	if e.Type.Valid() && e.Type != t {
		return CheckedExpression{}, fmt.Errorf("expr: declared type %s conflicts with inferred type %s", e.Type, t)
	}
	return CheckedExpression{Expression: e, Type: t}, nil
}

func (e Expression) Validate(ctx TypeContext) error {
	_, err := e.Check(ctx)
	return err
}

type checkState struct {
	ctx   TypeContext
	nodes int
}

func (s *checkState) check(e Expression, path string, depth int) (Type, error) {
	s.nodes++
	if s.nodes > s.ctx.MaxNodes {
		return Type{}, fmt.Errorf("%s: expression node limit %d exceeded", path, s.ctx.MaxNodes)
	}
	if depth > s.ctx.MaxDepth {
		return Type{}, fmt.Errorf("%s: expression depth limit %d exceeded", path, s.ctx.MaxDepth)
	}
	if !e.NullBehavior.Valid() {
		return Type{}, fmt.Errorf("%s: invalid null behavior %q", path, e.NullBehavior)
	}
	switch e.Kind {
	case SelectorNode:
		if e.Selector == nil || e.Literal != nil || e.Call != nil {
			return Type{}, fmt.Errorf("%s: selector node requires only selector", path)
		}
		if err := e.Selector.Validate(); err != nil {
			return Type{}, fmt.Errorf("%s: %w", path, err)
		}
		t, ok := s.ctx.Selectors[e.Selector.String()]
		if !ok {
			t, ok = s.ctx.Selectors[e.Selector.Path]
		}
		if !ok && s.ctx.Resolve != nil {
			var err error
			t, err = s.ctx.Resolve(*e.Selector)
			if err != nil {
				return Type{}, fmt.Errorf("%s: resolve selector %q: %w", path, e.Selector.String(), err)
			}
			ok = true
		}
		if !ok {
			return Type{}, fmt.Errorf("%s: selector %q has no type binding", path, e.Selector.String())
		}
		if !t.Valid() {
			return Type{}, fmt.Errorf("%s: selector %q has invalid type %s", path, e.Selector.String(), t)
		}
		return t, nil
	case LiteralNode:
		if e.Literal == nil || e.Selector != nil || e.Call != nil {
			return Type{}, fmt.Errorf("%s: literal node requires only literal", path)
		}
		if err := validateLiteral(*e.Literal); err != nil {
			return Type{}, fmt.Errorf("%s: %w", path, err)
		}
		return e.Literal.Type, nil
	case CallNode:
		if e.Call == nil || e.Selector != nil || e.Literal != nil {
			return Type{}, fmt.Errorf("%s: call node requires only call", path)
		}
		return s.checkCall(*e.Call, path, depth)
	default:
		return Type{}, fmt.Errorf("%s: unknown expression node kind %q", path, e.Kind)
	}
}

func validateLiteral(l Literal) error {
	if !l.Type.Valid() {
		return fmt.Errorf("literal type %s is invalid", l.Type)
	}
	if l.Type.Kind == KindNull {
		if l.Value != nil {
			return fmt.Errorf("null literal must have nil value")
		}
		return nil
	}
	if l.Value == nil {
		return fmt.Errorf("%s literal requires a value", l.Type.Kind)
	}
	if l.Type.Cardinality == Many {
		valueSlice := reflect.ValueOf(l.Value)
		if valueSlice.Kind() != reflect.Slice && valueSlice.Kind() != reflect.Array {
			return fmt.Errorf("many literal requires a slice, got %T", l.Value)
		}
		for index := 0; index < valueSlice.Len(); index++ {
			value := valueSlice.Index(index).Interface()
			if err := validateLiteral(Literal{Type: Type{Kind: l.Type.Kind, Cardinality: RequiredOne}, Value: value}); err != nil {
				return err
			}
		}
		return nil
	}
	if l.Type.Cardinality == Many {
		value := reflect.ValueOf(l.Value)
		if value.Kind() != reflect.Array && value.Kind() != reflect.Slice {
			return fmt.Errorf("many literal value %T must be an array or slice", l.Value)
		}
		for index := 0; index < value.Len(); index++ {
			if !validScalarLiteral(l.Type.Kind, value.Index(index).Interface()) {
				return fmt.Errorf("many literal element %d is incompatible with %s", index, l.Type.Kind)
			}
		}
		return nil
	}
	if !validScalarLiteral(l.Type.Kind, l.Value) {
		return fmt.Errorf("literal value %T is incompatible with %s", l.Value, l.Type.Kind)
	}
	return nil
}

func validScalarLiteral(kind ValueKind, value any) bool {
	valid := false
	switch kind {
	case KindBoolean:
		_, valid = value.(bool)
	case KindInteger:
		switch value.(type) {
		case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
			valid = true
		}
	case KindDecimal:
		switch value.(type) {
		case float32, float64:
			valid = true
		}
	case KindString, KindDate, KindDateTime, KindCode, KindUUID:
		_, valid = value.(string)
	}
	return valid
}

func (s *checkState) checkCall(c Call, path string, depth int) (Type, error) {
	name := strings.ToLower(strings.TrimSpace(c.Name))
	if name == "" {
		return Type{}, fmt.Errorf("%s: function name is required", path)
	}
	args := make([]Type, len(c.Args))
	for i, arg := range c.Args {
		t, err := s.check(arg, fmt.Sprintf("%s.args[%d]", path, i), depth+1)
		if err != nil {
			return Type{}, err
		}
		args[i] = t
	}
	if c.Target != nil && !c.Target.Valid() {
		return Type{}, fmt.Errorf("%s: cast target type %s is invalid", path, c.Target)
	}
	result, err := inferCall(name, args, c.Target)
	if err != nil {
		return Type{}, fmt.Errorf("%s: %w", path, err)
	}
	return result, nil
}

func inferCall(name string, args []Type, target *Type) (Type, error) {
	if name == "coalesce" || name == "fallback" {
		if len(args) == 0 {
			return Type{}, fmt.Errorf("%s requires at least one argument", name)
		}
		var result Type
		for i, arg := range args {
			if arg.Kind == KindNull {
				continue
			}
			if i == 0 || result.Kind == "" {
				result = arg
				continue
			}
			if !sameShape(result, arg) {
				return Type{}, fmt.Errorf("%s arguments must have compatible types, got %s and %s", name, result, arg)
			}
			result.Cardinality = mergeCardinality(result.Cardinality, arg.Cardinality)
		}
		if result.Kind == "" {
			return Type{Kind: KindNull, Cardinality: OptionalOne}, nil
		}
		return result, nil
	}
	if name == "first" {
		if len(args) != 1 {
			return Type{}, fmt.Errorf("first requires one argument")
		}
		if args[0].Kind == KindNull {
			return Type{Kind: KindNull, Cardinality: OptionalOne}, nil
		}
		args[0].Cardinality = OptionalOne
		return args[0], nil
	}
	if name == "all" || name == "distinct" {
		if len(args) != 1 {
			return Type{}, fmt.Errorf("%s requires one argument", name)
		}
		if args[0].Kind == KindNull {
			return Type{Kind: KindNull, Cardinality: Many}, nil
		}
		args[0].Cardinality = Many
		return args[0], nil
	}
	if name == "concat" {
		if len(args) < 1 {
			return Type{}, fmt.Errorf("concat requires at least one argument")
		}
		optional := false
		for _, arg := range args {
			if arg.Kind != KindString || arg.Cardinality == Many {
				return Type{}, fmt.Errorf("concat accepts only scalar strings, got %s", arg)
			}
			optional = optional || arg.Cardinality == OptionalOne
		}
		return Type{Kind: KindString, Cardinality: optionalCardinality(optional)}, nil
	}
	if name == "join" {
		if len(args) != 2 {
			return Type{}, fmt.Errorf("join requires values and delimiter arguments")
		}
		if args[0].Kind != KindString || args[0].Cardinality != Many || args[1].Kind != KindString || args[1].Cardinality == Many {
			return Type{}, fmt.Errorf("join requires repeated string values and a scalar string delimiter")
		}
		return Type{Kind: KindString, Cardinality: OptionalOne}, nil
	}
	if name == "cast" {
		if len(args) != 1 || target == nil {
			return Type{}, fmt.Errorf("cast requires one argument and a target type")
		}
		if target.Kind == KindNull {
			return Type{}, fmt.Errorf("cast target cannot be null")
		}
		if args[0].Cardinality == Many || target.Cardinality == Many {
			return Type{}, fmt.Errorf("cast does not accept repeated values")
		}
		result := *target
		if args[0].Cardinality == OptionalOne {
			result.Cardinality = OptionalOne
		}
		return result, nil
	}
	if name == "reference_id" || name == "path_segment" || name == "sanitize_name" {
		if len(args) != 1 || args[0].Kind != KindString || args[0].Cardinality == Many {
			return Type{}, fmt.Errorf("%s requires one scalar string argument", name)
		}
		return Type{Kind: KindString, Cardinality: OptionalOne}, nil
	}
	if name == "uuid3" || name == "uuid5" {
		if len(args) < 2 || !allScalarKind(args, KindString) {
			return Type{}, fmt.Errorf("%s requires a namespace and at least one name scalar string", name)
		}
		optional := false
		for _, arg := range args {
			optional = optional || arg.Cardinality == OptionalOne
		}
		return Type{Kind: KindUUID, Cardinality: optionalCardinality(optional)}, nil
	}
	if name == "if" {
		if len(args) != 3 || args[0].Kind != KindBoolean || args[0].Cardinality == Many {
			return Type{}, fmt.Errorf("if requires scalar boolean condition and two branches")
		}
		return mergeBranchTypes(args[1], args[2])
	}
	if name == "case" {
		if len(args) < 2 {
			return Type{}, fmt.Errorf("case requires at least one condition/result pair")
		}
		withElse := len(args)%2 == 1
		lastPairEnd := len(args)
		if withElse {
			lastPairEnd--
		}
		if lastPairEnd%2 != 0 {
			return Type{}, fmt.Errorf("case requires condition/result pairs and an optional else result")
		}
		var result Type
		for i := 0; i < lastPairEnd; i += 2 {
			if args[i].Kind != KindBoolean || args[i].Cardinality == Many {
				return Type{}, fmt.Errorf("case conditions must be scalar booleans")
			}
			if result.Kind == "" {
				result = args[i+1]
				continue
			}
			var err error
			result, err = mergeBranchTypes(result, args[i+1])
			if err != nil {
				return Type{}, err
			}
		}
		if !withElse {
			return mergeBranchTypes(result, NullType())
		}
		return mergeBranchTypes(result, args[len(args)-1])
	}
	if name == "not" {
		if len(args) != 1 || args[0].Kind != KindBoolean || args[0].Cardinality == Many {
			return Type{}, fmt.Errorf("not requires one scalar boolean")
		}
		return Type{Kind: KindBoolean, Cardinality: OptionalOne}, nil
	}
	if name == "and" || name == "or" {
		if len(args) < 2 {
			return Type{}, fmt.Errorf("%s requires at least two arguments", name)
		}
		for _, arg := range args {
			if arg.Kind != KindBoolean || arg.Cardinality == Many {
				return Type{}, fmt.Errorf("%s accepts only scalar booleans", name)
			}
		}
		return Type{Kind: KindBoolean, Cardinality: OptionalOne}, nil
	}
	if name == "eq" || name == "neq" || name == "gt" || name == "gte" || name == "lt" || name == "lte" || name == "contains" {
		if len(args) != 2 || args[0].Cardinality == Many || args[1].Cardinality == Many || !sameShape(args[0], args[1]) {
			return Type{}, fmt.Errorf("%s requires two compatible scalar arguments", name)
		}
		if name == "contains" && args[0].Kind != KindString {
			return Type{}, fmt.Errorf("contains requires strings")
		}
		return Type{Kind: KindBoolean, Cardinality: OptionalOne}, nil
	}
	return Type{}, fmt.Errorf("unsupported function %q", name)
}

func allScalarKind(args []Type, kind ValueKind) bool {
	for _, arg := range args {
		if arg.Kind != kind || arg.Cardinality == Many {
			return false
		}
	}
	return true
}

func sameShape(a, b Type) bool {
	return a.Kind == b.Kind && a.Cardinality != Many && b.Cardinality != Many
}

func mergeCardinality(a, b Cardinality) Cardinality {
	if a == Many || b == Many {
		return Many
	}
	if a == OptionalOne || b == OptionalOne {
		return OptionalOne
	}
	return RequiredOne
}

func optionalCardinality(optional bool) Cardinality {
	if optional {
		return OptionalOne
	}
	return RequiredOne
}

func NullType() Type { return Type{Kind: KindNull, Cardinality: OptionalOne} }

func mergeBranchTypes(a, b Type) (Type, error) {
	if a.Kind == KindNull {
		b.Cardinality = OptionalOne
		return b, nil
	}
	if b.Kind == KindNull {
		a.Cardinality = OptionalOne
		return a, nil
	}
	if a.Kind != b.Kind || a.Cardinality == Many || b.Cardinality == Many || (a.Cardinality == Many) != (b.Cardinality == Many) {
		return Type{}, fmt.Errorf("branches must have compatible scalar types, got %s and %s", a, b)
	}
	a.Cardinality = mergeCardinality(a.Cardinality, b.Cardinality)
	return a, nil
}

// Constructors keep callers from manufacturing malformed tagged nodes.
func Select(ref SelectorRef) Expression {
	return Expression{Kind: SelectorNode, Selector: &ref, NullBehavior: NullPropagate}
}

func Constant(t Type, value any) Expression {
	return Expression{Kind: LiteralNode, Literal: &Literal{Type: t, Value: value}, NullBehavior: NullPropagate}
}

func Null() Expression {
	return Constant(NullType(), nil)
}

func Function(name string, args ...Expression) Expression {
	return Expression{Kind: CallNode, Call: &Call{Name: name, Args: args}, NullBehavior: NullPropagate}
}

func Cast(value Expression, target Type) Expression {
	return Expression{Kind: CallNode, Call: &Call{Name: "cast", Args: []Expression{value}, Target: &target}, NullBehavior: NullPropagate}
}

func Coalesce(args ...Expression) Expression { return Function("coalesce", args...) }

// EqualTypes is useful to compiler adapters that need to compare checked
// results without depending on implementation details of this package.
func EqualTypes(a, b Type) bool { return reflect.DeepEqual(a, b) }
