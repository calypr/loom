package dataframe

import (
	"strings"
	"testing"
)

func TestRootValueModesCompileToDistinctShapes(t *testing.T) {
	for mode, want := range map[string]string{
		"FIRST":    "FIRST(",
		"ALL":      "FOR __root",
		"DISTINCT": "UNIQUE(",
	} {
		t.Run(mode, func(t *testing.T) {
			compiled, err := CompileRequest(Builder{
				Project:          "P1",
				RootResourceType: "Patient",
				Fields: []FieldSelect{{
					Name: "identifier", Select: "identifier[].value", ValueMode: mode,
				}},
			}, 1)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(compiled.Query, want) {
				t.Fatalf("%s query missing %q:\n%s", mode, want, compiled.Query)
			}
		})
	}
}

func TestGenericTraversalValueModesLowerToPhysicalOperations(t *testing.T) {
	for mode, wantOperation := range map[string]string{
		"FIRST":    DerivedOpFirstNonNull,
		"ALL":      DerivedOpAll,
		"DISTINCT": DerivedOpUnique,
		"AUTO":     DerivedOpFirstNonNull,
	} {
		t.Run(mode, func(t *testing.T) {
			planned, err := lowerGenericGraphQLBuilder(Builder{}, buildLogicalRequest(Builder{
				Project: "P1", RootResourceType: "Patient",
				Traversals: []TraversalStep{{
					Label: "subject_Patient", ToResourceType: "Condition", Alias: "condition",
					Fields: []FieldSelect{{Name: "code", Select: "code.coding[].display", ValueMode: mode}},
				}},
			}))
			if err != nil {
				t.Fatal(err)
			}
			if len(planned.DerivedFields) != 1 || planned.DerivedFields[0].Operation != wantOperation {
				t.Fatalf("%s derived fields = %#v, want %s", mode, planned.DerivedFields, wantOperation)
			}
		})
	}
}

func TestGenericFirstLikeTraversalSelectionSortsSourceSetByStableKey(t *testing.T) {
	for _, mode := range []string{"FIRST", "AUTO"} {
		t.Run(mode, func(t *testing.T) {
			planned, err := Lower(Builder{
				Project: "P1", RootResourceType: "Specimen",
				Traversals: []TraversalStep{{
					Label: "subject_Specimen", ToResourceType: "DocumentReference", Alias: "file",
					Fields: []FieldSelect{{Name: "title", Select: "content[].attachment.title", ValueMode: mode}},
				}},
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(planned.Sets) != 1 || planned.Sets[0].SortField != "_key" {
				t.Fatalf("%s traversal did not request a stable source order: %#v", mode, planned.Sets)
			}
			compiled, err := Compile(planned, 1)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(compiled.Query, "SORT __item._key") {
				t.Fatalf("%s traversal query does not sort its source set:\n%s", mode, compiled.Query)
			}
		})
	}
}
