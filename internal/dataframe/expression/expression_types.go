// Package expression contains the typed, backend-neutral expression AST used
// by dataframe recipes.  It intentionally has no knowledge of AQL, SQL, or
// output names: a physical compiler consumes the checked tree later.
package expression

import (
	"fmt"
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
	// DocumentRefNode denotes the complete document in a lexical resource
	// context.  It intentionally carries no selector or backend query text;
	// physical lowerers decide how to materialize the document envelope.
	DocumentRefNode NodeKind = "document_ref"
)

// DocumentRef identifies the complete document in a named lexical context.
// Context defaults to root and is validated against the surrounding semantic
// scope by the recipe planner.
type DocumentRef struct {
	Context string `json:"context,omitempty"`
}

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
	Document     *DocumentRef `json:"document,omitempty"`
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
func Select(ref SelectorRef) Expression {
	return Expression{Kind: SelectorNode, Selector: &ref, NullBehavior: NullPropagate}
}

// Document returns an expression referring to the complete document in
// context. An empty context is normalized to root by semantic planning.
func Document(context string) Expression {
	return Expression{Kind: DocumentRefNode, Document: &DocumentRef{Context: strings.TrimSpace(context)}, NullBehavior: NullPropagate}
}

func Constant(t Type, value any) Expression {
	return Expression{Kind: LiteralNode, Literal: &Literal{Type: t, Value: value}, NullBehavior: NullPropagate}
}

func Function(name string, args ...Expression) Expression {
	return Expression{Kind: CallNode, Call: &Call{Name: name, Args: args}, NullBehavior: NullPropagate}
}
