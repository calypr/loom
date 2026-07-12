package compilerfixture

import (
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe"
)

func TestPublicCompilerAcceptsExpandedSchemaRootExamples(t *testing.T) {
	for _, resourceType := range []string{
		"DiagnosticReport",
		"MedicationRequest",
		"MedicationStatement",
		"Procedure",
		"Task",
	} {
		t.Run(resourceType, func(t *testing.T) {
			compiled, err := dataframe.CompileRequest(dataframe.Builder{
				Project:          "compiler-oracle",
				RootResourceType: resourceType,
			}, 1)
			if err != nil {
				t.Fatalf("CompileRequest(%q): %v", resourceType, err)
			}
			assertOnlyValidatedRootCollection(t, compiled, resourceType)
		})
	}
}

func TestPublicCompilerRejectsSchemaBackboneAndAbstractRoots(t *testing.T) {
	for _, resourceType := range []string{"Address", "PatientContact", "Resource"} {
		t.Run(resourceType, func(t *testing.T) {
			compiled, err := dataframe.CompileRequest(dataframe.Builder{
				Project:          "compiler-oracle",
				RootResourceType: resourceType,
			}, 1)
			if err == nil || !strings.Contains(err.Error(), "not represented by the active generated FHIR schema") {
				t.Fatalf("CompileRequest(%q) error = %v, want generated-schema rejection", resourceType, err)
			}
			if compiled.Query != "" {
				t.Fatalf("CompileRequest(%q) rendered AQL for a rejected root:\n%s", resourceType, compiled.Query)
			}
		})
	}
}
