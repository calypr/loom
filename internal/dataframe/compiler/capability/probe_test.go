package capability

import (
	"context"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/spec"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

func testScope() Scope {
	return Scope{Project: "project-1", DatasetGeneration: "generation-7", AuthScopeMode: authscope.ReadScopeRestricted, AuthResourcePaths: []string{"/programs/p1"}}
}

func TestProbeRootRejectsBackboneAndCustomButAcceptsGenericResource(t *testing.T) {
	if _, err := ProbeRoot(context.Background(), RootRequest{Scope: testScope(), ResourceType: "PractitionerQualification"}); err == nil {
		t.Fatal("backbone definition was accepted as a row root")
	}
	if _, err := ProbeRoot(context.Background(), RootRequest{Scope: testScope(), ResourceType: "CustomResource"}); err == nil {
		t.Fatal("custom resource was accepted as a row root")
	}
	result, err := ProbeRoot(context.Background(), RootRequest{Scope: testScope(), ResourceType: "Observation"})
	if err != nil {
		t.Fatalf("generic generated resource was rejected: %v", err)
	}
	if !result.Root.RowRootEligible || result.Root.RowGrain != spec.RowGrainObservation || result.Rendered.Query == "" {
		t.Fatalf("unexpected root capability: %#v", result.Root)
	}
	assertScopedQuery(t, result.Rendered)
}

func TestProbeTraversalProvesInboundAndOutboundStorageDirection(t *testing.T) {
	inbound, err := ProbeTraversal(context.Background(), TraversalRequest{Scope: testScope(), RootResourceType: "Patient", Traversal: Traversal{EdgeLabel: "subject_Patient", ToResourceType: "Specimen"}})
	if err != nil {
		t.Fatalf("inbound route: %v", err)
	}
	if inbound.Traversal.StorageDirection != "INBOUND" || inbound.Traversal.SchemaDirection == "" {
		t.Fatalf("unexpected inbound capability: %#v", inbound.Traversal)
	}
	outbound, err := ProbeTraversal(context.Background(), TraversalRequest{Scope: testScope(), RootResourceType: "Observation", Traversal: Traversal{EdgeLabel: "subject_Patient", ToResourceType: "Patient"}})
	if err != nil {
		t.Fatalf("outbound route: %v", err)
	}
	if outbound.Traversal.StorageDirection != "OUTBOUND" {
		t.Fatalf("unexpected outbound capability: %#v", outbound.Traversal)
	}
	assertScopedQuery(t, outbound.Rendered)
}

func TestProbeTraversalRejectsInvalidRoute(t *testing.T) {
	for _, route := range []Traversal{
		{EdgeLabel: "missing", ToResourceType: "Specimen"},
		{EdgeLabel: "subject_Patient", ToResourceType: "Medication"},
		{FromResourceType: "Condition", EdgeLabel: "subject_Patient", ToResourceType: "Specimen"},
	} {
		if _, err := ProbeTraversal(context.Background(), TraversalRequest{Scope: testScope(), RootResourceType: "Patient", Traversal: route}); err == nil {
			t.Fatalf("invalid route was accepted: %#v", route)
		}
	}
}

func TestProbeCandidateReportsScalarRepeatedObjectAndOperations(t *testing.T) {
	scalar, err := ProbeCandidate(context.Background(), CandidateRequest{Scope: testScope(), ResourceType: "Patient", FieldRef: "Patient.gender", Selector: "gender"})
	if err != nil {
		t.Fatalf("scalar candidate: %v", err)
	}
	if scalar.Candidate.Repeated || !scalar.Candidate.Filterable || len(scalar.Candidate.ProjectionModes) == 0 {
		t.Fatalf("unexpected scalar candidate: %#v", scalar.Candidate)
	}
	status, err := ProbeCandidate(context.Background(), CandidateRequest{Scope: testScope(), ResourceType: "Observation", FieldRef: "Observation.status", Selector: "status"})
	if err != nil {
		t.Fatalf("Observation.status candidate: %v", err)
	}
	if status.Candidate.Repeated || status.Candidate.Cardinality != spec.CardinalityOptionalOne {
		t.Fatalf("Observation.status must be scalar: %#v", status.Candidate)
	}
	repeated, err := ProbeCandidate(context.Background(), CandidateRequest{Scope: testScope(), ResourceType: "Observation", FieldRef: "Observation.code.coding[].display", Selector: "code.coding[].display"})
	if err != nil {
		t.Fatalf("repeated candidate: %v", err)
	}
	if !repeated.Candidate.Repeated || !containsProjection(repeated.Candidate.ProjectionModes, spec.ProjectionArray) {
		t.Fatalf("unexpected repeated candidate: %#v", repeated.Candidate)
	}
	object, err := ProbeCandidate(context.Background(), CandidateRequest{Scope: testScope(), ResourceType: "Observation", FieldRef: "Observation.code", Selector: "code"})
	if err != nil {
		t.Fatalf("object candidate: %v", err)
	}
	if object.Candidate.FieldKind != "object" || object.Candidate.Filterable {
		t.Fatalf("unexpected object candidate: %#v", object.Candidate)
	}
	if _, err := ProbeCandidate(context.Background(), CandidateRequest{Scope: testScope(), ResourceType: "Patient", Selector: "gender", Projection: spec.ProjectionArray}); err == nil {
		t.Fatal("unsupported scalar array projection was accepted")
	}
	if _, err := ProbeCandidate(context.Background(), CandidateRequest{Scope: testScope(), ResourceType: "Patient", Selector: "gender", Filter: &Filter{Operator: spec.FilterGreaterThan}}); err == nil {
		t.Fatal("unsupported string comparison filter was accepted")
	}
	if _, err := ProbeCandidate(context.Background(), CandidateRequest{Scope: testScope(), ResourceType: "Patient", Selector: "gender", Chart: &Chart{Operation: recipe.AggregateOperation("NOT_A_CHART")}}); err == nil {
		t.Fatal("unsupported chart operation was accepted")
	}
}

func TestProbeCandidateExecutesNumericAggregateCompilerProofs(t *testing.T) {
	numeric, err := ProbeCandidate(context.Background(), CandidateRequest{
		Scope: testScope(), ResourceType: "Observation", FieldRef: "Observation.valueQuantity.value", Selector: "valueQuantity.value",
	})
	if err != nil {
		t.Fatalf("numeric candidate: %v", err)
	}
	for _, operation := range []recipe.AggregateOperation{recipe.AggregateSum, recipe.AggregateMean} {
		if !containsAggregateOperation(numeric.Candidate.ValueAggregateOperations, operation) {
			t.Errorf("numeric compiler proof is missing %s: %#v", operation, numeric.Candidate.ValueAggregateOperations)
		}
	}
	compiled, err := ProbeCandidate(context.Background(), CandidateRequest{
		Scope: testScope(), ResourceType: "Observation", FieldRef: "Observation.valueQuantity.value", Selector: "valueQuantity.value",
		Chart: &Chart{Operation: recipe.AggregateSum},
	})
	if err != nil {
		t.Fatalf("execute literal SUM compiler request: %v", err)
	}
	for _, fragment := range []string{"SUM(", "IS_NUMBER(", "NUMERIC_AGGREGATE_NON_NUMERIC", "== 0 ? null"} {
		if !strings.Contains(compiled.Rendered.Query, fragment) {
			t.Errorf("rendered SUM query is missing %q:\n%s", fragment, compiled.Rendered.Query)
		}
	}
	mean, err := ProbeCandidate(context.Background(), CandidateRequest{
		Scope: testScope(), ResourceType: "Observation", FieldRef: "Observation.valueQuantity.value", Selector: "valueQuantity.value",
		Chart: &Chart{Operation: recipe.AggregateMean},
	})
	if err != nil {
		t.Fatalf("execute literal MEAN compiler request: %v", err)
	}
	if !strings.Contains(mean.Rendered.Query, "SUM(") || !strings.Contains(mean.Rendered.Query, " / LENGTH(") {
		t.Fatalf("MEAN query must divide the non-null numeric sum by its contributor count:\n%s", mean.Rendered.Query)
	}
	if _, err := ProbeCandidate(context.Background(), CandidateRequest{
		Scope: testScope(), ResourceType: "Patient", FieldRef: "Patient.gender", Selector: "gender",
		Chart: &Chart{Operation: recipe.AggregateSum},
	}); err == nil || !strings.Contains(err.Error(), "integer or decimal selector") {
		t.Fatalf("string SUM error = %v", err)
	}
}

func containsAggregateOperation(values []recipe.AggregateOperation, want recipe.AggregateOperation) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestProbeCandidateRendersGenerationProjectAndAuthScope(t *testing.T) {
	result, err := ProbeCandidate(context.Background(), CandidateRequest{Scope: testScope(), ResourceType: "Patient", Selector: "gender", Filter: &Filter{Operator: spec.FilterEquals}})
	if err != nil {
		t.Fatalf("candidate filter: %v", err)
	}
	assertScopedQuery(t, result.Rendered)
}

func TestProbeCandidateSupportsRelatedOccurrence(t *testing.T) {
	result, err := ProbeCandidate(context.Background(), CandidateRequest{
		Scope:            testScope(),
		RootResourceType: "Patient",
		ResourceType:     "Specimen",
		Selector:         "id",
		Route:            []Traversal{{FromResourceType: "Patient", EdgeLabel: "subject_Patient", ToResourceType: "Specimen", Alias: "specimen"}},
	})
	if err != nil {
		t.Fatalf("related candidate: %v", err)
	}
	if result.Rendered.BindVars["child_set_1_label"] != "subject_Patient" || result.Candidate.ResourceType != "Specimen" {
		t.Fatalf("related candidate lost route or metadata: %#v\n%s", result.Candidate, result.Rendered.Query)
	}
}

func TestProbeOwnerRecordsProvesRootAndRelatedBindings(t *testing.T) {
	binding := fhirschema.CorrelatedBinding{
		OwnerPath: "component[]", KeyPath: "component[].code.coding[]",
		SystemPath: "system", CodePath: "code", ValuePath: "valueQuantity.value",
		ChoiceArms: []string{"valueQuantity"}, LogicalType: "decimal", UnitPath: "valueQuantity.unit",
	}
	root, err := ProbeOwnerRecords(context.Background(), OwnerRecordsRequest{
		Scope: testScope(), RootResourceType: "Observation", ResourceType: "Observation",
		Binding: binding, Key: fhirschema.CorrelatedKey{System: "http://loinc.org", Code: "8302-2"},
	})
	if err != nil {
		t.Fatalf("root owner records: %v", err)
	}
	if root.OwnerRecords == nil || !strings.Contains(root.Rendered.Query, "ownerOrdinal") {
		t.Fatalf("owner-record proof = %#v", root)
	}
	assertScopedQuery(t, root.Rendered)

	related, err := ProbeOwnerRecords(context.Background(), OwnerRecordsRequest{
		Scope: testScope(), RootResourceType: "Patient", ResourceType: "Observation",
		Route:   []Traversal{{FromResourceType: "Patient", EdgeLabel: "subject_Patient", ToResourceType: "Observation", Alias: "observation"}},
		Binding: binding, Key: fhirschema.CorrelatedKey{System: "http://loinc.org", Code: "8302-2"},
	})
	if err != nil {
		t.Fatalf("related owner records: %v", err)
	}
	if related.Rendered.BindVars["child_set_1_label"] != "subject_Patient" {
		t.Fatalf("related owner-record route binds = %#v", related.Rendered.BindVars)
	}
}

func TestProbeOwnerRecordsRejectsUnprovedShapes(t *testing.T) {
	base := OwnerRecordsRequest{
		Scope: testScope(), ResourceType: "Observation",
		Binding: fhirschema.CorrelatedBinding{
			OwnerPath: "component[]", KeyPath: "component[].code.coding[]",
			SystemPath: "system", CodePath: "code", ValuePath: "valueString", LogicalType: "string",
		},
		Key: fhirschema.CorrelatedKey{System: "urn:test", Code: "code"},
	}
	for _, mutate := range []func(*OwnerRecordsRequest){
		func(request *OwnerRecordsRequest) { request.Key.System = "" },
		func(request *OwnerRecordsRequest) { request.Binding.ValuePath = "missing" },
	} {
		request := base
		mutate(&request)
		if _, err := ProbeOwnerRecords(context.Background(), request); err == nil {
			t.Fatalf("invalid owner-record request was accepted: %#v", request)
		}
	}
}

func TestProbeOwnerRecordsProvesResourceOwner(t *testing.T) {
	result, err := ProbeOwnerRecords(context.Background(), OwnerRecordsRequest{
		Scope: testScope(), RootResourceType: "Observation", ResourceType: "Observation",
		Binding: fhirschema.CorrelatedBinding{
			KeyPath: "code.coding[]", SystemPath: "system", CodePath: "code",
			ValuePath: "valueString", ChoiceArms: []string{"valueString"}, LogicalType: "string",
		},
		Key: fhirschema.CorrelatedKey{System: "urn:test", Code: "result"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.OwnerRecords == nil || !strings.Contains(result.Rendered.Query, "ownerOrdinal") {
		t.Fatalf("resource owner evidence proof = %#v", result)
	}
	assertScopedQuery(t, result.Rendered)
}

func assertScopedQuery(t *testing.T, rendered Rendered) {
	t.Helper()
	if rendered.Query == "" || !strings.Contains(rendered.Query, "@project") || !strings.Contains(rendered.Query, "@dataset_generation") {
		t.Fatalf("query is not scoped: %q", rendered.Query)
	}
	if rendered.Project != "project-1" || rendered.DatasetGeneration != "generation-7" || len(rendered.AuthResourcePaths) != 1 {
		t.Fatalf("scope provenance = %#v", rendered)
	}
}

func containsProjection(values []spec.ProjectionMode, want spec.ProjectionMode) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
