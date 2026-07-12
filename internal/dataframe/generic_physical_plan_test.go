package dataframe

import (
	"strings"
	"testing"
)

func TestBuildGenericPhysicalPlanNavigationSkeleton(t *testing.T) {
	semantic := SemanticPlan{
		Version:           1,
		Project:           "project-1",
		AuthResourcePaths: []string{"/programs/p1"},
		Root: SemanticNode{
			Alias: "root", ResourceType: "Patient",
			Children: []SemanticNode{{
				Alias: "specimen", ResourceType: "Specimen", EdgeLabel: "subject_Patient",
				Children: []SemanticNode{{Alias: "file", ResourceType: "DocumentReference", EdgeLabel: "subject_Specimen"}},
			}},
		},
	}
	plan, err := BuildGenericPhysicalPlan(semantic)
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.Validate(); err != nil {
		t.Fatalf("built plan does not validate: %v", err)
	}
	if plan.BindVars["root_collection"] != "Patient" || plan.BindVars["traversal_1_edge_collection"] != "fhir_edge" || plan.BindVars["traversal_1_label"] != "subject_Patient" || plan.BindVars["traversal_1_target_type"] != "Specimen" {
		t.Fatalf("unexpected bound physical values: %#v", plan.BindVars)
	}
	if plan.BindVars["auth_resource_paths_unrestricted"] != false {
		t.Fatalf("unexpected auth mode: %#v", plan.BindVars)
	}

	traversals := 0
	authPredicates := 0
	for _, operation := range plan.Operations {
		if operation.Traversal != nil {
			traversals++
			if operation.Traversal.Direction != PhysicalInbound {
				t.Fatalf("generic traversal direction = %q", operation.Traversal.Direction)
			}
			if operation.Traversal.EdgeTargetTypeField != "from_type" {
				t.Fatalf("generic inbound traversal edge discriminator = %q", operation.Traversal.EdgeTargetTypeField)
			}
			if operation.Source.SemanticNode == "" || operation.Source.Relationship == "" {
				t.Fatalf("traversal lost provenance: %#v", operation.Source)
			}
		}
		if operation.DerivedLet != nil && operation.DerivedLet.Operator == "AUTH_RESOURCE_PATH_ALLOWED" {
			authPredicates++
		}
	}
	if traversals != 2 || authPredicates != 3 {
		t.Fatalf("traversals=%d auth predicates=%d operations=%#v", traversals, authPredicates, plan.Operations)
	}
	last := plan.Operations[len(plan.Operations)-1]
	if last.Kind != PhysicalReturnOp || len(last.Return.Projections) != 1 || last.Return.Projections[0].Name != "_key" {
		t.Fatalf("unexpected terminal return: %#v", last)
	}
}

func TestBuildGenericPhysicalPlanEmptyAuthScopeIsExplicit(t *testing.T) {
	plan, err := BuildGenericPhysicalPlan(SemanticPlan{Version: 1, Project: "p", Root: SemanticNode{Alias: "root", ResourceType: "Observation"}})
	if err != nil {
		t.Fatal(err)
	}
	if plan.BindVars["auth_resource_paths_unrestricted"] != true {
		t.Fatalf("empty auth paths were not represented as unrestricted: %#v", plan.BindVars)
	}
}

func TestBuildGenericPhysicalPlanRejectsUnsupportedSemanticFeatures(t *testing.T) {
	tests := []struct {
		name string
		root SemanticNode
		want string
	}{
		{"unknown root", SemanticNode{Alias: "root", ResourceType: "Unknown"}, "not represented"},
		{"root field", SemanticNode{Alias: "root", ResourceType: "Patient", Fields: []SemanticField{{Name: "gender"}}}, "not supported"},
		{"child filter", SemanticNode{Alias: "root", ResourceType: "Patient", Children: []SemanticNode{{Alias: "specimen", ResourceType: "Specimen", EdgeLabel: "subject_Patient", Filters: []TypedFilter{{FieldRef: "status"}}}}}, "not supported"},
		{"unknown traversal", SemanticNode{Alias: "root", ResourceType: "Patient", Children: []SemanticNode{{Alias: "medication", ResourceType: "Medication", EdgeLabel: "missing"}}}, "not represented"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := BuildGenericPhysicalPlan(SemanticPlan{Version: 1, Project: "p", Root: test.root})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v; want substring %q", err, test.want)
			}
		})
	}
}
