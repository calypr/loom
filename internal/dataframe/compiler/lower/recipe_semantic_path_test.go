package lower

import (
	"testing"

	"github.com/calypr/loom/internal/dataframe/expression"
)

func TestRecipeSemanticPathUsesProvenanceAndNotPhysicalNames(t *testing.T) {
	if got := recipeSemanticPath("Patient", "Patient", "Patient.case_id", expression.Expression{}); got != "Patient.case_id" {
		t.Fatalf("explicit semantic path = %q", got)
	}
	expr := expression.Expression{Selector: &expression.SelectorRef{Path: "identifier[].value"}}
	if got := recipeSemanticPath("Patient", "Patient", "", expr); got != "Patient.identifier[].value" {
		t.Fatalf("selector semantic path = %q", got)
	}
	if got := recipeSemanticPath("ResearchSubject", "Patient", "", expr); got != "Patient.identifier[].value" {
		t.Fatalf("nested semantic path = %q", got)
	}
}
