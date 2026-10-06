package ir

import "testing"

func TestClickHouseCombineScalarBaseTypeMatchesCombinePolicies(t *testing.T) {
	for _, test := range []struct {
		name  string
		type_ string
		kind  PhysicalCombineKind
		base  string
		ok    bool
	}{
		{name: "key join string", type_: "String", kind: PhysicalCombineKeyJoin, base: "String", ok: true},
		{name: "key join nullable string", type_: "Nullable(String)", kind: PhysicalCombineKeyJoin, base: "String", ok: true},
		{name: "key join date time", type_: "DateTime64(3)", kind: PhysicalCombineKeyJoin, base: "DateTime64(3)", ok: true},
		{name: "key join rejects UUID", type_: "UUID", kind: PhysicalCombineKeyJoin, ok: false},
		{name: "key join rejects float", type_: "Float64", kind: PhysicalCombineKeyJoin, ok: false},
		{name: "membership rejects nullable", type_: "Nullable(String)", kind: PhysicalCombineMembership, ok: false},
		{name: "membership accepts scalar", type_: "String", kind: PhysicalCombineMembership, base: "String", ok: true},
		{name: "append nullable float", type_: "Nullable(Float64)", kind: PhysicalCombineAppend, base: "Float64", ok: true},
		{name: "append UUID", type_: "UUID", kind: PhysicalCombineAppend, base: "UUID", ok: true},
		{name: "append rejects unsupported decimal", type_: "Decimal64(3)", kind: PhysicalCombineAppend, ok: false},
		{name: "rejects array", type_: "Array(String)", kind: PhysicalCombineKeyJoin, ok: false},
		{name: "rejects nullable array", type_: "Nullable(Array(String))", kind: PhysicalCombineKeyJoin, ok: false},
		{name: "rejects nested nullable", type_: "Nullable(Nullable(String))", kind: PhysicalCombineKeyJoin, ok: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			base, ok := ClickHouseCombineScalarBaseType(test.type_, test.kind)
			if base != test.base || ok != test.ok {
				t.Fatalf("ClickHouseCombineScalarBaseType(%q, %q) = (%q, %t), want (%q, %t)", test.type_, test.kind, base, ok, test.base, test.ok)
			}
		})
	}
}
