package dataframe

import (
	"strings"
	"testing"

	"github.com/calypr/loom/internal/fhirschema"
)

func TestLowerGraphQLBuilderFallsBackToGenericRootOnlyPlan(t *testing.T) {
	planned, err := lowerGraphQLBuilder(Builder{
		Project:          "P1",
		RootResourceType: "Patient",
		Fields:           []FieldSelect{{Name: "gender", Select: "gender"}},
	})
	if err != nil {
		t.Fatalf("lowerGraphQLBuilder() error = %v", err)
	}
	if planned.PlanHint == nil || planned.PlanHint.Profile != "generic_fhir_graph" {
		t.Fatalf("expected generic plan hint, got %#v", planned.PlanHint)
	}
	compiled, err := Compile(planned, 25)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	if !strings.Contains(compiled.Query, "FOR root IN Patient") || !strings.Contains(compiled.Query, "root.payload.gender") {
		t.Fatalf("unexpected generic root query:\n%s", compiled.Query)
	}
}

func TestLowerGraphQLBuilderFallsBackToGenericNonPatientTraversal(t *testing.T) {
	planned, err := lowerGraphQLBuilder(Builder{
		Project:          "P1",
		RootResourceType: "Specimen",
		Fields:           []FieldSelect{{Name: "id", Select: "id"}},
		Traversals: []TraversalStep{{
			Label:          "subject_Specimen",
			ToResourceType: "DocumentReference",
			Alias:          "file",
			Fields:         []FieldSelect{{Name: "file_name", Select: "content[].attachment.title"}},
		}},
	})
	if err != nil {
		t.Fatalf("lowerGraphQLBuilder() error = %v", err)
	}
	if planned.PlanHint == nil || planned.PlanHint.Profile != "generic_fhir_graph" {
		t.Fatalf("expected generic plan hint, got %#v", planned.PlanHint)
	}
	if len(planned.Sets) != 1 || planned.Sets[0].Direction != "INBOUND" || planned.Sets[0].Source != "" {
		t.Fatalf("unexpected generic set: %#v", planned.Sets)
	}
	compiled, err := Compile(planned, 25)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	if !strings.Contains(compiled.Query, "1..1 INBOUND root fhir_edge") || !strings.Contains(compiled.Query, "generic_file_set") {
		t.Fatalf("unexpected generic traversal query:\n%s", compiled.Query)
	}
}

func TestGenericLoweringUsesInboundForNestedSchemaTraversal(t *testing.T) {
	planned, err := lowerGraphQLBuilder(Builder{
		Project:          "P1",
		RootResourceType: "Patient",
		Traversals: []TraversalStep{{
			Label:          "subject_Patient",
			ToResourceType: "Specimen",
			Alias:          "specimen",
			Traversals: []TraversalStep{{
				Label:          "subject_Specimen",
				ToResourceType: "DocumentReference",
				Alias:          "file",
				Fields:         []FieldSelect{{Name: "id", Select: "id"}},
			}},
		}},
	})
	if err != nil {
		t.Fatalf("lowerGraphQLBuilder() error = %v", err)
	}
	if planned.PlanHint == nil || planned.PlanHint.Profile != "generic_fhir_graph" {
		t.Fatalf("ordinary Patient navigation must use generic lowering, got %#v", planned.PlanHint)
	}
	compiled, err := Compile(planned, 10)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(compiled.Query, " INBOUND ") != 2 {
		t.Fatalf("expected two generic INBOUND traversals, got:\n%s", compiled.Query)
	}
}

func TestGenericLoweringCompilesEveryGeneratedRootType(t *testing.T) {
	for _, resourceType := range fhirschema.ResourceTypes() {
		t.Run(resourceType, func(t *testing.T) {
			planned, err := lowerGraphQLBuilder(Builder{Project: "P1", RootResourceType: resourceType})
			if err != nil {
				t.Fatalf("lowerGraphQLBuilder() error = %v", err)
			}
			if planned.PlanHint == nil || planned.PlanHint.Profile != "generic_fhir_graph" {
				t.Fatalf("expected generic plan, got %#v", planned.PlanHint)
			}
			compiled, err := Compile(planned, 1)
			if err != nil {
				t.Fatalf("Compile() error = %v", err)
			}
			if !strings.Contains(compiled.Query, "FOR root IN "+resourceType) {
				t.Fatalf("query does not use expected root collection:\n%s", compiled.Query)
			}
		})
	}
}
