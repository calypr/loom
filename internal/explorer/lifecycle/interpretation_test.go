package lifecycle

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
)

type countingInterpretationRepository struct {
	revision explorer.InterpretationRevision
	reads    int
}

func (r *countingInterpretationRepository) ListInterpretationLibraries(context.Context, string) ([]explorer.InterpretationLibrary, error) {
	return nil, nil
}

func (r *countingInterpretationRepository) GetInterpretationRevision(_ context.Context, _ string, id explorer.InterpretationRevisionID) (*explorer.InterpretationRevision, error) {
	r.reads++
	if id != r.revision.ID {
		return nil, explorer.ErrNotFound
	}
	value := r.revision
	return &value, nil
}

func (r *countingInterpretationRepository) CreateInterpretationRevision(context.Context, explorer.InterpretationRevision, *explorer.InterpretationRevisionID) (*explorer.InterpretationRevision, error) {
	return nil, errors.New("not used")
}

func TestLifecycleResolvesEachDistinctRevisionOnce(t *testing.T) {
	revision := lifecyclePrepareInterpretation(t)
	repository := &countingInterpretationRepository{revision: revision}
	service := newTestService(t, &fakeStore{}, Config{InterpretationRepository: repository})
	inputs, err := service.resolveWorkspaceInterpretations(context.Background(), "project-a", lifecycleInterpretationWorkspace(string(revision.ID), false), lifecycleInterpretationSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	if repository.reads != 1 {
		t.Fatalf("revision reads = %d, want one read for the distinct revision", repository.reads)
	}
	if len(inputs.Interpretations) != 2 {
		t.Fatalf("resolved interpretations = %#v", inputs.Interpretations)
	}
}

func TestLifecycleDoesNotReadRepositoryForInlineWorkspace(t *testing.T) {
	service := newTestService(t, &fakeStore{}, Config{})
	inputs, err := service.resolveWorkspaceInterpretations(context.Background(), "project-a", lifecycleInterpretationWorkspace("", true), lifecycleInterpretationSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	if len(inputs.Interpretations) != 0 {
		t.Fatalf("inline workspace unexpectedly resolved interpretations: %#v", inputs.Interpretations)
	}
}

func TestLifecycleRejectsMissingPinnedRevision(t *testing.T) {
	service := newTestService(t, &fakeStore{}, Config{InterpretationRepository: &countingInterpretationRepository{}})
	_, err := service.resolveWorkspaceInterpretations(context.Background(), "project-a", lifecycleInterpretationWorkspace("revision-missing", false), lifecycleInterpretationSnapshot())
	if !errors.Is(err, explorer.ErrNotFound) {
		t.Fatalf("missing revision error = %v", err)
	}
}

func lifecycleInterpretationSnapshot() capability.Snapshot {
	return capability.NewSnapshot(
		capability.SnapshotIdentity{Project: "project-a", Generation: "generation-a", AuthorizationScopeDigest: "scope", SchemaDigest: "schema", ShapeDigest: "shape"},
		capability.Policy{Projection: capability.ProjectionPolicy{Modes: []capability.ProjectionMode{capability.ProjectionScalar}}}, capability.StatusReady, true, false,
		[]capability.Node{{ID: "node-patient", ResourceType: "Patient", RowRootEligible: true, RowGrain: "patient"}}, nil,
		[]capability.Candidate{{ID: "candidate-id", NodeID: "node-patient", ResourceType: "Patient", FieldPath: "id", LogicalType: "string", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar}, SupportedOperations: []capability.Operation{capability.OperationSelect}}}, nil)
}

func lifecycleInterpretationWorkspace(revisionID string, inline bool) authoringv2.Workspace {
	column := authoringv2.Column{Column: "patient_id", Label: "Patient ID", OccurrenceID: authoringv2.RootOccurrenceID, Source: authoringv2.ColumnSource{Kind: authoringv2.SourceField, Field: &authoringv2.FieldSource{Path: "id", ProjectionMode: "VALUE"}}}
	if !inline {
		column.Interpretation = &authoringv2.FeatureInterpretation{Kind: authoringv2.FeatureInterpretationPinned, Pinned: &authoringv2.PinnedInterpretation{RevisionID: revisionID}}
	}
	return authoringv2.Workspace{
		APIVersion: authoringv2.APIVersion, Kind: authoringv2.WorkspaceKind, Explorer: authoringv2.ExplorerMetadata{Title: "Interpretations"},
		Documents: []authoringv2.Document{
			{Kind: authoringv2.Kind, Output: authoringv2.Output{ID: "patients_a", Title: "Patients A"}, RootResourceType: "Patient", Route: authoringv2.RouteNode{OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "Patient"}, Columns: []authoringv2.Column{column}},
			{Kind: authoringv2.Kind, Output: authoringv2.Output{ID: "patients_b", Title: "Patients B"}, RootResourceType: "Patient", Route: authoringv2.RouteNode{OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "Patient"}, Columns: []authoringv2.Column{column}},
		},
		Tabs: []authoringv2.Tab{{ID: "patients_a", Title: "Patients A", OutputID: "patients_a", Order: 0, Visible: true}, {ID: "patients_b", Title: "Patients B", OutputID: "patients_b", Order: 1, Visible: true}},
	}
}

func lifecyclePrepareInterpretation(t *testing.T) explorer.InterpretationRevision {
	t.Helper()
	revision, err := explorer.PrepareInterpretationRevision(explorer.InterpretationRevision{
		Project: "project-a", LibraryID: "library-a", Author: "tester", Explanation: "lifecycle",
		CreatedAt: time.Unix(1, 0).UTC(), Rules: []explorer.InterpretationRule{{ID: "rule", Match: explorer.InterpretationStructuralMatch{ResourceType: "Patient", LogicalType: "string"}, Definition: explorer.InterpretationFeatureDefinition{Source: authoringv2.ColumnSource{Kind: authoringv2.SourceField, Field: &authoringv2.FieldSource{Path: "id", ProjectionMode: "VALUE"}}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return revision
}
