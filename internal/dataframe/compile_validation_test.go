package dataframe

import (
	"strings"
	"testing"
)

func TestCompileRejectsMalformedSelectorsInManualLoweredBuilders(t *testing.T) {
	_, err := Compile(Builder{
		Project:          "P1",
		RootResourceType: "Patient",
		PlanHint:         &PlanHint{Mode: "lowered", Profile: "test"},
		Fields:           []FieldSelect{{Name: "bad", Select: "name[not-an-index]"}},
	}, 1)
	if err == nil || !strings.Contains(err.Error(), "root field") {
		t.Fatalf("expected malformed selector error, got %v", err)
	}
}

func TestCompileTraverseSetHonorsExplicitUniqueFlag(t *testing.T) {
	compiled, err := Compile(Builder{
		Project:          "P1",
		RootResourceType: "Patient",
		PlanHint:         &PlanHint{Mode: "lowered", Profile: "test"},
		Sets: []NamedSet{{
			Name: "raw_children", Kind: SetKindTraverse, Label: "subject_Patient", ToResourceType: "Condition", Unique: false,
		}},
	}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(compiled.Query, "LET raw_children = UNIQUE") {
		t.Fatalf("unexpected deduplication for explicit non-unique set:\n%s", compiled.Query)
	}
}

func TestCompileRejectsUnknownOrUnsafeRootCollection(t *testing.T) {
	_, err := Compile(Builder{
		Project:          "P1",
		RootResourceType: "Patient RETURN SLEEP(1)",
		PlanHint:         &PlanHint{Mode: "lowered", Profile: "test"},
	}, 1)
	if err == nil || !strings.Contains(err.Error(), "not represented by the active generated FHIR schema") {
		t.Fatalf("expected generated-schema root validation error, got %v", err)
	}
}

func TestCompileRejectsUnsafeInternalSortField(t *testing.T) {
	_, err := Compile(Builder{
		Project:          "P1",
		RootResourceType: "Patient",
		PlanHint:         &PlanHint{Mode: "lowered", Profile: "test"},
		Sets: []NamedSet{{
			Name: "children", Kind: SetKindTraverse, Label: "subject_Patient", ToResourceType: "Condition",
			SortField: "_key RETURN SLEEP(1)",
		}},
	}, 1)
	if err == nil || !strings.Contains(err.Error(), "unsafe sort field") {
		t.Fatalf("expected sort-field validation error, got %v", err)
	}
}

func TestCompileRejectsRemovedRawAQLDerivedField(t *testing.T) {
	_, err := Compile(Builder{
		Project:          "P1",
		RootResourceType: "Patient",
		PlanHint:         &PlanHint{Mode: "lowered", Profile: "test"},
		DerivedFields: []DerivedField{{
			Name: "unsafe", Operation: "RAW_EXPR",
		}},
	}, 1)
	if err == nil || !strings.Contains(err.Error(), "unsupported operation") {
		t.Fatalf("expected removed raw AQL operation rejection, got %v", err)
	}
}
