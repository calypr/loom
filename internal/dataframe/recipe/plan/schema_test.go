package plan

import "testing"

func TestFreezeIsBoundedDeterministicAndTyped(t *testing.T) {
	first, err := Freeze(DynamicSpec{Name: "code", MaxColumns: 4}, []Candidate{{Key: "10-a", ValueType: "string"}, {Key: "b", ValueType: "integer"}})
	if err != nil {
		t.Fatal(err)
	}
	second, err := Freeze(DynamicSpec{Name: "code", MaxColumns: 4}, []Candidate{{Key: "b", ValueType: "integer"}, {Key: "10-a", ValueType: "string"}})
	if err != nil {
		t.Fatal(err)
	}
	if first.Digest != second.Digest || first.Columns[0].Name != "code__10_a" {
		t.Fatalf("unstable schema: %#v %#v", first, second)
	}
}

func TestFreezeRejectsSanitizedCollisionAndLimit(t *testing.T) {
	if _, err := Freeze(DynamicSpec{Name: "x"}, []Candidate{{Key: "a-b", ValueType: "string"}, {Key: "a_b", ValueType: "string"}}); err == nil {
		t.Fatal("expected collision")
	}
	if _, err := Freeze(DynamicSpec{Name: "x", MaxColumns: 1}, []Candidate{{Key: "a", ValueType: "string"}, {Key: "b", ValueType: "string"}}); err == nil {
		t.Fatal("expected limit")
	}
}
