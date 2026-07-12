package dataframe

import (
	"errors"
	"strings"
	"testing"
)

func TestResolveStorageRouteAcceptsGeneratedBuilderOrientation(t *testing.T) {
	route, err := resolveStorageRoute("Patient", "subject_Patient", "Condition")
	if err != nil {
		t.Fatalf("resolveStorageRoute(Patient -> Condition): %v", err)
	}
	if route.Direction != PhysicalInbound {
		t.Fatalf("route direction = %q, want %q", route.Direction, PhysicalInbound)
	}
	if route.targetEdgeTypeField() != "from_type" {
		t.Fatalf("inbound target edge type field = %q, want from_type", route.targetEdgeTypeField())
	}

	planned, err := lowerGenericGraphQLBuilder(Builder{}, buildLogicalRequest(Builder{
		Project:          "P1",
		RootResourceType: "Patient",
		Traversals: []TraversalStep{{
			Alias: "diagnosis", Label: "subject_Patient", ToResourceType: "Condition",
		}},
	}))
	if err != nil {
		t.Fatalf("lowerGenericGraphQLBuilder: %v", err)
	}
	if len(planned.Sets) != 1 || planned.Sets[0].Direction != string(PhysicalInbound) {
		t.Fatalf("generic sets = %#v, want one INBOUND stored route", planned.Sets)
	}
	compiled, err := Compile(planned, 5)
	if err != nil {
		t.Fatalf("Compile(generic builder): %v", err)
	}
	for _, required := range []string{
		"1..1 INBOUND root fhir_edge",
		"__edge.project == @project",
		"__node.project == @project",
		"__edge.dataset_generation == @dataset_generation",
		"__node.dataset_generation == @dataset_generation",
		"auth_resource_path IN @auth_resource_paths",
		"__edge.from_type == @",
		"__node.resourceType == @",
	} {
		if !strings.Contains(compiled.Query, required) {
			t.Fatalf("generic query does not retain %q:\n%s", required, compiled.Query)
		}
	}
}

func TestGenericLoweredOutboundTraversalScopesLegacyRenderer(t *testing.T) {
	compiled, err := Compile(Builder{
		Project:           "P1",
		DatasetGeneration: "generation-1",
		AuthResourcePaths: []string{"/programs/p1"},
		RootResourceType:  "ResearchSubject",
		PlanHint:          &PlanHint{Mode: "lowered", Profile: "generic_fhir_graph"},
		Fields:            []FieldSelect{{Name: "id", Select: "id"}},
		Sets: []NamedSet{{
			Name: "study", Kind: SetKindTraverse, Label: "study", ToResourceType: "ResearchStudy", Direction: "OUTBOUND",
		}},
	}, 5)
	if err != nil {
		t.Fatalf("Compile(generic outbound lowered builder): %v", err)
	}
	for _, required := range []string{
		"1..1 OUTBOUND root fhir_edge",
		"__edge.project == @project",
		"__node.project == @project",
		"__edge.dataset_generation == @dataset_generation",
		"__node.dataset_generation == @dataset_generation",
		"__edge.to_type == @",
		"__node.resourceType == @",
	} {
		if !strings.Contains(compiled.Query, required) {
			t.Fatalf("legacy outbound traversal is missing %q:\n%s", required, compiled.Query)
		}
	}
	assertBindVarValue(t, compiled.BindVars, "ResearchStudy")
}

func TestGenericLoweredRequiredOutboundTraversalScopesTargetNode(t *testing.T) {
	compiled, err := Compile(Builder{
		Project:           "P1",
		DatasetGeneration: "generation-1",
		AuthResourcePaths: []string{"/programs/p1"},
		RootResourceType:  "ResearchSubject",
		PlanHint:          &PlanHint{Mode: "lowered", Profile: "generic_fhir_graph"},
		RequiredTraversalMatches: []RequiredTraversalMatch{{
			Steps: []TraversalMatchStep{{Label: "study", ToResourceType: "ResearchStudy"}},
		}},
	}, 5)
	if err != nil {
		t.Fatalf("Compile(generic required outbound lowered builder): %v", err)
	}
	for _, required := range []string{
		"FOR __match_0_0, __match_edge_0_0 IN 1..1 OUTBOUND root fhir_edge",
		"__match_edge_0_0.project == @project",
		"__match_0_0.project == @project",
		"__match_edge_0_0.dataset_generation == @dataset_generation",
		"__match_0_0.dataset_generation == @dataset_generation",
		"__match_edge_0_0.to_type == @",
		"__match_0_0.resourceType == @",
	} {
		if !strings.Contains(compiled.Query, required) {
			t.Fatalf("required outbound traversal is missing %q:\n%s", required, compiled.Query)
		}
	}
	assertBindVarValue(t, compiled.BindVars, "ResearchStudy")
}

func TestStorageRouteAcceptsProvenOutboundResearchSubjectStudy(t *testing.T) {
	route, err := resolveStorageRoute("ResearchSubject", "study", "ResearchStudy")
	if err != nil {
		t.Fatalf("resolveStorageRoute(ResearchSubject -> ResearchStudy): %v", err)
	}
	if route.Direction != PhysicalOutbound {
		t.Fatalf("route direction = %q, want %q", route.Direction, PhysicalOutbound)
	}
	if route.targetEdgeTypeField() != "to_type" {
		t.Fatalf("outbound target edge type field = %q, want to_type", route.targetEdgeTypeField())
	}

	request := Builder{
		Project:           "P1",
		DatasetGeneration: "generation-1",
		AuthResourcePaths: []string{"/programs/p1"},
		RootResourceType:  "ResearchSubject",
		Traversals: []TraversalStep{{
			Alias: "study", Label: "study", ToResourceType: "ResearchStudy",
		}},
	}
	compiled, err := CompileRequest(request, 5)
	if err != nil {
		t.Fatalf("CompileRequest(ResearchSubject -> ResearchStudy): %v", err)
	}
	for _, required := range []string{
		"1..1 OUTBOUND root @@traversal_1_edge_collection",
		"edge_1.project == @project",
		"node_1.project == @project",
		"edge_1.dataset_generation == @dataset_generation",
		"node_1.dataset_generation == @dataset_generation",
		"auth_resource_path IN @auth_resource_paths",
		"edge_1.label == @traversal_1_label",
		"edge_1.to_type == @traversal_1_target_type",
		"node_1.resourceType == @traversal_1_target_type",
	} {
		if !strings.Contains(compiled.Query, required) {
			t.Fatalf("outbound query does not retain %q:\n%s", required, compiled.Query)
		}
	}

	explanation, err := ExplainCompilerRequest(request, 5)
	if err != nil {
		t.Fatalf("ExplainCompilerRequest(ResearchSubject -> ResearchStudy): %v", err)
	}
	if !explanation.GenericPhysicalPlan.Available || explanation.GenericPhysicalPlan.Plan == nil {
		t.Fatalf("outbound generic physical plan = %#v", explanation.GenericPhysicalPlan)
	}
	var traversal *PhysicalTraversal
	for _, operation := range explanation.GenericPhysicalPlan.Plan.Operations {
		if operation.Kind == PhysicalTraversalOp {
			traversal = operation.Traversal
			break
		}
	}
	if traversal == nil || traversal.Direction != PhysicalOutbound {
		t.Fatalf("explained outbound traversal = %#v, want OUTBOUND", traversal)
	}
}

func TestStorageRouteRejectsGeneratedForwardFHIRReference(t *testing.T) {
	_, err := resolveStorageRoute("Condition", "subject_Patient", "Patient")
	if !errors.Is(err, ErrUnsupportedStorageRoute) {
		t.Fatalf("resolveStorageRoute(Condition -> Patient) error = %v, want ErrUnsupportedStorageRoute", err)
	}

	_, err = CompileRequest(Builder{
		Project:          "P1",
		RootResourceType: "Condition",
		Traversals: []TraversalStep{{
			Alias: "patient", Label: "subject_Patient", ToResourceType: "Patient",
		}},
	}, 5)
	if !errors.Is(err, ErrUnsupportedStorageRoute) {
		t.Fatalf("CompileRequest(forward route) error = %v, want ErrUnsupportedStorageRoute", err)
	}
}

func TestStorageRouteAppliesToNestedRequiredMatches(t *testing.T) {
	_, err := Lower(Builder{
		Project:          "P1",
		RootResourceType: "Patient",
		Traversals: []TraversalStep{{
			Alias: "specimen", Label: "subject_Patient", ToResourceType: "Specimen",
			Traversals: []TraversalStep{{
				Alias: "patient", Label: "subject_Patient", ToResourceType: "Patient", MatchMode: TraversalMatchRequired,
			}},
		}},
	})
	if !errors.Is(err, ErrUnsupportedStorageRoute) {
		t.Fatalf("Lower(nested required forward route) error = %v, want ErrUnsupportedStorageRoute", err)
	}
}

func TestStorageRouteAppliesToGenericPhysicalPlansAndPreLoweredBuilders(t *testing.T) {
	semantic, err := BuildSemanticPlan(Builder{
		Project:          "P1",
		RootResourceType: "Patient",
		Traversals: []TraversalStep{{
			Alias: "diagnosis", Label: "subject_Patient", ToResourceType: "Condition",
		}},
	})
	if err != nil {
		t.Fatalf("BuildSemanticPlan(builder route): %v", err)
	}
	physical, err := BuildGenericPhysicalPlan(semantic)
	if err != nil {
		t.Fatalf("BuildGenericPhysicalPlan: %v", err)
	}
	var traversal *PhysicalTraversal
	for _, operation := range physical.Operations {
		if operation.Kind == PhysicalTraversalOp {
			traversal = operation.Traversal
			break
		}
	}
	if traversal == nil || traversal.Direction != PhysicalInbound {
		t.Fatalf("physical traversal = %#v, want INBOUND", traversal)
	}

	_, err = Compile(Builder{
		Project:          "P1",
		RootResourceType: "Patient",
		PlanHint:         &PlanHint{Mode: "lowered", Profile: "generic_fhir_graph"},
		Sets: []NamedSet{{
			Name: "diagnosis", Kind: SetKindTraverse, Label: "subject_Patient", ToResourceType: "Condition", Direction: "OUTBOUND",
		}},
	}, 1)
	if !errors.Is(err, ErrUnsupportedStorageRoute) {
		t.Fatalf("Compile(pre-lowered wrong direction) error = %v, want ErrUnsupportedStorageRoute", err)
	}

	_, err = Compile(Builder{
		Project:          "P1",
		RootResourceType: "ResearchSubject",
		PlanHint:         &PlanHint{Mode: "lowered", Profile: "generic_fhir_graph"},
		Sets: []NamedSet{{
			Name: "study", Kind: SetKindTraverse, Label: "study", ToResourceType: "ResearchStudy", Direction: "INBOUND",
		}},
	}, 1)
	if !errors.Is(err, ErrUnsupportedStorageRoute) {
		t.Fatalf("Compile(pre-lowered outbound route with INBOUND direction) error = %v, want ErrUnsupportedStorageRoute", err)
	}
}

func TestGenericStorageRoutePropagatesTypeThroughFilterSet(t *testing.T) {
	_, err := Compile(Builder{
		Project:          "P1",
		RootResourceType: "Patient",
		PlanHint:         &PlanHint{Mode: "lowered", Profile: "generic_fhir_graph"},
		Sets: []NamedSet{
			{Name: "specimen", Kind: SetKindTraverse, Label: "subject_Patient", ToResourceType: "Specimen", Direction: "INBOUND"},
			{Name: "filtered_specimen", Kind: SetKindFilter, Source: "specimen", MatchResourceType: "Specimen"},
			{Name: "file", Kind: SetKindTraverse, Source: "filtered_specimen", Label: "subject_Specimen", ToResourceType: "DocumentReference", Direction: "INBOUND"},
		},
	}, 1)
	if err != nil {
		t.Fatalf("Compile(generic route through FILTER set): %v", err)
	}
}
