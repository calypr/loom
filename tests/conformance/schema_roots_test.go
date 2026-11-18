package compilerfixture

import (
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
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
			compiled, err := compileRecipe(rootRecipe(resourceType), "compiler-oracle", 1, ir.DefaultPhysicalOptimizationPolicy())
			if err != nil {
				t.Fatalf("compile recipe root %q: %v", resourceType, err)
			}
			assertOnlyValidatedRootCollection(t, compiled, resourceType)
		})
	}
}

func TestPublicCompilerRejectsSchemaBackboneAndAbstractRoots(t *testing.T) {
	for _, resourceType := range []string{"Address", "PatientContact", "Resource"} {
		t.Run(resourceType, func(t *testing.T) {
			compiled, err := compileRecipe(rootRecipe(resourceType), "compiler-oracle", 1, ir.DefaultPhysicalOptimizationPolicy())
			if err == nil || !strings.Contains(err.Error(), "not represented by the active generated FHIR schema") {
				t.Fatalf("compile recipe root %q error = %v, want generated-schema rejection", resourceType, err)
			}
			if compiled.Query != "" {
				t.Fatalf("compile recipe root %q rendered AQL for a rejected root:\n%s", resourceType, compiled.Query)
			}
		})
	}
}
