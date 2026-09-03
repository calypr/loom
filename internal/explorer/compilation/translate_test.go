package compilation

import (
	"context"
	"errors"
	"testing"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
)

func fixtureSnapshot() capability.Snapshot {
	return fixtureSnapshotForProject("project-a")
}

func fixtureSnapshotForProject(project string) capability.Snapshot {
	identity := capability.SnapshotIdentity{Project: project, Generation: "generation-a", AuthorizationScopeDigest: "scope-a", SchemaDigest: "schema-a", CompilerVersion: "compiler-a"}
	policy := capability.Policy{Route: capability.RoutePolicy{Version: "route-a", AllowsRepeatedEdges: true, AllowsSelfLoops: true}, Projection: capability.ProjectionPolicy{Version: "projection-a", Modes: []capability.ProjectionMode{capability.ProjectionScalar, capability.ProjectionFirst, capability.ProjectionArray, capability.ProjectionDistinctArray}}}
	nodes := []capability.Node{{ID: "n_patient", ResourceType: "Patient", RowRootEligible: true, RowGrain: "patient"}, {ID: "n_encounter", ResourceType: "Encounter", RowRootEligible: true, RowGrain: "resource"}}
	edges := []capability.Edge{{ID: "e_encounter", FromNodeID: "n_patient", ToNodeID: "n_encounter", Label: "encounters"}, {ID: "e_self", FromNodeID: "n_encounter", ToNodeID: "n_encounter", Label: "revisits"}}
	ops := []capability.Operation{capability.OperationSelect, capability.OperationFilter, capability.OperationChart}
	candidates := []capability.Candidate{{ID: "c_patient_id", NodeID: "n_patient", ResourceType: "Patient", FieldPath: "id", Label: "Patient.id", LogicalType: "string", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar}, SupportedOperations: ops}, {ID: "c_encounter_code", NodeID: "n_encounter", ResourceType: "Encounter", FieldPath: "code.coding[].code", Label: "Encounter.code.coding[].code", LogicalType: "string", ProjectionModes: []capability.ProjectionMode{capability.ProjectionFirst, capability.ProjectionArray, capability.ProjectionDistinctArray}, SupportedOperations: ops}}
	return capability.NewSnapshot(identity, policy, capability.StatusReady, true, false, nodes, edges, candidates, nil)
}

func TestCompileAcceptsEquivalentProjectIdentities(t *testing.T) {
	visible := true
	document := authoringv2.Document{
		Kind:             authoringv2.Kind,
		Output:           authoringv2.Output{ID: "patient_output", Title: "Patients"},
		RootResourceType: "Patient",
		Route:            authoringv2.RouteNode{OccurrenceID: "base", ResourceType: "Patient"},
		Columns: []authoringv2.Column{{
			Column: "project_id", Label: "Project", LogicalType: "string", OccurrenceID: "base",
			Source: authoringv2.ColumnSource{Kind: authoringv2.SourceProjectID},
			Table:  &authoringv2.TablePresentation{Visible: &visible},
		}},
	}
	snapshot := fixtureSnapshotForProject("HTAN_INT/BForePC")

	for _, project := range []string{"HTAN_INT/BForePC", "HTAN_INT%2FBForePC", "HTAN_INT-BForePC"} {
		t.Run(project, func(t *testing.T) {
			result, err := Compile(context.Background(), project, "explorer-a", document, snapshot)
			if err != nil {
				t.Fatalf("Compile(%q) rejected an equivalent project identity: %v", project, err)
			}
			if got := string(result.Bundle.Outputs[0].Fields[0].Expr.Literal); got != `"HTAN_INT/BForePC"` {
				t.Fatalf("project binding = %s, want canonical project identity", got)
			}
		})
	}

	_, err := Compile(context.Background(), "HTAN_INT/OtherProject", "explorer-a", document, snapshot)
	var compileErr *Error
	if !errors.As(err, &compileErr) || compileErr.Code != "PROJECT_MISMATCH" {
		t.Fatalf("different project error = %v, want PROJECT_MISMATCH", err)
	}
}

func TestCompileSemanticWorkspacePreservesAuthoredColumnsAndTypedSources(t *testing.T) {
	visible, order := true, 0
	document := authoringv2.Document{
		Kind:             authoringv2.Kind,
		Output:           authoringv2.Output{ID: "patient_output", Title: "Patients", RowLabel: "People"},
		RootResourceType: "Patient",
		Route:            authoringv2.RouteNode{OccurrenceID: "base", ResourceType: "Patient", Children: []authoringv2.RouteNode{{OccurrenceID: "encounter", ResourceType: "Encounter", Relationship: "encounters"}}},
		Columns: []authoringv2.Column{
			{Column: "patient_id", Label: "Patient ID", LogicalType: "string", OccurrenceID: "base", Source: authoringv2.ColumnSource{Kind: authoringv2.SourceField, FieldPath: "id", ProjectionMode: "VALUE"}, Table: &authoringv2.TablePresentation{Visible: &visible, Order: &order}},
			{Column: "encounter__code", Label: "Encounter code", LogicalType: "string", OccurrenceID: "encounter", Source: authoringv2.ColumnSource{Kind: authoringv2.SourceField, FieldPath: "code.coding[].code", ProjectionMode: "FIRST"}},
			{Column: "project_id", Label: "Project", LogicalType: "string", OccurrenceID: "base", Source: authoringv2.ColumnSource{Kind: authoringv2.SourceProjectID}},
		},
	}
	result, err := Compile(context.Background(), "project-a", "explorer-a", document, fixtureSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"patient_id", "encounter__code", "project_id"}
	for index, column := range result.OutputContract.Columns {
		if column.Column != want[index] || result.EmittedColumns[index].PublicColumn != want[index] {
			t.Fatalf("column %d = %#v emission=%#v", index, column, result.EmittedColumns[index])
		}
	}
	if got := result.Bundle.Outputs[0].Traversals[0].Alias; got != "encounter" {
		t.Fatalf("traversal alias = %q", got)
	}
	if got := result.Bundle.Outputs[0].TraversalColumnNaming; got != recipe.TraversalColumnNamingAlias {
		t.Fatalf("traversal column naming = %q", got)
	}
	if got := result.Bundle.Outputs[0].RootColumnNaming; got != recipe.RootColumnNamingExact {
		t.Fatalf("root column naming = %q", got)
	}
	if got := string(result.Bundle.Outputs[0].Fields[1].Expr.Literal); got != `"project-a"` {
		t.Fatalf("project binding = %s", got)
	}
}

func TestCompileSemanticWorkspacePreservesSiblingBranchesDeterministically(t *testing.T) {
	visible := true
	document := authoringv2.Document{
		Kind:             authoringv2.Kind,
		Output:           authoringv2.Output{ID: "patient_output", Title: "Patients"},
		RootResourceType: "Patient",
		Route: authoringv2.RouteNode{
			OccurrenceID: "base", ResourceType: "Patient",
			Children: []authoringv2.RouteNode{
				{OccurrenceID: "encounter_b", ResourceType: "Encounter", Relationship: "encounters"},
				{OccurrenceID: "encounter_a", ResourceType: "Encounter", Relationship: "encounters"},
			},
		},
		Columns: []authoringv2.Column{
			{Column: "patient_id", Label: "Patient ID", OccurrenceID: "base", Source: authoringv2.ColumnSource{Kind: authoringv2.SourceField, FieldPath: "id", ProjectionMode: "VALUE"}, Table: &authoringv2.TablePresentation{Visible: &visible}},
			{Column: "encounter_a__code", Label: "Encounter A", OccurrenceID: "encounter_a", Source: authoringv2.ColumnSource{Kind: authoringv2.SourceField, FieldPath: "code.coding[].code", ProjectionMode: "FIRST"}, Table: &authoringv2.TablePresentation{Visible: &visible}},
			{Column: "encounter_b__code", Label: "Encounter B", OccurrenceID: "encounter_b", Source: authoringv2.ColumnSource{Kind: authoringv2.SourceField, FieldPath: "code.coding[].code", ProjectionMode: "FIRST"}, Table: &authoringv2.TablePresentation{Visible: &visible}},
		},
	}

	first, err := Compile(context.Background(), "project-a", "explorer-a", document, fixtureSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	traversals := first.Bundle.Outputs[0].Traversals
	if len(traversals) != 2 || traversals[0].Alias != "encounter_a" || traversals[1].Alias != "encounter_b" {
		t.Fatalf("sibling traversals = %#v", traversals)
	}
	if traversals[0].Fields[0].Name != "code" || traversals[1].Fields[0].Name != "code" {
		t.Fatalf("sibling fields = %#v", traversals)
	}

	document.Route.Children[0], document.Route.Children[1] = document.Route.Children[1], document.Route.Children[0]
	second, err := Compile(context.Background(), "project-a", "explorer-a", document, fixtureSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	if first.RecipeDigest != second.RecipeDigest {
		t.Fatalf("sibling order changed recipe identity: %q != %q", first.RecipeDigest, second.RecipeDigest)
	}
}

func TestSemanticObservationPivotFreezesOnlyAuthoredComponentColumns(t *testing.T) {
	column := authoringv2.Column{
		Column:       "observation__observation_component_values__GENE_SYMBOL",
		OccurrenceID: "observation",
		Source: authoringv2.ColumnSource{
			Kind:      authoringv2.SourceObservationComponentByCode,
			FieldPath: "component[]",
			Match:     "GENE_SYMBOL",
		},
	}
	pivot, err := semanticObservationPivot(column, "observation", "observation_component_values__GENE_SYMBOL")
	if err != nil {
		t.Fatal(err)
	}
	if pivot.Name != "observation_component_values" || len(pivot.Columns) != 1 || pivot.Columns[0] != "GENE_SYMBOL" || pivot.ItemResourceType != "ObservationComponent" {
		t.Fatalf("pivot = %#v", pivot)
	}
	merged := appendSemanticPivot([]recipe.Pivot{pivot}, recipe.Pivot{Name: pivot.Name, Columns: []string{"TIMEPOINT_LABEL"}})
	if len(merged) != 1 || len(merged[0].Columns) != 2 {
		t.Fatalf("merged pivots = %#v", merged)
	}
}

func TestSemanticNestedOccurrenceUsesGloballyScopedAlias(t *testing.T) {
	if got := semanticAlias("patient__condition"); got != "patient__condition" {
		t.Fatalf("nested alias = %q", got)
	}
	leaf, err := semanticColumnLeaf("patient__condition__code_coding_code", "patient__condition")
	if err != nil || leaf != "code_coding_code" {
		t.Fatalf("nested physical leaf = %q, %v", leaf, err)
	}
}

func TestCompileSemanticAggregateUsesExactPublicName(t *testing.T) {
	visible := true
	document := authoringv2.Document{
		Kind:             authoringv2.Kind,
		Output:           authoringv2.Output{ID: "patient_output", Title: "Patients"},
		RootResourceType: "Patient",
		Route: authoringv2.RouteNode{OccurrenceID: "base", ResourceType: "Patient", Children: []authoringv2.RouteNode{{
			OccurrenceID: "encounter", ResourceType: "Encounter", Relationship: "encounters",
		}}},
		Columns: []authoringv2.Column{
			{Column: "patient_id", Label: "Patient ID", OccurrenceID: "base", Source: authoringv2.ColumnSource{Kind: authoringv2.SourceField, FieldPath: "id", ProjectionMode: "VALUE"}, Table: &authoringv2.TablePresentation{Visible: &visible}},
			{Column: "encounter_count", Label: "Encounter count", OccurrenceID: "encounter", Source: authoringv2.ColumnSource{Kind: authoringv2.SourceAggregate, Operation: "COUNT"}, Table: &authoringv2.TablePresentation{Visible: &visible}},
		},
	}

	result, err := Compile(context.Background(), "project-a", "explorer-a", document, fixtureSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	aggregate := result.Bundle.Outputs[0].Traversals[0].Aggregates[0]
	if aggregate.OutputName != "encounter_count" || aggregate.Operation != recipe.AggregateCount {
		t.Fatalf("aggregate = %#v", aggregate)
	}
}
