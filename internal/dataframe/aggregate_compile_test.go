package dataframe

import (
	"strings"
	"testing"
)

func TestGenericExistsAggregateWithoutPredicateTestsSetNonEmptiness(t *testing.T) {
	compiled, err := CompileRequest(Builder{
		Project: "P1", RootResourceType: "Specimen",
		Traversals: []TraversalStep{{
			Label: "subject_Specimen", ToResourceType: "DocumentReference", Alias: "file",
			Aggregates: []AggregateSelect{{Name: "has_file", Operation: "EXISTS"}},
		}},
	}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(compiled.Query, `"file__has_file": LENGTH(generic_file_set) > 0`) {
		t.Fatalf("EXISTS aggregate did not compile as set non-emptiness:\n%s", compiled.Query)
	}
}

func TestSemanticPlanRejectsAggregateWithoutRequiredInput(t *testing.T) {
	_, err := BuildSemanticPlan(Builder{
		Project: "P1", RootResourceType: "Specimen",
		Aggregates: []AggregateSelect{{Name: "distinct", Operation: "COUNT_DISTINCT"}},
	})
	// COUNT_DISTINCT is accepted by the current public shape but requires an
	// input before lowering; assert it never reaches a silent zero-value plan.
	if err == nil {
		t.Fatal("COUNT_DISTINCT without a selector unexpectedly reached semantic plan")
	}
}

func TestGenericMinAndMaxAggregatesLowerToTypedReductions(t *testing.T) {
	compiled, err := CompileRequest(Builder{
		Project: "P1", RootResourceType: "Specimen",
		Traversals: []TraversalStep{{
			Label: "subject_Specimen", ToResourceType: "DocumentReference", Alias: "file",
			Aggregates: []AggregateSelect{
				{Name: "min_size", Operation: "MIN", Select: "content[].attachment.size"},
				{Name: "max_size", Operation: "MAX", Select: "content[].attachment.size"},
			},
		}},
	}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(compiled.Query, `"file__min_size": MIN(FLATTEN(`) || !strings.Contains(compiled.Query, `"file__max_size": MAX(FLATTEN(`) {
		t.Fatalf("MIN/MAX did not compile through typed aggregate reductions:\n%s", compiled.Query)
	}
}
