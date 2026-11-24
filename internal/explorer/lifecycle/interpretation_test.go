package lifecycle

import (
	"context"
	"errors"
	"testing"
	"time"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/explorer/interpretation"

	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/capability"
)

type countingInterpretationRepository struct {
	revision interpretation.InterpretationRevision
	reads    int
}

func (r *countingInterpretationRepository) ListInterpretationLibraries(context.Context, string) ([]interpretation.InterpretationLibrary, error) {
	return nil, nil
}

func (r *countingInterpretationRepository) GetInterpretationRevision(_ context.Context, _ string, id interpretation.InterpretationRevisionID) (*interpretation.InterpretationRevision, error) {
	r.reads++
	if id != r.revision.ID {
		return nil, explorer.ErrNotFound
	}
	value := r.revision
	return &value, nil
}

func (r *countingInterpretationRepository) CreateInterpretationRevision(context.Context, interpretation.InterpretationRevision, *interpretation.InterpretationRevisionID) (*interpretation.InterpretationRevision, error) {
	return nil, errors.New("not used")
}

func TestLifecycleResolvesEachDistinctRevisionOnce(t *testing.T) {
	revision := lifecyclePrepareInterpretation(t)
	repository := &countingInterpretationRepository{revision: revision}
	service := newTestService(t, &fakeStore{}, Config{InterpretationRepository: repository})
	inputs, err := service.core.ResolveWorkspaceInterpretations(context.Background(), "project-a", lifecycleInterpretationWorkspace(string(revision.ID), false), lifecycleInterpretationSnapshot())
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
	inputs, err := service.core.ResolveWorkspaceInterpretations(context.Background(), "project-a", lifecycleInterpretationWorkspace("", true), lifecycleInterpretationSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	if len(inputs.Interpretations) != 0 {
		t.Fatalf("inline workspace unexpectedly resolved interpretations: %#v", inputs.Interpretations)
	}
}

func TestLifecycleRejectsMissingPinnedRevision(t *testing.T) {
	service := newTestService(t, &fakeStore{}, Config{InterpretationRepository: &countingInterpretationRepository{}})
	_, err := service.core.ResolveWorkspaceInterpretations(context.Background(), "project-a", lifecycleInterpretationWorkspace("revision-missing", false), lifecycleInterpretationSnapshot())
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

func lifecycleInterpretationWorkspace(revisionID string, inline bool) ws.Workspace {
	column := ws.Column{Column: "patient_id", Label: "Patient ID", OccurrenceID: ws.RootOccurrenceID, Source: ws.ColumnSource{Kind: ws.SourceField, Field: &ws.FieldSource{Path: "id", ProjectionMode: "VALUE"}}}
	if !inline {
		column.Interpretation = &ws.FeatureInterpretation{Kind: ws.FeatureInterpretationPinned, Pinned: &ws.PinnedInterpretation{RevisionID: revisionID}}
	}
	return ws.Workspace{
		APIVersion: ws.APIVersion, Kind: ws.WorkspaceKind, Explorer: ws.ExplorerMetadata{Title: "Interpretations"},
		Documents: []ws.Document{
			{Kind: ws.Kind, Output: ws.Output{ID: "patients_a", Title: "Patients A"}, RootResourceType: "Patient", Route: ws.RouteNode{OccurrenceID: ws.RootOccurrenceID, ResourceType: "Patient"}, Columns: []ws.Column{column}, Rows: ws.RecordsRowDefinition()},
			{Kind: ws.Kind, Output: ws.Output{ID: "patients_b", Title: "Patients B"}, RootResourceType: "Patient", Route: ws.RouteNode{OccurrenceID: ws.RootOccurrenceID, ResourceType: "Patient"}, Columns: []ws.Column{column}, Rows: ws.RecordsRowDefinition()},
		},
		Tabs: []ws.Tab{{ID: "patients_a", Title: "Patients A", OutputID: "patients_a", Order: 0, Visible: true}, {ID: "patients_b", Title: "Patients B", OutputID: "patients_b", Order: 1, Visible: true}}, SemanticsVersion: ws.CurrentSemanticsVersion,
	}
}

func lifecyclePrepareInterpretation(t *testing.T) interpretation.InterpretationRevision {
	t.Helper()
	revision, err := interpretation.PrepareInterpretationRevision(interpretation.InterpretationRevision{
		Project: "project-a", LibraryID: "library-a", Author: "tester", Explanation: "lifecycle",
		CreatedAt: time.Unix(1, 0).UTC(), Rules: []interpretation.InterpretationRule{{ID: "rule", Match: interpretation.InterpretationStructuralMatch{ResourceType: "Patient", LogicalType: "string"}, Definition: interpretation.InterpretationFeatureDefinition{Source: ws.ColumnSource{Kind: ws.SourceField, Field: &ws.FieldSource{Path: "id", ProjectionMode: "VALUE"}}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return revision
}
