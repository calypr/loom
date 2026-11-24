package compiler

import (
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/recipeplan"
)

func TestValidateSemanticGraphAcceptsGeneratedAcyclicGraph(t *testing.T) {
	plan := semanticValidationPlan(recipeplan.SemanticNode{
		Alias: "specimen", ResourceType: "Specimen", EdgeLabel: "subject_Patient",
		Children: []recipeplan.SemanticNode{{
			Alias: "file", ResourceType: "DocumentReference", EdgeLabel: "subject_Specimen",
		}},
	})
	if err := recipeplan.ValidateSemanticGraph(plan.Root); err != nil {
		t.Fatal(err)
	}
}

func TestValidateSemanticGraphRejectsDuplicateAlias(t *testing.T) {
	plan := semanticValidationPlan(
		recipeplan.SemanticNode{Alias: "related", ResourceType: "Specimen", EdgeLabel: "subject_Patient"},
		recipeplan.SemanticNode{Alias: "related", ResourceType: "Condition", EdgeLabel: "subject_Patient"},
	)
	assertSemanticValidationError(t, plan, "alias \"related\" is not unique")
}

func TestValidateSemanticGraphRejectsUnknownRootAndTraversal(t *testing.T) {
	unknownRoot := recipeplan.OutputPlan{Root: recipeplan.SemanticNode{Alias: "root", ResourceType: "InventedResource"}}
	assertSemanticValidationError(t, unknownRoot, "root resource type \"InventedResource\"")

	unknownTraversal := semanticValidationPlan(recipeplan.SemanticNode{
		Alias: "specimen", ResourceType: "Specimen", EdgeLabel: "invented_edge",
	})
	assertSemanticValidationError(t, unknownTraversal, "is not represented by the active generated FHIR schema")
}

func TestValidateSemanticGraphAcceptsRepeatedSelfReference(t *testing.T) {
	plan := recipeplan.OutputPlan{Root: recipeplan.SemanticNode{
		Alias: "root", ResourceType: "Patient",
		Children: []recipeplan.SemanticNode{{
			Alias: "linked", ResourceType: "Patient", EdgeLabel: "link_other_Patient",
			Children: []recipeplan.SemanticNode{{Alias: "linked_again", ResourceType: "Patient", EdgeLabel: "link_other_Patient"}},
		}},
	}}
	if err := recipeplan.ValidateSemanticGraph(plan.Root); err != nil {
		t.Fatalf("repeated self-reference should remain a valid finite query: %v", err)
	}
}

func TestValidateSemanticGraphAllowsOneHopSelfReference(t *testing.T) {
	plan := recipeplan.OutputPlan{Root: recipeplan.SemanticNode{
		Alias: "root", ResourceType: "Patient",
		Children: []recipeplan.SemanticNode{{Alias: "linked", ResourceType: "Patient", EdgeLabel: "link_other_Patient"}},
	}}
	if err := recipeplan.ValidateSemanticGraph(plan.Root); err != nil {
		t.Fatalf("one-hop self reference should remain a valid finite query: %v", err)
	}
}

func TestValidateSemanticGraphAcceptsMoreThanFourHops(t *testing.T) {
	plan := recipeplan.OutputPlan{Root: recipeplan.SemanticNode{
		Alias: "root", ResourceType: "Patient",
		Children: []recipeplan.SemanticNode{{
			Alias: "specimen", ResourceType: "Specimen", EdgeLabel: "subject_Patient",
			Children: []recipeplan.SemanticNode{{
				Alias: "file", ResourceType: "DocumentReference", EdgeLabel: "subject_Specimen",
				Children: []recipeplan.SemanticNode{{
					Alias: "procedure", ResourceType: "Procedure", EdgeLabel: "report_DocumentReference",
					Children: []recipeplan.SemanticNode{{
						Alias: "another_specimen", ResourceType: "Specimen", EdgeLabel: "collection_procedure",
						Children: []recipeplan.SemanticNode{{
							Alias: "another_file", ResourceType: "DocumentReference", EdgeLabel: "subject_Specimen",
						}},
					}},
				}},
			}},
		}},
	}}
	if err := recipeplan.ValidateSemanticGraph(plan.Root); err != nil {
		t.Fatalf("finite route longer than four hops should remain valid: %v", err)
	}
}

func TestValidateSemanticGraphRejectsMalformedRoot(t *testing.T) {
	assertSemanticValidationError(t, recipeplan.OutputPlan{Root: recipeplan.SemanticNode{Alias: "patient", ResourceType: "Patient"}}, "root alias must be")
	assertSemanticValidationError(t, recipeplan.OutputPlan{Root: recipeplan.SemanticNode{Alias: "root", ResourceType: "Patient", EdgeLabel: "subject"}}, "must not declare edge label")
}

func semanticValidationPlan(children ...recipeplan.SemanticNode) recipeplan.OutputPlan {
	return recipeplan.OutputPlan{Root: recipeplan.SemanticNode{Alias: "root", ResourceType: "Patient", Children: children}}
}

func assertSemanticValidationError(t *testing.T, plan recipeplan.OutputPlan, contains string) {
	t.Helper()
	err := recipeplan.ValidateSemanticGraph(plan.Root)
	if err == nil || !strings.Contains(err.Error(), contains) {
		t.Fatalf("error = %v, want substring %q", err, contains)
	}
}
