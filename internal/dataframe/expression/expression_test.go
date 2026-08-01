package expression

import (
	"strings"
	"testing"
)

func TestCheckSelectorUsesBoundSchemaType(t *testing.T) {
	checked, err := Select(SelectorRef{Context: "root", Path: "id"}).Check(TypeContext{
		Selectors: map[string]Type{"root.id": {Kind: KindString, Cardinality: RequiredOne}},
	})
	if err != nil {
		t.Fatalf("check selector: %v", err)
	}
	if checked.Type != (Type{Kind: KindString, Cardinality: RequiredOne}) {
		t.Fatalf("type = %s", checked.Type)
	}
}

func TestCheckDocumentRefIsRequiredObject(t *testing.T) {
	checked, err := Document("root").Check(TypeContext{})
	if err != nil {
		t.Fatal(err)
	}
	if checked.Type != (Type{Kind: KindObject, Cardinality: RequiredOne}) {
		t.Fatalf("type = %s, want object/required_one", checked.Type)
	}
	if err := Document("bad context!").Validate(TypeContext{}); err == nil {
		t.Fatal("invalid document context unexpectedly passed")
	}
}

func TestCheckOperationTypes(t *testing.T) {
	stringOne := Constant(Type{Kind: KindString, Cardinality: RequiredOne}, "x")
	ctx := TypeContext{Selectors: map[string]Type{"root.alt": {Kind: KindString, Cardinality: OptionalOne}}}
	tests := []struct {
		name string
		expr Expression
		want Type
	}{
		{"coalesce_string", Function("coalesce_string", Constant(Type{Kind: KindInteger, Cardinality: OptionalOne}, int64(3)), stringOne), Type{Kind: KindString, Cardinality: OptionalOne}},
		{"concat", Function("concat", stringOne, stringOne), Type{Kind: KindString, Cardinality: RequiredOne}},
		{"first", Function("first", Function("all", stringOne)), Type{Kind: KindString, Cardinality: OptionalOne}},
		{"uuid5", Function("uuid5", stringOne, stringOne), Type{Kind: KindUUID, Cardinality: RequiredOne}},
		{"case", Function("case", Constant(Type{Kind: KindBoolean, Cardinality: RequiredOne}, true), stringOne), Type{Kind: KindString, Cardinality: OptionalOne}},
		{"eq", Function("eq", stringOne, stringOne), Type{Kind: KindBoolean, Cardinality: OptionalOne}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			checked, err := test.expr.Check(ctx)
			if err != nil {
				t.Fatalf("check: %v", err)
			}
			if checked.Type != test.want {
				t.Fatalf("type = %s, want %s", checked.Type, test.want)
			}
		})
	}
}

func TestCheckRejectsInvalidArityAndTypes(t *testing.T) {
	stringOne := Constant(Type{Kind: KindString, Cardinality: RequiredOne}, "x")
	tests := []Expression{
		Function("concat"),
		Function("join", stringOne, stringOne),
		Function("uuid5", stringOne),
		Function("unknown", stringOne),
		Function("if", stringOne, stringOne, stringOne),
	}
	for i, expr := range tests {
		if err := expr.Validate(TypeContext{}); err == nil {
			t.Errorf("case %d unexpectedly passed", i)
		}
	}
}

func TestCheckRejectsMalformedSelectorAndLiteral(t *testing.T) {
	if err := Select(SelectorRef{Path: "root.id\" FOR x IN y"}).Validate(TypeContext{}); err == nil || !strings.Contains(err.Error(), "logical selector") {
		t.Fatalf("malformed selector error = %v", err)
	}
	bad := Constant(Type{Kind: KindInteger, Cardinality: RequiredOne}, "not an integer")
	if err := bad.Validate(TypeContext{}); err == nil || !strings.Contains(err.Error(), "incompatible") {
		t.Fatalf("bad literal error = %v", err)
	}
}

func TestCheckUsesResolverAndLimitsTree(t *testing.T) {
	checked, err := Select(SelectorRef{Context: "member", Path: "entity.reference"}).Check(TypeContext{
		Resolve: func(ref SelectorRef) (Type, error) {
			if ref.String() != "member.entity.reference" {
				t.Fatalf("resolved ref = %q", ref.String())
			}
			return Type{Kind: KindString, Cardinality: OptionalOne}, nil
		},
	})
	if err != nil || checked.Type.Kind != KindString {
		t.Fatalf("resolver result = %#v, err=%v", checked, err)
	}

	deep := Constant(Type{Kind: KindString, Cardinality: RequiredOne}, "x")
	for i := 0; i < 4; i++ {
		deep = Function("concat", deep)
	}
	if err := deep.Validate(TypeContext{MaxDepth: 2}); err == nil || !strings.Contains(err.Error(), "depth limit") {
		t.Fatalf("depth error = %v", err)
	}
}

func TestManyLiteralValidatesElements(t *testing.T) {
	good := Constant(Type{Kind: KindString, Cardinality: Many}, []string{"a", "b"})
	if err := good.Validate(TypeContext{}); err != nil {
		t.Fatalf("many literal: %v", err)
	}
	bad := Constant(Type{Kind: KindString, Cardinality: Many}, []int{1})
	if err := bad.Validate(TypeContext{}); err == nil {
		t.Fatal("incompatible many literal unexpectedly passed")
	}
}
