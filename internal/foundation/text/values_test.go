package text

import "testing"

func TestFirstNonBlankAndFirstTrimmed(t *testing.T) {
	for _, tc := range []struct {
		values       []string
		raw, trimmed string
	}{
		{},
		{values: []string{"", " ", "\u2003"}},
		{values: []string{" ", "  first\t", "second"}, raw: "  first\t", trimmed: "first"},
		{values: []string{"first", "second"}, raw: "first", trimmed: "first"},
	} {
		if raw, trimmed := FirstNonBlank(tc.values...), FirstTrimmed(tc.values...); raw != tc.raw || trimmed != tc.trimmed {
			t.Fatalf("first for %q = (%q, %q), want (%q, %q)", tc.values, raw, trimmed, tc.raw, tc.trimmed)
		}
	}
}

func TestEqualUnorderedRetainsMultiplicityAndInputOrder(t *testing.T) {
	left, right := []string{"b", "a", "b"}, []string{"b", "b", "a"}
	if !EqualUnordered(left, right) || EqualUnordered(left, []string{"a", "a", "b"}) || EqualUnordered(left, []string{"b", "a"}) {
		t.Fatal("unordered equality lost multiplicity")
	}
	if left[0] != "b" || right[0] != "b" || !EqualUnordered(nil, []string{}) {
		t.Fatal("unordered equality must preserve its inputs and accept both empty forms")
	}
}
