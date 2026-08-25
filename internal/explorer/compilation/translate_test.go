package compilation

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
)

func fixtureSnapshot() capability.Snapshot {
	identity := capability.SnapshotIdentity{Project: "project-a", Generation: "generation-a", AuthorizationScopeDigest: "scope-a", SchemaDigest: "schema-a", CompilerVersion: "compiler-a"}
	policy := capability.Policy{Route: capability.RoutePolicy{Version: "route-a", AllowsRepeatedEdges: true, AllowsSelfLoops: true}, Projection: capability.ProjectionPolicy{Version: "projection-a", Modes: []capability.ProjectionMode{capability.ProjectionScalar, capability.ProjectionFirst, capability.ProjectionArray, capability.ProjectionDistinctArray}}}
	nodes := []capability.Node{{ID: "n_patient", ResourceType: "Patient", RowRootEligible: true, RowGrain: "patient"}, {ID: "n_encounter", ResourceType: "Encounter", RowRootEligible: true, RowGrain: "resource"}}
	edges := []capability.Edge{{ID: "e_encounter", FromNodeID: "n_patient", ToNodeID: "n_encounter", Label: "encounters"}, {ID: "e_self", FromNodeID: "n_encounter", ToNodeID: "n_encounter", Label: "revisits"}}
	ops := []capability.Operation{capability.OperationSelect, capability.OperationFilter, capability.OperationChart}
	candidates := []capability.Candidate{{ID: "c_patient_id", NodeID: "n_patient", ResourceType: "Patient", FieldPath: "id", LogicalType: "string", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar}, SupportedOperations: ops}, {ID: "c_encounter_code", NodeID: "n_encounter", ResourceType: "Encounter", FieldPath: "code.coding[].code", LogicalType: "string", ProjectionModes: []capability.ProjectionMode{capability.ProjectionFirst, capability.ProjectionArray, capability.ProjectionDistinctArray}, SupportedOperations: ops}}
	return capability.NewSnapshot(identity, policy, capability.StatusReady, true, false, nodes, edges, candidates, nil)
}

func BenchmarkCompileRepresentativeBuilderState(b *testing.B) {
	snapshot := fixtureSnapshot()
	for candidateIndex := 0; candidateIndex < 5; candidateIndex++ {
		snapshot.Candidates = append(snapshot.Candidates, capability.Candidate{
			ID: "c_encounter_" + fmt.Sprint(candidateIndex), NodeID: "n_encounter",
			ResourceType: "Encounter", FieldPath: "subject.display" + fmt.Sprint(candidateIndex),
			LogicalType: "string", ProjectionModes: []capability.ProjectionMode{capability.ProjectionFirst},
			SupportedOperations: []capability.Operation{capability.OperationSelect},
		})
	}
	// NewSnapshot recomputes the token after extending the benchmark fixture.
	snapshot = capability.NewSnapshot(snapshot.Identity, snapshot.Policy, capability.StatusReady, true, false, snapshot.Nodes, snapshot.Edges, snapshot.Candidates, nil)
	document := authoringv2.Document{APIVersion: authoringv2.APIVersion, Kind: authoringv2.Kind, Output: authoringv2.Output{ID: "representative", Title: "Representative"}, RootNodeID: "n_patient", Presentation: map[string]authoringv2.Presentation{}}
	for hop := 0; hop < 20; hop++ {
		edgeID := "e_self"
		if hop == 0 {
			edgeID = "e_encounter"
		}
		occurrenceID := fmt.Sprintf("hop-%d", hop)
		document.RouteSteps = append(document.RouteSteps, authoringv2.RouteStep{EdgeID: edgeID, OccurrenceID: occurrenceID})
		for candidateIndex := 0; candidateIndex < 5; candidateIndex++ {
			document.Selections = append(document.Selections, authoringv2.Selection{CandidateID: "c_encounter_" + fmt.Sprint(candidateIndex), OccurrenceID: occurrenceID, ProjectionMode: "FIRST"})
		}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Compile(context.Background(), "project-a", "explorer-a", document, snapshot); err != nil {
			b.Fatal(err)
		}
	}
}

func fixtureDocument() authoringv2.Document {
	return authoringv2.Document{APIVersion: authoringv2.APIVersion, Kind: authoringv2.Kind, Output: authoringv2.Output{ID: "patient_output", Title: "Patients"}, RootNodeID: "n_patient", RouteSteps: []authoringv2.RouteStep{{EdgeID: "e_encounter", OccurrenceID: "encounter"}, {EdgeID: "e_self", OccurrenceID: "encounter_again"}}, Selections: []authoringv2.Selection{{CandidateID: "c_patient_id", OccurrenceID: "base", ProjectionMode: "SCALAR"}, {CandidateID: "c_encounter_code", OccurrenceID: "encounter_again", ProjectionMode: "DISTINCT_ARRAY"}}, Presentation: map[string]authoringv2.Presentation{"c_patient_id": {Label: "Patient ID"}}}
}

func TestCompileNativeV2BuildsNestedRouteAndStableColumns(t *testing.T) {
	snapshot := fixtureSnapshot()
	result, err := Compile(context.Background(), "project-a", "explorer-a", fixtureDocument(), snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Bundle.Outputs) != 1 || len(result.Bundle.Outputs[0].Traversals) != 1 || len(result.Bundle.Outputs[0].Traversals[0].Traversals) != 1 {
		t.Fatalf("route was not lowered as a nested chain: %#v", result.Bundle.Outputs[0].Traversals)
	}
	if got := result.Bundle.Outputs[0].Traversals[0].Traversals[0].Fields[0].ValueMode; got != "DISTINCT" {
		t.Fatalf("projection value mode=%q", got)
	}
	if len(result.EmittedColumns) != 2 || result.EmittedColumns[0].EmissionID == result.EmittedColumns[1].EmissionID {
		t.Fatalf("emissions=%#v", result.EmittedColumns)
	}
	if result.Presentation.Columns[0].Label != "Patient ID" || !result.Presentation.Columns[0].Visible {
		t.Fatalf("presentation=%#v", result.Presentation)
	}
	again, err := Compile(context.Background(), "project-a", "explorer-a", fixtureDocument(), snapshot)
	if err != nil || again.RecipeDigest != result.RecipeDigest || again.EmittedColumns[0].EmissionID != result.EmittedColumns[0].EmissionID {
		t.Fatalf("compile is not stable: first=%#v again=%#v err=%v", result, again, err)
	}
}

func TestCompileAllowsRepeatedSelfLoopAndLongFiniteRoutes(t *testing.T) {
	snapshot := fixtureSnapshot()
	document := fixtureDocument()
	for i := 0; i < 12; i++ {
		document.RouteSteps = append(document.RouteSteps, authoringv2.RouteStep{EdgeID: "e_self", OccurrenceID: "loop-" + string(rune('a'+i))})
	}
	document.Selections[1].OccurrenceID = "loop-l"
	if _, err := Compile(context.Background(), "project-a", "explorer-a", document, snapshot); err != nil {
		t.Fatal(err)
	}
}

func TestCompileRejectsMismatchesWithStructuredErrors(t *testing.T) {
	document := fixtureDocument()
	_, err := Compile(context.Background(), "other-project", "explorer-a", document, fixtureSnapshot())
	var structured *Error
	if !errors.As(err, &structured) || structured.Code != "PROJECT_MISMATCH" || structured.Stage != "capability" || structured.Path != "$.project" {
		t.Fatalf("error=%#v", err)
	}
	document = fixtureDocument()
	document.Selections[0].ProjectionMode = "ARRAY"
	_, err = Compile(context.Background(), "project-a", "explorer-a", document, fixtureSnapshot())
	if !errors.As(err, &structured) || structured.Code != "UNSUPPORTED_PROJECTION_MODE" || !strings.Contains(structured.Path, "projectionMode") {
		t.Fatalf("projection error=%#v", err)
	}
}
