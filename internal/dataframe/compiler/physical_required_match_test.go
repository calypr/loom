package compiler

import (
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
	"github.com/calypr/loom/internal/dataframe/semantic"
	"github.com/calypr/loom/internal/dataframe/spec"
)

func TestRenderPhysicalPlanRequiredInboundTraversalMatch(t *testing.T) {
	plan, err := buildGenericPhysicalPlan(semantic.SemanticPlan{
		Version: 1, Project: "project-1", AuthResourcePaths: []string{"/programs/p1"},
		Root: semantic.SemanticNode{Alias: "root", ResourceType: "Patient", Children: []semantic.SemanticNode{{
			Alias: "condition", ResourceType: "Condition", EdgeLabel: "subject_Patient", MatchMode: spec.TraversalMatchRequired,
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := aql.RenderPhysicalPlan(plan)
	if err != nil {
		t.Fatalf("RenderPhysicalPlan() error = %v", err)
	}
	for _, want := range []string{
		"FILTER LENGTH((",
		"FOR required_0_node_0, required_0_edge_0 IN 1..1 INBOUND root @@required_0_0_edge_collection",
		"required_0_edge_0.label == @required_0_0_label",
		"required_0_edge_0.from_type == @required_0_0_target_type",
		"required_0_node_0.resourceType == @required_0_0_target_type",
		"required_0_edge_0.project == @project",
		"required_0_node_0.project == @project",
		"required_0_edge_0.dataset_generation == @dataset_generation",
		"required_0_node_0.dataset_generation == @dataset_generation",
		"required_0_edge_0.auth_resource_path IN @auth_resource_paths",
		"required_0_node_0.auth_resource_path IN @auth_resource_paths",
		"LIMIT 1",
	} {
		if !strings.Contains(rendered.Query, want) {
			t.Fatalf("rendered required match missing %q:\n%s", want, rendered.Query)
		}
	}
	if got := rendered.BindVars["@required_0_0_edge_collection"]; got != "fhir_edge" {
		t.Fatalf("required traversal edge collection bind = %#v", got)
	}
}

func TestRenderPhysicalPlanRequiredOutboundResearchStudyMatch(t *testing.T) {
	plan, err := buildGenericPhysicalPlan(semantic.SemanticPlan{
		Version: 1, Project: "project-1",
		Root: semantic.SemanticNode{Alias: "root", ResourceType: "ResearchSubject", Children: []semantic.SemanticNode{{
			Alias: "study", ResourceType: "ResearchStudy", EdgeLabel: "study", MatchMode: spec.TraversalMatchRequired,
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := aql.RenderPhysicalPlan(plan)
	if err != nil {
		t.Fatalf("RenderPhysicalPlan() error = %v", err)
	}
	for _, want := range []string{
		"FOR required_0_node_0, required_0_edge_0 IN 1..1 OUTBOUND root @@required_0_0_edge_collection",
		"required_0_edge_0.to_type == @required_0_0_target_type",
		"required_0_node_0.resourceType == @required_0_0_target_type",
		"LIMIT 1",
	} {
		if !strings.Contains(rendered.Query, want) {
			t.Fatalf("rendered outbound required match missing %q:\n%s", want, rendered.Query)
		}
	}
}

func TestBuildPhysicalPlanRequiredTraversalWithFilterUsesTypedPredicate(t *testing.T) {
	plan, err := buildGenericPhysicalPlan(semantic.SemanticPlan{
		Version: 1, Project: "project-1",
		Root: semantic.SemanticNode{Alias: "root", ResourceType: "Patient", Children: []semantic.SemanticNode{{
			Alias: "condition", ResourceType: "Condition", EdgeLabel: "subject_Patient", MatchMode: spec.TraversalMatchRequired,
			Filters: []spec.TypedFilter{{FieldRef: "Condition.id", Selector: "id", FieldKind: spec.FilterString, Operator: spec.FilterExists}},
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := aql.RenderPhysicalPlan(plan)
	if err != nil || !strings.Contains(rendered.Query, "required_0_0") {
		t.Fatalf("required filter did not render physically: err=%v query=%s", err, rendered.Query)
	}
}
