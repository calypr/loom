package dataframe

import (
	"reflect"
	"strings"
	"testing"
)

func TestRenderPhysicalPlanGenericNavigation(t *testing.T) {
	plan, err := BuildGenericPhysicalPlan(SemanticPlan{
		Version:           1,
		Project:           "project-1",
		AuthResourcePaths: []string{"/programs/p1"},
		Root: SemanticNode{
			Alias:        "root",
			ResourceType: "Patient",
			Children: []SemanticNode{{
				Alias:        "specimen",
				ResourceType: "Specimen",
				EdgeLabel:    "subject_Patient",
			}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	rendered, err := RenderPhysicalPlan(plan)
	if err != nil {
		t.Fatalf("RenderPhysicalPlan() error = %v", err)
	}
	for _, want := range []string{
		"FOR root IN @@root_collection",
		"  FILTER root.project == @project",
		"  LET root_scope_allowed = @auth_resource_paths_unrestricted == true OR root.auth_resource_path IN @auth_resource_paths",
		"  FILTER root_scope_allowed == @scope_allowed",
		"  LET __loom_physical_set_1 = (",
		"    FOR node_1, edge_1 IN 1..1 INBOUND root @@traversal_1_edge_collection",
		"      FILTER edge_1.label == @traversal_1_label",
		"      FILTER edge_1.from_type == @traversal_1_target_type",
		"      FILTER node_1.resourceType == @traversal_1_target_type",
		"      FILTER edge_1.project == @project",
		"      FILTER node_1.project == @project",
		"      LET traversal_1_scope_allowed = @auth_resource_paths_unrestricted == true OR (edge_1.auth_resource_path IN @auth_resource_paths AND node_1.auth_resource_path IN @auth_resource_paths)",
		"      FILTER traversal_1_scope_allowed == @scope_allowed",
		"      RETURN node_1",
		"RETURN { [@__loom_physical_projection_0_name]: root._key }",
	} {
		if !strings.Contains(rendered.Query, want) {
			t.Fatalf("rendered query missing %q:\n%s", want, rendered.Query)
		}
	}
	if got := rendered.BindVars["@root_collection"]; got != "Patient" {
		t.Fatalf("root collection bind = %#v", got)
	}
	if got := rendered.BindVars["@traversal_1_edge_collection"]; got != "fhir_edge" {
		t.Fatalf("edge collection bind = %#v", got)
	}
	if _, present := rendered.BindVars["root_collection"]; present {
		t.Fatalf("runtime binds retained unprefixed root collection: %#v", rendered.BindVars)
	}
	if _, present := rendered.BindVars["traversal_1_edge_collection"]; present {
		t.Fatalf("runtime binds retained unprefixed edge collection: %#v", rendered.BindVars)
	}
	if got := rendered.BindVars["project"]; got != "project-1" {
		t.Fatalf("normal bind was not retained: %#v", got)
	}
	if got := rendered.BindVars["__loom_physical_projection_0_name"]; got != "_key" {
		t.Fatalf("projection name was not bound: %#v", got)
	}
}

func TestRenderPhysicalPlanTraversalSetsPreserveRootRowGrain(t *testing.T) {
	plan, err := BuildGenericPhysicalPlan(SemanticPlan{
		Version:           1,
		Project:           "project-1",
		AuthResourcePaths: []string{"/programs/p1"},
		Root: SemanticNode{
			Alias: "root", ResourceType: "Patient",
			Children: []SemanticNode{{
				Alias: "specimen", ResourceType: "Specimen", EdgeLabel: "subject_Patient",
				Children: []SemanticNode{{
					Alias: "file", ResourceType: "DocumentReference", EdgeLabel: "subject_Specimen",
				}},
			}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := RenderPhysicalPlan(plan)
	if err != nil {
		t.Fatal(err)
	}

	setOne := strings.Index(rendered.Query, "\n  LET __loom_physical_set_1 = (")
	firstTraversal := strings.Index(rendered.Query, "\n    FOR node_1, edge_1 IN 1..1 INBOUND root @@traversal_1_edge_collection")
	setTwo := strings.Index(rendered.Query, "\n  LET __loom_physical_set_2 = (")
	parentLoop := strings.Index(rendered.Query, "\n    FOR __loom_physical_parent_2 IN __loom_physical_set_1")
	secondTraversal := strings.Index(rendered.Query, "\n      FOR node_2, edge_2 IN 1..1 INBOUND __loom_physical_parent_2 @@traversal_2_edge_collection")
	outerReturn := strings.LastIndex(rendered.Query, "\nRETURN { [@__loom_physical_projection_0_name]: root._key }")
	if setOne < 0 || firstTraversal < setOne || setTwo < firstTraversal || parentLoop < setTwo || secondTraversal < parentLoop || outerReturn < secondTraversal {
		t.Fatalf("nested traversal sets did not preserve outer root shape:\n%s", rendered.Query)
	}
	if strings.Contains(rendered.Query, "\nFOR node_1") || strings.Contains(rendered.Query, "\nFOR node_2") {
		t.Fatalf("a traversal escaped its LET subquery and can multiply root rows:\n%s", rendered.Query)
	}
	if strings.Count(rendered.Query, "RETURN { [@__loom_physical_projection_0_name]: root._key }") != 1 {
		t.Fatalf("expected exactly one outer root RETURN:\n%s", rendered.Query)
	}
}

func TestRenderPhysicalPlanIsDeterministicAndCopiesBindVars(t *testing.T) {
	plan, err := BuildGenericPhysicalPlan(SemanticPlan{
		Version:           1,
		Project:           "project-1",
		AuthResourcePaths: []string{"/programs/p1"},
		Root:              SemanticNode{Alias: "root", ResourceType: "Patient"},
	})
	if err != nil {
		t.Fatal(err)
	}
	first, err := RenderPhysicalPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	second, err := RenderPhysicalPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	if first.Query != second.Query || !reflect.DeepEqual(first.BindVars, second.BindVars) {
		t.Fatalf("renders are not deterministic:\nfirst=%#v\nsecond=%#v", first, second)
	}
	if strings.Contains(first.Query, "LET __loom_physical_set_") {
		t.Fatalf("root-only navigation should not require a child traversal set:\n%s", first.Query)
	}

	first.BindVars["project"] = "changed"
	first.BindVars["auth_resource_paths"].([]string)[0] = "/changed"
	if plan.BindVars["project"] != "project-1" {
		t.Fatalf("runtime bind map mutated plan: %#v", plan.BindVars)
	}
	if got := plan.BindVars["auth_resource_paths"].([]string)[0]; got != "/programs/p1" {
		t.Fatalf("runtime bind slice mutated plan: %#v", plan.BindVars)
	}
}

func TestRenderPhysicalPlanRejectsUnsupportedOrAmbiguousOperations(t *testing.T) {
	newPlan := func(t *testing.T) PhysicalPlan {
		t.Helper()
		plan, err := BuildGenericPhysicalPlan(SemanticPlan{
			Version: 1,
			Project: "project-1",
			Root:    SemanticNode{Alias: "root", ResourceType: "Patient"},
		})
		if err != nil {
			t.Fatal(err)
		}
		return plan
	}

	tests := []struct {
		name   string
		mutate func(*PhysicalPlan)
		want   string
	}{
		{
			name: "invalid physical plan",
			mutate: func(plan *PhysicalPlan) {
				plan.Version = 0
			},
			want: "validate physical plan",
		},
		{
			name: "unsupported filter operator",
			mutate: func(plan *PhysicalPlan) {
				returnIndex := len(plan.Operations) - 1
				unsupported := PhysicalOperation{
					Kind: PhysicalFilterOp,
					Filter: &PhysicalFilter{Predicate: PhysicalPredicate{
						Operator: "NOT_EQUALS",
						Left:     PhysicalValue{Variable: "root", Path: []string{"_key"}},
						Right:    &PhysicalValue{BindKey: "project"},
					}},
				}
				plan.Operations = append(plan.Operations[:returnIndex], append([]PhysicalOperation{unsupported}, plan.Operations[returnIndex:]...)...)
			},
			want: "unsupported physical filter operator",
		},
		{
			name: "unsupported derived operator",
			mutate: func(plan *PhysicalPlan) {
				returnIndex := len(plan.Operations) - 1
				unsupported := PhysicalOperation{
					Kind:       PhysicalDerivedLetOp,
					DerivedLet: &PhysicalDerivedLet{Variable: "unsupported_value", Operator: "LENGTH", Inputs: []PhysicalValue{{Variable: "root"}}},
				}
				plan.Operations = append(plan.Operations[:returnIndex], append([]PhysicalOperation{unsupported}, plan.Operations[returnIndex:]...)...)
			},
			want: "unsupported physical derived LET operator",
		},
		{
			name: "collection key used as scalar bind",
			mutate: func(plan *PhysicalPlan) {
				value := PhysicalValue{BindKey: "root_collection"}
				plan.Operations[1].Filter.Predicate.Right = &value
			},
			want: "both a collection and scalar bind",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan := newPlan(t)
			test.mutate(&plan)
			_, err := RenderPhysicalPlan(plan)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("RenderPhysicalPlan() error = %v; want substring %q", err, test.want)
			}
		})
	}
}

func TestRenderPhysicalPlanRejectsMissingGenericScope(t *testing.T) {
	plan, err := BuildGenericPhysicalPlan(SemanticPlan{
		Version: 1,
		Project: "project-1",
		Root:    SemanticNode{Alias: "root", ResourceType: "Patient"},
	})
	if err != nil {
		t.Fatal(err)
	}
	plan.Operations = append(plan.Operations[:1], plan.Operations[2:]...)

	_, err = RenderPhysicalPlan(plan)
	if err == nil || !strings.Contains(err.Error(), "generic physical plan scope") {
		t.Fatalf("RenderPhysicalPlan() error = %v, want scope validation failure", err)
	}
}

func TestRenderPhysicalPlanRejectsMisboundGenericEdgeTypeDiscriminator(t *testing.T) {
	plan, err := BuildGenericPhysicalPlan(SemanticPlan{
		Version: 1,
		Project: "project-1",
		Root: SemanticNode{
			Alias: "root", ResourceType: "Patient",
			Children: []SemanticNode{{
				Alias: "specimen", ResourceType: "Specimen", EdgeLabel: "subject_Patient",
			}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for index := range plan.Operations {
		if plan.Operations[index].Traversal != nil {
			plan.Operations[index].Traversal.EdgeTargetTypeField = "to_type"
			break
		}
	}
	_, err = RenderPhysicalPlan(plan)
	if err == nil || !strings.Contains(err.Error(), "must constrain edge.from_type") {
		t.Fatalf("RenderPhysicalPlan() error = %v, want inbound edge discriminator rejection", err)
	}
}

func TestRenderPhysicalPlanKeepsCollectionAndProjectionValuesOutOfAQL(t *testing.T) {
	plan, err := BuildGenericPhysicalPlan(SemanticPlan{
		Version: 1,
		Project: "project-1",
		Root:    SemanticNode{Alias: "root", ResourceType: "Patient"},
	})
	if err != nil {
		t.Fatal(err)
	}
	maliciousCollection := "Patient; RETURN {injected: true}"
	maliciousProjection := "x]: true } RETURN {injected: true} //"
	plan.BindVars["root_collection"] = maliciousCollection
	for index := range plan.Operations {
		if plan.Operations[index].Return != nil {
			plan.Operations[index].Return.Projections[0].Name = maliciousProjection
		}
	}

	rendered, err := RenderPhysicalPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rendered.Query, maliciousCollection) || strings.Contains(rendered.Query, maliciousProjection) {
		t.Fatalf("data value was interpolated into AQL:\n%s", rendered.Query)
	}
	if got := rendered.BindVars["@root_collection"]; got != maliciousCollection {
		t.Fatalf("collection value was not carried as a collection bind: %#v", got)
	}
	if got := rendered.BindVars["__loom_physical_projection_0_name"]; got != maliciousProjection {
		t.Fatalf("projection name was not carried as a scalar bind: %#v", got)
	}
}
