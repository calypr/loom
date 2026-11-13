package expression

import (
	"fmt"
	"regexp"
	"strings"
)

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
	KindObject   ValueKind = "object"
)

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

type NullBehavior string

const (
	NullPropagate NullBehavior = "propagate"
	NullEmpty     NullBehavior = "empty"
	NullError     NullBehavior = "error"
)

func (n NullBehavior) Valid() bool {
	return n == "" || n == NullPropagate || n == NullEmpty || n == NullError
}

type NodeKind string

const (
	SelectorNode    NodeKind = "selector"
	LiteralNode     NodeKind = "literal"
	CallNode        NodeKind = "call"
	DocumentRefNode NodeKind = "document_ref"
)

type DocumentRef struct {
	Context string `json:"context,omitempty"`
}

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

type Literal struct {
	Type  Type `json:"type"`
	Value any  `json:"value,omitempty"`
}

type Call struct {
	Name   string       `json:"name"`
	Args   []Expression `json:"args,omitempty"`
	Target *Type        `json:"target,omitempty"`
}

type Expression struct {
	Kind         NodeKind     `json:"kind"`
	Type         Type         `json:"type,omitempty"`
	NullBehavior NullBehavior `json:"nullBehavior,omitempty"`
	Selector     *SelectorRef `json:"selector,omitempty"`
	Literal      *Literal     `json:"literal,omitempty"`
	Call         *Call        `json:"call,omitempty"`
	Document     *DocumentRef `json:"document,omitempty"`
}

type SelectorResolver func(SelectorRef) (Type, error)

type TypeContext struct {
	Selectors map[string]Type
	Resolve   SelectorResolver
	MaxDepth  int
	MaxNodes  int
}

type CheckedExpression struct {
	Expression Expression
	Type       Type
}

func Select(ref SelectorRef) Expression {
	return Expression{Kind: SelectorNode, Selector: &ref, NullBehavior: NullPropagate}
}

func Document(context string) Expression {
	return Expression{Kind: DocumentRefNode, Document: &DocumentRef{Context: strings.TrimSpace(context)}, NullBehavior: NullPropagate}
}

func Constant(t Type, value any) Expression {
	return Expression{Kind: LiteralNode, Literal: &Literal{Type: t, Value: value}, NullBehavior: NullPropagate}
}

func Function(name string, args ...Expression) Expression {
	return Expression{Kind: CallNode, Call: &Call{Name: name, Args: args}, NullBehavior: NullPropagate}
}
