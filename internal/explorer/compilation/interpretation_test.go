package compilation

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
)

func TestResolveInterpretationsSelectsUniquePriorityRule(t *testing.T) {
	snapshot := interpretationSnapshot()
	low := explorer.InterpretationPriority(1)
	high := explorer.InterpretationPriority(2)
	revision := prepareInterpretation(t, explorer.InterpretationRevision{
		ID: "revision-priority", Project: "project-a", LibraryID: "library-a", Author: "tester", Explanation: "priority",
		Applicability: explorer.InterpretationApplicability{ResourceTypes: []string{"Patient"}, LogicalTypes: []string{"string"}},
		Rules: []explorer.InterpretationRule{
			{ID: "rule-low", Priority: &low, Match: explorer.InterpretationStructuralMatch{ResourceType: "Patient", LogicalType: "string"}, Definition: fieldDefinition("id")},
			{ID: "rule-high", Priority: &high, Match: explorer.InterpretationStructuralMatch{ResourceType: "Patient", LogicalType: "string"}, Definition: fieldDefinition("name.family")},
		},
	})
	workspace := interpretationWorkspace(string(revision.ID))
	inputs, err := ResolveWorkspaceInterpretations("project-a", workspace, snapshot, map[explorer.InterpretationRevisionID]explorer.InterpretationRevision{revision.ID: revision})
	if err != nil {
		t.Fatal(err)
	}
	if len(inputs.Interpretations) != 1 || inputs.Interpretations[0].SelectedRuleID != "rule-high" {
		t.Fatalf("resolved interpretations = %#v", inputs.Interpretations)
	}
}

func TestResolveInterpretationsRejectsMissingInapplicableAndAmbiguous(t *testing.T) {
	snapshot := interpretationSnapshot()
	workspace := interpretationWorkspace("revision-missing")
	if _, err := ResolveWorkspaceInterpretations("project-a", workspace, snapshot, nil); err == nil {
		t.Fatal("missing revision was accepted")
	}
	inapplicable := prepareInterpretation(t, explorer.InterpretationRevision{
		ID: "revision-inapplicable", Project: "project-a", LibraryID: "library-a", Author: "tester", Explanation: "wrong resource",
		Applicability: explorer.InterpretationApplicability{ResourceTypes: []string{"Observation"}},
		Rules:         []explorer.InterpretationRule{{ID: "rule", Match: explorer.InterpretationStructuralMatch{ResourceType: "Observation"}, Definition: fieldDefinition("id")}},
	})
	workspace = interpretationWorkspace(string(inapplicable.ID))
	if _, err := ResolveWorkspaceInterpretations("project-a", workspace, snapshot, map[explorer.InterpretationRevisionID]explorer.InterpretationRevision{inapplicable.ID: inapplicable}); err == nil {
		t.Fatal("inapplicable revision was accepted")
	}
	first := explorer.InterpretationPriority(1)
	second := explorer.InterpretationPriority(1)
	ambiguous := explorer.InterpretationRevision{Project: "project-a", LibraryID: "library-a", Author: "tester", Explanation: "tie", Rules: []explorer.InterpretationRule{
		{ID: "rule-a", Priority: &first, Match: explorer.InterpretationStructuralMatch{ResourceType: "Patient"}, Definition: fieldDefinition("id")},
		{ID: "rule-b", Priority: &second, Match: explorer.InterpretationStructuralMatch{ResourceType: "Patient"}, Definition: fieldDefinition("name.family")},
	}}
	if _, err := ambiguous.SelectRule(explorer.InterpretationStructuralCandidate{ResourceType: "Patient"}); !errors.Is(err, explorer.ErrAmbiguousMapping) {
		t.Fatalf("ambiguous selection error = %v", err)
	}
}

func TestCompileWorkspaceUsesResolvedDefinitionAndResolvedDigest(t *testing.T) {
	snapshot := interpretationSnapshot()
	revision := prepareInterpretation(t, explorer.InterpretationRevision{
		ID: "revision-source", Project: "project-a", LibraryID: "library-a", Author: "tester", Explanation: "replace suggestion",
		Rules: []explorer.InterpretationRule{{ID: "rule", Match: explorer.InterpretationStructuralMatch{ResourceType: "Patient", LogicalType: "string"}, Definition: fieldDefinition("name.family")}},
	})
	workspace := interpretationWorkspace(string(revision.ID))
	inputs, err := ResolveWorkspaceInterpretations("project-a", workspace, snapshot, map[explorer.InterpretationRevisionID]explorer.InterpretationRevision{revision.ID: revision})
	if err != nil {
		t.Fatal(err)
	}
	first, err := CompileWorkspace(context.Background(), "project-a", "patients", workspace, snapshot, inputs)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Bundle.Outputs) != 1 || len(first.Bundle.Outputs[0].Fields) != 1 || !strings.Contains(first.Bundle.Outputs[0].Fields[0].Expr.Select, "name.family") {
		t.Fatalf("compiled source = %#v", first.Bundle.Outputs[0].Fields)
	}
	if strings.Contains(first.Bundle.Outputs[0].Fields[0].Expr.Select, "root.id") {
		t.Fatalf("original source suggestion remained executable: %#v", first.Bundle.Outputs[0].Fields[0].Expr)
	}
	_, err = CompileWorkspace(context.Background(), "project-a", "patients", workspace, snapshot, ResolvedInputs{})
	if err == nil {
		t.Fatal("pinned workspace compiled without resolved interpretation")
	}
	changed := revision
	changed.Rules = append([]explorer.InterpretationRule(nil), revision.Rules...)
	changed.Rules[0].Definition = fieldDefinition("id")
	changed, err = explorer.PrepareInterpretationRevision(changed)
	if err != nil {
		t.Fatal(err)
	}
	changedInputs, err := ResolveWorkspaceInterpretations("project-a", workspaceWithRevision(string(changed.ID)), snapshot, map[explorer.InterpretationRevisionID]explorer.InterpretationRevision{changed.ID: changed})
	if err != nil {
		t.Fatal(err)
	}
	second, err := CompileWorkspace(context.Background(), "project-a", "patients", workspaceWithRevision(string(changed.ID)), snapshot, changedInputs)
	if err != nil {
		t.Fatal(err)
	}
	if first.ResolvedInputsDigest == second.ResolvedInputsDigest || first.RecipeDigest == second.RecipeDigest {
		t.Fatalf("resolved definition change did not change identity: %q/%q vs %q/%q", first.ResolvedInputsDigest, first.RecipeDigest, second.ResolvedInputsDigest, second.RecipeDigest)
	}
	stable, err := CompileWorkspace(context.Background(), "project-a", "patients", workspace, snapshot, inputs)
	if err != nil || stable.ResolvedInputsDigest != first.ResolvedInputsDigest || stable.RecipeDigest != first.RecipeDigest {
		t.Fatalf("same resolved content was unstable: stable=%#v err=%v first=%#v", stable, err, first)
	}
}

func interpretationSnapshot() capability.Snapshot {
	return capability.NewSnapshot(
		capability.SnapshotIdentity{Project: "project-a", Generation: "generation-a", AuthorizationScopeDigest: "scope", SchemaDigest: "schema", ShapeDigest: "shape"},
		capability.Policy{Route: capability.RoutePolicy{}, Projection: capability.ProjectionPolicy{Modes: []capability.ProjectionMode{capability.ProjectionScalar}}}, capability.StatusReady, true, false,
		[]capability.Node{{ID: "node-patient", ResourceType: "Patient", RowRootEligible: true, RowGrain: "patient"}}, nil,
		[]capability.Candidate{
			{ID: "candidate-id", NodeID: "node-patient", ResourceType: "Patient", FieldPath: "id", LogicalType: "string", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar}, SupportedOperations: []capability.Operation{capability.OperationSelect}},
			{ID: "candidate-family", NodeID: "node-patient", ResourceType: "Patient", FieldPath: "name.family", LogicalType: "string", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar}, SupportedOperations: []capability.Operation{capability.OperationSelect}},
		}, nil)
}

func interpretationWorkspace(revisionID string) authoringv2.Workspace {
	return workspaceWithRevision(revisionID)
}

func workspaceWithRevision(revisionID string) authoringv2.Workspace {
	return authoringv2.Workspace{
		APIVersion: authoringv2.APIVersion, Kind: authoringv2.WorkspaceKind, Explorer: authoringv2.ExplorerMetadata{Title: "Interpretations"},
		Documents: []authoringv2.Document{{Kind: authoringv2.Kind, Output: authoringv2.Output{ID: "patients", Title: "Patients"}, RootResourceType: "Patient", Route: authoringv2.RouteNode{OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "Patient"}, Columns: []authoringv2.Column{{Column: "patient_id", Label: "Patient ID", OccurrenceID: authoringv2.RootOccurrenceID, Source: authoringv2.ColumnSource{Kind: authoringv2.SourceField, Field: &authoringv2.FieldSource{Path: "id", ProjectionMode: "VALUE"}}, Interpretation: &authoringv2.FeatureInterpretation{Kind: authoringv2.FeatureInterpretationPinned, Pinned: &authoringv2.PinnedInterpretation{RevisionID: revisionID}}}}}},
		Tabs:      []authoringv2.Tab{{ID: "patients", Title: "Patients", OutputID: "patients", Order: 0, Visible: true}},
	}
}

func fieldDefinition(path string) explorer.InterpretationFeatureDefinition {
	return explorer.InterpretationFeatureDefinition{Source: authoringv2.ColumnSource{Kind: authoringv2.SourceField, Field: &authoringv2.FieldSource{Path: path, ProjectionMode: "VALUE"}}}
}

func prepareInterpretation(t *testing.T, revision explorer.InterpretationRevision) explorer.InterpretationRevision {
	t.Helper()
	if revision.CreatedAt.IsZero() {
		revision.CreatedAt = time.Unix(1, 0).UTC()
	}
	prepared, err := explorer.PrepareInterpretationRevision(revision)
	if err != nil {
		t.Fatal(err)
	}
	return prepared
}
