package compilation

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/semantic"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
)

func TestCompileExpandedRowsBindsDeepExactOccurrenceAndIdentity(t *testing.T) {
	snapshot := expandedRowsSnapshot()
	document := expandedRowsDocument("second_patient", "name[]")
	document.Route.Children = []authoringv2.RouteNode{{
		OccurrenceID: "first_condition", ResourceType: "Condition", Relationship: "subject_Patient", CatalogEdgeID: "e_condition",
		Children: []authoringv2.RouteNode{{
			OccurrenceID: "second_patient", ResourceType: "Patient", Relationship: "subject_Patient", CatalogEdgeID: "e_patient_return",
		}},
	}}

	compiled, err := Compile(context.Background(), "project-a", "explorer-a", document, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if len(compiled.Bundle.Outputs) != 1 {
		t.Fatalf("compiled outputs=%d, want one", len(compiled.Bundle.Outputs))
	}
	output := compiled.Bundle.Outputs[0]
	if output.RootOccurrenceID != authoringv2.RootOccurrenceID || output.RowGrain != "expanded" {
		t.Fatalf("compiled root identity/grain = %q/%q", output.RootOccurrenceID, output.RowGrain)
	}
	if output.Expand == nil || output.Expand.OwnerOccurrenceID != "second_patient" || output.Expand.From.Select != "second_patient.name[]" || output.Expand.As != "__loom_expanded_item" || output.Expand.Ordinality != "__loom_expanded_ordinal" || output.Expand.EmptyPolicy != recipe.ExpansionPreserveParent {
		t.Fatalf("compiled expansion = %#v", output.Expand)
	}
	if output.Identity == nil || output.Identity.Name != "__loom_row_id" || output.Identity.Expansion == nil || output.Identity.Expr.Select != "" {
		t.Fatalf("compiled identity = %#v", output.Identity)
	}
	if len(output.Traversals) != 1 || output.Traversals[0].OccurrenceID != "first_condition" || len(output.Traversals[0].Traversals) != 1 || output.Traversals[0].Traversals[0].OccurrenceID != "second_patient" {
		t.Fatalf("compiled traversal occurrence IDs = %#v", output.Traversals)
	}
	if compiled.OutputContract.RowGrain != "expanded" || compiled.OutputContract.RowMultiplication != "expand" {
		t.Fatalf("compiled output contract = %#v", compiled.OutputContract)
	}
	if err := compiled.OutputContract.ValidateAgainst(compiled.Bundle, compiled.EmittedColumns); err != nil {
		t.Fatalf("expanded output contract does not match compiled evidence: %v", err)
	}

	plan, err := semantic.BuildRecipePlan(compiled.Bundle, recipe.RuntimeBindings{Project: "project-a"})
	if err != nil {
		t.Fatalf("BuildRecipePlan: %v", err)
	}
	resolved, err := semantic.ResolveRecipePlan(plan, "scope-a", "generation-a")
	if err != nil {
		t.Fatalf("ResolveRecipePlan: %v", err)
	}
	physical, err := lower.CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("CompileResolvedRecipePlan: %v", err)
	}
	rendered, err := aql.RenderPhysicalPlan(physical.Outputs[0].Plan)
	if err != nil {
		t.Fatalf("RenderPhysicalPlan: %v", err)
	}
	if strings.Count(rendered.Query, "FOR __loom_expansion_edge_") != 2 || !strings.Contains(rendered.Query, "__loom_expansion_edge_2._id") || !strings.Contains(rendered.Query, "__loom_has_expanded_item") || !strings.Contains(rendered.Query, "__loom_expanded_ordinal") {
		t.Fatalf("deep occurrence expansion evidence missing from rendered query:\n%s", rendered.Query)
	}
	if rendered.BindVars["expansion_identity_occurrence"] != "second_patient" {
		t.Fatalf("rendered expansion identity occurrence = %#v", rendered.BindVars["expansion_identity_occurrence"])
	}
}

func TestCompileExpandedRowsBindsRootOccurrenceAndMapsEmptyPolicies(t *testing.T) {
	policies := []struct {
		authored authoringv2.EmptyCollectionPolicy
		recipe   recipe.ExpansionEmptyPolicy
	}{
		{authoringv2.EmptyCollectionError, recipe.ExpansionError},
		{authoringv2.EmptyCollectionExclude, recipe.ExpansionExclude},
		{authoringv2.EmptyCollectionPreserveParent, recipe.ExpansionPreserveParent},
	}
	for _, policy := range policies {
		t.Run(string(policy.authored), func(t *testing.T) {
			document := expandedRowsDocument(authoringv2.RootOccurrenceID, "name[]")
			document.Rows.Expanded.EmptyCollectionPolicy = policy.authored
			compiled, err := Compile(context.Background(), "project-a", "explorer-a", document, expandedRowsSnapshot())
			if err != nil {
				t.Fatal(err)
			}
			output := compiled.Bundle.Outputs[0]
			if output.RootOccurrenceID != authoringv2.RootOccurrenceID || output.Expand == nil || output.Expand.OwnerOccurrenceID != authoringv2.RootOccurrenceID || output.Expand.From.Select != "root.name[]" || output.Expand.EmptyPolicy != policy.recipe {
				t.Fatalf("root expansion = %#v", output.Expand)
			}
			if output.Identity == nil || output.Identity.Expansion == nil || compiled.OutputContract.RowMultiplication != "expand" {
				t.Fatalf("expanded identity/contract missing: identity=%#v contract=%#v", output.Identity, compiled.OutputContract)
			}
		})
	}
}

func TestCompileExplicitGroupRowsPinsRevisionAndPolicy(t *testing.T) {
	document := expandedRowsDocument(authoringv2.RootOccurrenceID, "name[]")
	document.Rows = authoringv2.RowDefinition{Kind: authoringv2.RowDefinitionGroups, Groups: &authoringv2.GroupedRows{Source: authoringv2.GroupSource{
		Kind:     authoringv2.GroupSourceExplicit,
		Explicit: &authoringv2.ExplicitGroupSource{RevisionID: "grouprev_test", UnassignedMemberPolicy: authoringv2.UnassignedMemberGroupAsUnassigned},
	}}}
	compiled, err := Compile(context.Background(), "project-a", "explorer-a", document, expandedRowsSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	output := compiled.Bundle.Outputs[0]
	if output.RowGrain != "groups" || output.GroupRows == nil || output.GroupRows.RevisionID != "grouprev_test" || output.GroupRows.UnassignedMemberPolicy != "GROUP_AS_UNASSIGNED" {
		t.Fatalf("compiled group rows = %#v", output)
	}
	if compiled.OutputContract.RowGrain != "groups" {
		t.Fatalf("group output contract = %#v", compiled.OutputContract)
	}
}

func TestCompileExpandedRowsRejectsStaleAmbiguousAndScalarScope(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*authoringv2.Document, *capability.Snapshot)
		code   string
	}{
		{
			name: "stale occurrence",
			mutate: func(document *authoringv2.Document, _ *capability.Snapshot) {
				document.Rows.Expanded.OccurrenceID = "missing_occurrence"
			},
			code: "STALE_EXPANSION_OCCURRENCE",
		},
		{
			name: "scalar scope",
			mutate: func(document *authoringv2.Document, _ *capability.Snapshot) {
				document.Rows.Expanded.ScopePath = "id"
			},
			code: "UNSUPPORTED_EXPANSION_SCOPE",
		},
		{
			name: "ambiguous capability scope",
			mutate: func(_ *authoringv2.Document, snapshot *capability.Snapshot) {
				snapshot.Candidates = append(snapshot.Candidates, capability.Candidate{
					ID: "c_patient_names_duplicate", NodeID: "n_patient", ResourceType: "Patient", FieldPath: "name[]",
					LogicalType: "HumanName", Cardinality: "many", RepeatedBoundaries: []capability.RepeatedBoundary{{Path: "name[]", MaxItems: 2}},
					ProjectionModes: []capability.ProjectionMode{capability.ProjectionArray}, SupportedOperations: []capability.Operation{capability.OperationSelect},
				})
				*snapshot = capability.NewSnapshot(snapshot.Identity, snapshot.Policy, snapshot.Status, snapshot.Complete, snapshot.Truncated, snapshot.Nodes, snapshot.Edges, snapshot.Candidates, snapshot.Diagnostics)
			},
			code: "AMBIGUOUS_EXPANSION_SCOPE",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			snapshot := expandedRowsSnapshot()
			document := expandedRowsDocument(authoringv2.RootOccurrenceID, "name[]")
			test.mutate(&document, &snapshot)
			_, err := Compile(context.Background(), "project-a", "explorer-a", document, snapshot)
			var compileErr *Error
			if !errors.As(err, &compileErr) || compileErr.Code != test.code {
				t.Fatalf("Compile error = %v, want code %q", err, test.code)
			}
		})
	}
}

func expandedRowsDocument(owner, scopePath string) authoringv2.Document {
	return authoringv2.Document{
		Rows: authoringv2.RowDefinition{Kind: authoringv2.RowDefinitionExpanded, Expanded: &authoringv2.ExpandedRows{
			OccurrenceID: owner, ScopePath: scopePath, EmptyCollectionPolicy: authoringv2.EmptyCollectionPreserveParent,
		}},
		Kind: authoringv2.Kind, Output: authoringv2.Output{ID: "patients", Title: "Patients"}, RootResourceType: "Patient",
		Route: authoringv2.RouteNode{OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "Patient"},
		Columns: []authoringv2.Column{{
			Column: "patient_id", Label: "Patient ID", OccurrenceID: authoringv2.RootOccurrenceID,
			Source: authoringv2.ColumnSource{Kind: authoringv2.SourceField, Field: &authoringv2.FieldSource{Path: "id", ProjectionMode: "VALUE"}},
		}},
	}
}

func expandedRowsSnapshot() capability.Snapshot {
	snapshot := fixtureSnapshot()
	snapshot.Nodes = append(snapshot.Nodes, capability.Node{ID: "n_condition", ResourceType: "Condition", RowRootEligible: true, RowGrain: "condition"})
	for index := range snapshot.Edges {
		switch snapshot.Edges[index].ID {
		case "e_encounter":
			snapshot.Edges[index].SourceResourceType = "Patient"
			snapshot.Edges[index].TargetResourceType = "Encounter"
			snapshot.Edges[index].StorageDirection = "OUTBOUND"
		case "e_self":
			snapshot.Edges[index].SourceResourceType = "Encounter"
			snapshot.Edges[index].TargetResourceType = "Encounter"
			snapshot.Edges[index].StorageDirection = "OUTBOUND"
		}
	}
	snapshot.Edges = append(snapshot.Edges,
		capability.Edge{ID: "e_condition", FromNodeID: "n_patient", ToNodeID: "n_condition", Label: "subject_Patient", SourceResourceType: "Patient", TargetResourceType: "Condition", StorageDirection: "OUTBOUND"},
		capability.Edge{ID: "e_patient_return", FromNodeID: "n_condition", ToNodeID: "n_patient", Label: "subject_Patient", SourceResourceType: "Condition", TargetResourceType: "Patient", StorageDirection: "OUTBOUND"},
	)
	snapshot.Candidates = append(snapshot.Candidates, capability.Candidate{
		ID: "c_patient_names", NodeID: "n_patient", ResourceType: "Patient", FieldPath: "name[]", Label: "Patient names",
		LogicalType: "HumanName", Cardinality: "many", RepeatedBoundaries: []capability.RepeatedBoundary{{Path: "name[]", MaxItems: 2}},
		ProjectionModes: []capability.ProjectionMode{capability.ProjectionArray}, SupportedOperations: []capability.Operation{capability.OperationSelect},
	})
	return capability.NewSnapshot(snapshot.Identity, snapshot.Policy, snapshot.Status, snapshot.Complete, snapshot.Truncated, snapshot.Nodes, snapshot.Edges, snapshot.Candidates, snapshot.Diagnostics)
}
