package lifecycle

import (
	"context"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
)

func TestPopulationRouteChoicePersistsAndRevalidatesStorageDirection(t *testing.T) {
	store, service, snapshot, _, _ := inboundPatientObservationRouteFixture(t)
	store.selection = completedTestSelection(snapshot, "Observation")
	routes, err := service.SearchPopulationRoutes(context.Background(), PopulationRoutesRequest{
		Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token,
		OutputID: "patients", SelectionRevisionID: "selection-1",
	})
	if err != nil || !routes.Complete || len(routes.Choices) != 1 {
		t.Fatalf("population route choices = %#v, %v", routes, err)
	}
	identity, err := capability.DecodePopulationRouteChoiceID(routes.Choices[0].RouteChoiceID)
	if err != nil || len(identity.Route) != 1 || identity.Route[0].StorageDirection != "INBOUND" {
		t.Fatalf("signed route direction = %#v, %v", identity.Route, err)
	}
	_, err = service.ApplyCommands(context.Background(), "project-a", "patients", authoringv2.ApplyCommandsRequest{
		CommandID: "set-population-direction", SemanticsVersion: authoringv2.CurrentSemanticsVersion,
		SnapshotToken: snapshot.Token, ExpectedDraftVersion: store.created.DraftVersion,
		ExpectedDraftDigest: store.created.DraftDigest,
		Commands: []authoringv2.Command{{
			Type: authoringv2.CommandSetTablePopulation, OutputID: "patients",
			SelectionRevisionID: "selection-1", RouteChoiceID: routes.Choices[0].RouteChoiceID,
		}},
	}, "alice")
	if err != nil {
		t.Fatalf("apply signed population route: %v", err)
	}
	workspace, err := authoringv2.DecodeWorkspace(store.created.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	step := workspace.Documents[0].Population.Route[0]
	if step.CatalogEdgeID != "subject-patient" || step.StorageDirection != "INBOUND" {
		t.Fatalf("saved population route lost signed edge direction: %#v", step)
	}
	inputs, err := service.resolveWorkspacePopulations(context.Background(), "project-a", workspace, snapshot, snapshot.Identity.AuthorizationScopeDigest)
	if err != nil || len(inputs.Populations) != 1 || inputs.Populations[0].Route[0].StorageDirection != "INBOUND" {
		t.Fatalf("resolved population route lost signed direction: %#v, %v", inputs.Populations, err)
	}

	workspace.Documents[0].Population.Route[0].StorageDirection = "OUTBOUND"
	if _, err := service.resolveWorkspacePopulations(context.Background(), "project-a", workspace, snapshot, snapshot.Identity.AuthorizationScopeDigest); err == nil || !strings.Contains(err.Error(), "catalog edge") {
		t.Fatalf("substituted saved direction was not rejected: %v", err)
	}
}

func TestLegacyPopulationRouteHydratesDirectionOnlyForResolvedInputs(t *testing.T) {
	store, service, snapshot, _, _ := inboundPatientObservationRouteFixture(t)
	store.selection = completedTestSelection(snapshot, "Observation")
	routes, err := service.SearchPopulationRoutes(context.Background(), PopulationRoutesRequest{
		Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token, OutputID: "patients",
		SelectionRevisionID: "selection-1",
	})
	if err != nil || len(routes.Choices) != 1 {
		t.Fatalf("population route choices = %#v, %v", routes, err)
	}
	_, err = service.ApplyCommands(context.Background(), "project-a", "patients", authoringv2.ApplyCommandsRequest{
		CommandID: "set-legacy-route-source", SemanticsVersion: authoringv2.CurrentSemanticsVersion,
		SnapshotToken: snapshot.Token, ExpectedDraftVersion: store.created.DraftVersion,
		ExpectedDraftDigest: store.created.DraftDigest,
		Commands: []authoringv2.Command{{
			Type: authoringv2.CommandSetTablePopulation, OutputID: "patients",
			SelectionRevisionID: "selection-1", RouteChoiceID: routes.Choices[0].RouteChoiceID,
		}},
	}, "alice")
	if err != nil {
		t.Fatalf("apply population route: %v", err)
	}
	workspace, err := authoringv2.DecodeWorkspace(store.created.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	workspace.Documents[0].Population.Route[0].StorageDirection = ""
	if workspace.Documents[0].Population.Route[0].CatalogEdgeID != "subject-patient" {
		t.Fatalf("legacy route lost exact catalog edge: %#v", workspace.Documents[0].Population.Route[0])
	}

	inputs, err := service.resolveWorkspacePopulations(context.Background(), "project-a", workspace, snapshot, snapshot.Identity.AuthorizationScopeDigest)
	if err != nil || len(inputs.Populations) != 1 {
		t.Fatalf("resolve legacy population route = %#v, %v", inputs.Populations, err)
	}
	if got := inputs.Populations[0].Route[0].StorageDirection; got != "INBOUND" {
		t.Fatalf("resolved legacy direction = %q, want INBOUND", got)
	}
	if got := workspace.Documents[0].Population.Route[0].StorageDirection; got != "" {
		t.Fatalf("resolution mutated authored legacy route direction to %q", got)
	}

	workspace.Documents[0].Population.Route[0].CatalogEdgeID = "stale-edge"
	if _, err := service.resolveWorkspacePopulations(context.Background(), "project-a", workspace, snapshot, snapshot.Identity.AuthorizationScopeDigest); err == nil || !strings.Contains(err.Error(), "catalog edge") {
		t.Fatalf("stale legacy catalog edge was accepted: %v", err)
	}

	workspace.Documents[0].Population.Route[0].CatalogEdgeID = "subject-patient"
	blockedSnapshot := snapshot.Clone()
	blockedSnapshot.Edges[0].BlockedReason = "relationship is no longer authorized"
	if _, err := service.resolveWorkspacePopulations(context.Background(), "project-a", workspace, blockedSnapshot, snapshot.Identity.AuthorizationScopeDigest); err == nil || !strings.Contains(err.Error(), "no longer identifies this route step") {
		t.Fatalf("blocked legacy catalog edge was accepted: %v", err)
	}
}
