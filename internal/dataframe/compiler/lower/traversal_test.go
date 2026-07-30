package lower

import (
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

func TestBuildPhysicalTraversalUsesSchemaDerivedInboundRoute(t *testing.T) {
	policy := ir.DefaultPhysicalOptimizationPolicy()
	got, err := BuildPhysicalTraversal(TraversalLoweringRequest{
		FromType:       "Patient",
		EdgeLabel:      "subject_Patient",
		ToType:         "Specimen",
		SourceVariable: "root",
		TargetVariable: "child_node",
		EdgeVariable:   "child_edge",
		BindPrefix:     "child_set_1",
		Policy:         policy,
	})
	if err != nil {
		t.Fatalf("BuildPhysicalTraversal() error = %v", err)
	}
	if got.Route.Direction != ir.PhysicalInbound {
		t.Fatalf("route direction = %q, want %q", got.Route.Direction, ir.PhysicalInbound)
	}
	if got.Traversal.Direction != ir.PhysicalInbound || got.Traversal.EdgeTargetTypeField != "from_type" {
		t.Fatalf("traversal route contract = %#v", got.Traversal)
	}
	if got.Traversal.Strategy != ir.PhysicalTraversalEndpointLookup {
		t.Fatalf("traversal strategy = %q, want endpoint lookup", got.Traversal.Strategy)
	}
	if got.Traversal.EndpointField != "_to" || got.Traversal.EndpointJoinField != "_from" {
		t.Fatalf("endpoint contract = %#v", got.Traversal)
	}
	wantBinds := map[string]any{
		"child_set_1_label":           "subject_Patient",
		"child_set_1_target_type":     "Specimen",
		"child_set_1_edge_collection": "fhir_edge",
	}
	for key, want := range wantBinds {
		if got.BindVars[key] != want {
			t.Fatalf("bind %q = %#v, want %#v", key, got.BindVars[key], want)
		}
	}
}

func TestBuildPhysicalTraversalUsesSchemaDerivedOutboundRoute(t *testing.T) {
	got, err := BuildPhysicalTraversal(TraversalLoweringRequest{
		FromType:       "ResearchSubject",
		EdgeLabel:      "study",
		ToType:         "ResearchStudy",
		SourceVariable: "root",
		TargetVariable: "study_node",
		EdgeVariable:   "study_edge",
		BindPrefix:     "study_1",
		Policy:         ir.DefaultPhysicalOptimizationPolicy(),
	})
	if err != nil {
		t.Fatalf("BuildPhysicalTraversal() error = %v", err)
	}
	if got.Route.Direction != ir.PhysicalOutbound || got.Traversal.Direction != ir.PhysicalOutbound {
		t.Fatalf("outbound route contract = route %#v traversal %#v", got.Route, got.Traversal)
	}
	if got.Traversal.EdgeTargetTypeField != "to_type" {
		t.Fatalf("target discriminator = %q, want to_type", got.Traversal.EdgeTargetTypeField)
	}
	if got.Traversal.EndpointField != "_from" || got.Traversal.EndpointJoinField != "_to" {
		t.Fatalf("outbound endpoint contract = %#v", got.Traversal)
	}
}

func TestBuildPhysicalTraversalUsesGeneratedOutboundReferenceForAnyFHIRType(t *testing.T) {
	got, err := BuildPhysicalTraversal(TraversalLoweringRequest{
		FromType: "ResearchSubject", EdgeLabel: "subject_Patient", ToType: "Patient",
		SourceVariable: "root", TargetVariable: "patient_node", EdgeVariable: "patient_edge",
		BindPrefix: "subject_1", Policy: ir.DefaultPhysicalOptimizationPolicy(),
	})
	if err != nil {
		t.Fatalf("BuildPhysicalTraversal() error = %v", err)
	}
	if got.Route.Direction != ir.PhysicalOutbound || got.Traversal.Direction != ir.PhysicalOutbound {
		t.Fatalf("generated outbound route contract = route %#v traversal %#v", got.Route, got.Traversal)
	}
	if got.Traversal.EndpointField != "_from" || got.Traversal.EndpointJoinField != "_to" {
		t.Fatalf("generated outbound endpoint contract = %#v", got.Traversal)
	}
}

func TestBuildPhysicalTraversalPolicyCanForceNativeRoute(t *testing.T) {
	policy := ir.DefaultPhysicalOptimizationPolicy().WithRule(ir.PhysicalOptimizationRuleEndpointTraversal, false)
	got, err := BuildPhysicalTraversal(TraversalLoweringRequest{
		FromType:       "Patient",
		EdgeLabel:      "subject_Patient",
		ToType:         "Specimen",
		SourceVariable: "root",
		TargetVariable: "node",
		EdgeVariable:   "edge",
		BindPrefix:     "traversal_1",
		Policy:         policy,
	})
	if err != nil {
		t.Fatalf("BuildPhysicalTraversal() error = %v", err)
	}
	if got.Traversal.Strategy != ir.PhysicalTraversalNative {
		t.Fatalf("strategy = %q, want native", got.Traversal.Strategy)
	}
	if got.Traversal.EndpointField != "" || got.Traversal.EndpointJoinField != "" || len(got.Traversal.EndpointIndexFields) != 0 {
		t.Fatalf("native traversal retained endpoint contract: %#v", got.Traversal)
	}
}

func TestBuildPhysicalTraversalRejectsUnknownSchemaRoute(t *testing.T) {
	_, err := BuildPhysicalTraversal(TraversalLoweringRequest{
		FromType:       "Patient",
		EdgeLabel:      "not_generated",
		ToType:         "Specimen",
		SourceVariable: "root",
		TargetVariable: "node",
		EdgeVariable:   "edge",
		BindPrefix:     "traversal_1",
		Policy:         ir.DefaultPhysicalOptimizationPolicy(),
	})
	if err == nil {
		t.Fatal("BuildPhysicalTraversal() succeeded for an unknown schema route")
	}
}

func TestBuildPhysicalTraversalRequiresCompilerNames(t *testing.T) {
	cases := []TraversalLoweringRequest{
		{EdgeLabel: "subject_Patient", ToType: "Specimen", SourceVariable: "root", TargetVariable: "node", EdgeVariable: "edge", BindPrefix: "x"},
		{FromType: "Patient", ToType: "Specimen", SourceVariable: "root", TargetVariable: "node", EdgeVariable: "edge", BindPrefix: "x"},
		{FromType: "Patient", EdgeLabel: "subject_Patient", ToType: "Specimen", TargetVariable: "node", EdgeVariable: "edge", BindPrefix: "x"},
		{FromType: "Patient", EdgeLabel: "subject_Patient", ToType: "Specimen", SourceVariable: "root", EdgeVariable: "edge", BindPrefix: "x"},
		{FromType: "Patient", EdgeLabel: "subject_Patient", ToType: "Specimen", SourceVariable: "root", TargetVariable: "node", EdgeVariable: "edge"},
		{FromType: "Patient", EdgeLabel: "subject_Patient", ToType: "Specimen", SourceVariable: "root", TargetVariable: "node", EdgeVariable: "edge", BindPrefix: "unsafe-prefix"},
	}
	for index, request := range cases {
		if _, err := BuildPhysicalTraversal(request); err == nil {
			t.Errorf("case %d: BuildPhysicalTraversal() accepted incomplete request", index)
		}
	}
}
