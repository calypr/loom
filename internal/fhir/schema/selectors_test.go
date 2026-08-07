package schema

import "testing"

func TestSelectorFromFieldRoundTrips(t *testing.T) {
	field, ok := LookupField("DocumentReference", "content[].attachment.title")
	if !ok {
		t.Fatal("expected document reference field")
	}
	spec := SelectorFromField(field)
	if spec.SourcePath != "content[].attachment" {
		t.Fatalf("unexpected source path: %q", spec.SourcePath)
	}
	if spec.ValuePath != "title" {
		t.Fatalf("unexpected value path: %q", spec.ValuePath)
	}
	if got := SelectorExpression(spec); got != "content[].attachment.title" {
		t.Fatalf("unexpected selector expression: %q", got)
	}
}

func TestParseSelectorCanonicalizesIndexedPaths(t *testing.T) {
	sel, err := ParseSelector(`identifier[0].value where system contains "case_id"`)
	if err != nil {
		t.Fatal(err)
	}
	if got := sel.CanonicalPath(); got != "identifier[].value" {
		t.Fatalf("unexpected canonical path: %q", got)
	}
	if sel.Filter == nil || sel.Filter.Field != "system" || sel.Filter.Needle != "case_id" {
		t.Fatalf("unexpected filter: %#v", sel.Filter)
	}
}

func TestChoiceValueSelectorOptions(t *testing.T) {
	options := ChoiceValueSelectorOptions("Observation")
	if len(options) == 0 {
		t.Fatal("expected observation value selector options")
	}
	found := false
	for _, option := range options {
		if SelectorExpression(option) == "valueQuantity.value" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected valueQuantity.value option, got %#v", options)
	}
}
