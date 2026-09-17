package lifecycle

import (
	"context"
	"errors"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
)

func TestAssessRowChangeBindsExactDraftWithoutWriting(t *testing.T) {
	snapshot := readySnapshot("project-a", "generation-a", "token", authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted})
	workspace := authoringv2.Workspace{
		APIVersion: authoringv2.APIVersion,
		Kind:       authoringv2.WorkspaceKind,
		Explorer:   authoringv2.ExplorerMetadata{Title: "Patients"},
		Documents: []authoringv2.Document{{
			Kind: authoringv2.Kind, Output: authoringv2.Output{ID: "patients", Title: "Patients"}, RootResourceType: "Patient",
			Route:   authoringv2.RouteNode{OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "Patient", Children: []authoringv2.RouteNode{{OccurrenceID: "encounter", ResourceType: "Encounter", Relationship: "encounters"}}},
			Columns: []authoringv2.Column{{Column: "patient_id", Label: "Patient ID", OccurrenceID: authoringv2.RootOccurrenceID, Source: authoringv2.ColumnSource{Kind: authoringv2.SourceField, Field: &authoringv2.FieldSource{Path: "id", ProjectionMode: "VALUE"}}}},
		}},
		Tabs: []authoringv2.Tab{{ID: "patients", Title: "Patients", OutputID: "patients", Visible: true}},
	}
	draft, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	store := &fakeStore{created: &explorer.Explorer{Project: "project-a", ExplorerID: "patients", DraftConfig: draft, DraftVersion: 7, DraftDigest: digest}}
	config := testConfig(snapshot)
	config.Capability.Catalog = func(capability.Snapshot, string) authoringv2.CatalogSnapshot {
		return authoringv2.CatalogSnapshot{
			APIVersion: authoringv2.APIVersion, Kind: authoringv2.CatalogKind, Project: "project-a", ExplorerID: "patients",
			SourceGeneration: snapshot.Identity.Generation, AuthorizationScopeDigest: snapshot.Identity.AuthorizationScopeDigest, SnapshotToken: snapshot.Token, Complete: true,
			RoutePolicy: authoringv2.RoutePolicy{Unbounded: true},
			Nodes: []authoringv2.CatalogNode{
				{ID: "patient", ResourceType: "Patient", RowRootEligible: true},
				{ID: "encounter", ResourceType: "Encounter", RowRootEligible: true},
			},
			Edges: []authoringv2.CatalogEdge{
				{ID: "patient-encounter", FromNodeID: "patient", ToNodeID: "encounter", Label: "encounters"},
				{ID: "encounter-patient", FromNodeID: "encounter", ToNodeID: "patient", Label: "patient"},
			},
		}
	}
	service := newTestService(t, store, config)
	result, err := service.AssessRowChange(context.Background(), AssessRowChangeRequest{
		Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token, DraftVersion: 7, DraftDigest: digest,
		OutputID: "patients", RootNodeID: "encounter",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.DraftVersion != 7 || result.DraftDigest != digest || result.Assessment.Status != authoringv2.RowChangeReady || result.Assessment.Proposal == nil {
		t.Fatalf("row change result=%#v", result)
	}
	if store.created.DraftVersion != 7 || store.created.DraftDigest != digest || string(store.created.DraftConfig) != string(draft) {
		t.Fatalf("assessment wrote draft=%#v", store.created)
	}

	_, err = service.AssessRowChange(context.Background(), AssessRowChangeRequest{
		Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token, DraftVersion: 6, DraftDigest: digest,
		OutputID: "patients", RootNodeID: "encounter",
	})
	var lifecycleErr *Error
	if !errors.As(err, &lifecycleErr) || lifecycleErr.Code != "DRAFT_CONFLICT" {
		t.Fatalf("stale draft error=%v", err)
	}
}
