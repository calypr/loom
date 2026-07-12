package dataframe

import (
	"strings"
	"testing"
)

func TestValidateSemanticGraphAcceptsGeneratedAcyclicGraph(t *testing.T) {
	plan := semanticValidationPlan(SemanticNode{
		Alias: "specimen", ResourceType: "Specimen", EdgeLabel: "subject_Patient",
		Children: []SemanticNode{{
			Alias: "file", ResourceType: "DocumentReference", EdgeLabel: "subject_Specimen",
		}},
	})
	if err := ValidateSemanticGraph(plan); err != nil {
		t.Fatal(err)
	}
}

func TestValidateSemanticGraphRejectsDuplicateAlias(t *testing.T) {
	plan := semanticValidationPlan(
		SemanticNode{Alias: "related", ResourceType: "Specimen", EdgeLabel: "subject_Patient"},
		SemanticNode{Alias: "related", ResourceType: "Condition", EdgeLabel: "subject_Patient"},
	)
	assertSemanticValidationError(t, plan, "alias \"related\" is not unique")
}

func TestValidateSemanticGraphRejectsUnknownRootAndTraversal(t *testing.T) {
	unknownRoot := SemanticPlan{Root: SemanticNode{Alias: "root", ResourceType: "InventedResource"}}
	assertSemanticValidationError(t, unknownRoot, "root resource type \"InventedResource\"")

	unknownTraversal := semanticValidationPlan(SemanticNode{
		Alias: "specimen", ResourceType: "Specimen", EdgeLabel: "invented_edge",
	})
	assertSemanticValidationError(t, unknownTraversal, "is not represented by the active generated FHIR schema")
}

func TestValidateSemanticGraphRejectsSelfCycle(t *testing.T) {
	plan := SemanticPlan{Root: SemanticNode{
		Alias: "root", ResourceType: "Patient",
		Children: []SemanticNode{{
			Alias: "linked", ResourceType: "Patient", EdgeLabel: "link_other_Patient",
			Children: []SemanticNode{{Alias: "linked_again", ResourceType: "Patient", EdgeLabel: "link_other_Patient"}},
		}},
	}}
	assertSemanticValidationError(t, plan, "cycle detected")
}

func TestValidateSemanticGraphAllowsOneHopSelfReference(t *testing.T) {
	plan := SemanticPlan{Root: SemanticNode{
		Alias: "root", ResourceType: "Patient",
		Children: []SemanticNode{{Alias: "linked", ResourceType: "Patient", EdgeLabel: "link_other_Patient"}},
	}}
	if err := ValidateSemanticGraph(plan); err != nil {
		t.Fatalf("one-hop self reference should remain a valid finite query: %v", err)
	}
}

func TestValidateSemanticGraphEnforcesDepthCap(t *testing.T) {
	plan := SemanticPlan{Root: SemanticNode{
		Alias: "root", ResourceType: "Patient",
		Children: []SemanticNode{{
			Alias: "specimen", ResourceType: "Specimen", EdgeLabel: "subject_Patient",
			Children: []SemanticNode{{
				Alias: "file", ResourceType: "DocumentReference", EdgeLabel: "subject_Specimen",
				Children: []SemanticNode{{
					Alias: "procedure", ResourceType: "Procedure", EdgeLabel: "report_DocumentReference",
					Children: []SemanticNode{{
						Alias: "another_specimen", ResourceType: "Specimen", EdgeLabel: "collection_procedure",
						Children: []SemanticNode{{
							Alias: "too_deep", ResourceType: "Practitioner", EdgeLabel: "irrelevant_at_depth_guard",
						}},
					}},
				}},
			}},
		}},
	}}
	assertSemanticValidationError(t, plan, "exceeds maximum 4")
}

func TestValidateSemanticGraphRejectsMalformedRoot(t *testing.T) {
	assertSemanticValidationError(t, SemanticPlan{Root: SemanticNode{Alias: "patient", ResourceType: "Patient"}}, "root alias must be")
	assertSemanticValidationError(t, SemanticPlan{Root: SemanticNode{Alias: "root", ResourceType: "Patient", EdgeLabel: "subject"}}, "must not declare edge label")
}

func semanticValidationPlan(children ...SemanticNode) SemanticPlan {
	return SemanticPlan{Root: SemanticNode{Alias: "root", ResourceType: "Patient", Children: children}}
}

func assertSemanticValidationError(t *testing.T, plan SemanticPlan, contains string) {
	t.Helper()
	err := ValidateSemanticGraph(plan)
	if err == nil || !strings.Contains(err.Error(), contains) {
		t.Fatalf("error = %v, want substring %q", err, contains)
	}
}
