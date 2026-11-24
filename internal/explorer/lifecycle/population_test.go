package lifecycle

import (
	"context"
	"errors"
	"strings"
	"testing"

	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/construction"
	"github.com/calypr/loom/internal/explorer/population"
	"github.com/calypr/loom/internal/explorer/receiptmodel"
	ws "github.com/calypr/loom/internal/explorer/workspace"
)

func TestPopulationRouteChoicePersistsAndRevalidatesStorageDirection(t *testing.T) {
	store, service, snapshot, _, _ := inboundPatientObservationRouteFixture(t)
	store.selection = completedTestSelection(snapshot, "Observation")
	routes, err := service.OwnerServices().Population.SearchPopulationRoutes(context.Background(), population.PopulationRoutesRequest{
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
		CommandID: "set-population-direction", SemanticsVersion: ws.CurrentSemanticsVersion,
		SnapshotToken: snapshot.Token, ExpectedDraftVersion: store.created.DraftVersion,
		ExpectedDraftDigest: store.created.DraftDigest,
		Commands:            []authoringv2.Command{{Type: authoringv2.CommandSetTablePopulation, OutputID: "patients", Payload: &authoringv2.TablePayload{SelectionRevisionID: "selection-1", RouteChoiceID: routes.Choices[0].RouteChoiceID}}},
	}, "alice")
	if err != nil {
		t.Fatalf("apply signed population route: %v", err)
	}
	workspace, err := ws.DecodeWorkspace(store.created.DraftConfig)
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
	routes, err := service.OwnerServices().Population.SearchPopulationRoutes(context.Background(), population.PopulationRoutesRequest{
		Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token, OutputID: "patients",
		SelectionRevisionID: "selection-1",
	})
	if err != nil || len(routes.Choices) != 1 {
		t.Fatalf("population route choices = %#v, %v", routes, err)
	}
	_, err = service.ApplyCommands(context.Background(), "project-a", "patients", authoringv2.ApplyCommandsRequest{
		CommandID: "set-legacy-route-source", SemanticsVersion: ws.CurrentSemanticsVersion,
		SnapshotToken: snapshot.Token, ExpectedDraftVersion: store.created.DraftVersion,
		ExpectedDraftDigest: store.created.DraftDigest,
		Commands:            []authoringv2.Command{{Type: authoringv2.CommandSetTablePopulation, OutputID: "patients", Payload: &authoringv2.TablePayload{SelectionRevisionID: "selection-1", RouteChoiceID: routes.Choices[0].RouteChoiceID}}},
	}, "alice")
	if err != nil {
		t.Fatalf("apply population route: %v", err)
	}
	workspace, err := ws.DecodeWorkspace(store.created.DraftConfig)
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

func TestConstructionColumnPopulationValidatesExactStageAndCachesOnlyConclusiveResults(t *testing.T) {
	var request construction.ConstructionColumnPopulationRequest
	var calls int
	fail := true
	service, store, snapshot := constructionProposalService(t, func(setup *testServiceSetup) {
		setup.Config.ScanColumnPopulation = func(_ context.Context, receipt *receiptmodel.CompilationReceipt, bindings recipe.RuntimeBindings, scan dataframeexecution.ColumnPopulationRequest) (dataframeexecution.ColumnPopulationResult, error) {
			calls++
			if receipt == nil || receipt.ID == "" || bindings.SelectionProject != setup.Store.created.Project || scan.Output != request.OutputID || scan.StageID != request.StageID || len(scan.ColumnIDs) != 2 {
				t.Fatalf("scanner was not bound to the exact request: receipt=%#v bindings=%#v request=%#v", receipt, bindings, scan)
			}
			if fail {
				fail = false
				return dataframeexecution.ColumnPopulationResult{}, errors.New("backend timeout")
			}
			return dataframeexecution.ColumnPopulationResult{Columns: []dataframeexecution.ColumnPopulation{
				{ColumnID: scan.ColumnIDs[0], Status: dataframeexecution.ColumnPopulationPopulated},
				{ColumnID: scan.ColumnIDs[1], Status: dataframeexecution.ColumnPopulationEmpty},
			}}, nil
		}
	})
	workspace, err := ws.DecodeWorkspace(store.created.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	for index := range workspace.Documents[0].Columns {
		workspace.Documents[0].Columns[index].LogicalType = "string"
	}
	workspace.Documents[0].Columns[0].ColumnID = "base-column"
	column := workspace.Documents[0].Columns[0]
	column.ColumnID = "status_column"
	column.Column = "status"
	column.Label = "Status"
	column.Source.Field.Path = "status"
	workspace.Documents[0].Columns = append(workspace.Documents[0].Columns, column)
	encoded, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	store.created.DraftConfig, store.created.DraftDigest = encoded, digest
	document := workspace.Documents[0]
	request = construction.ConstructionColumnPopulationRequest{
		Project: store.created.Project, ExplorerID: store.created.ExplorerID, SnapshotToken: snapshot.Token,
		ExpectedDraftVersion: store.created.DraftVersion, ExpectedDraftDigest: store.created.DraftDigest,
		OutputID: "patients", StageID: recipe.ConstructionSourceProjectionID,
		ColumnIDs: []string{document.Columns[0].ColumnID, document.Columns[1].ColumnID},
	}

	stale := request
	stale.StageID = "stale-stage"
	if _, err := service.OwnerServices().Construction.GetConstructionColumnPopulation(context.Background(), stale); lifecycleErrorCode(err) != "STALE_STAGE_REFERENCE" {
		t.Fatalf("stale stage error = %v", err)
	}
	stale = request
	stale.ColumnIDs = []string{"stale-column"}
	if _, err := service.OwnerServices().Construction.GetConstructionColumnPopulation(context.Background(), stale); lifecycleErrorCode(err) != "INVALID_COLUMN_ID" {
		t.Fatalf("stale column error = %v", err)
	}
	if calls != 0 {
		t.Fatalf("scanner ran before exact stage/columns validated: %d", calls)
	}

	if _, err := service.OwnerServices().Construction.GetConstructionColumnPopulation(context.Background(), request); lifecycleErrorCode(err) != "COLUMN_POPULATION_SCAN_FAILED" {
		t.Fatalf("scan failure = %v", err)
	}
	response, err := service.OwnerServices().Construction.GetConstructionColumnPopulation(context.Background(), request)
	if err != nil || calls != 2 || len(response.Columns) != 2 || response.Columns[0].Status != "POPULATED" || response.Columns[1].Status != "EMPTY" {
		t.Fatalf("population response = %#v, calls=%d, error=%v", response, calls, err)
	}
	if _, err := service.OwnerServices().Construction.GetConstructionColumnPopulation(context.Background(), request); err != nil || calls != 2 {
		t.Fatalf("cached population call = calls %d, error %v", calls, err)
	}

	stale = request
	stale.ExpectedDraftDigest = "stale-digest"
	if _, err := service.OwnerServices().Construction.GetConstructionColumnPopulation(context.Background(), stale); lifecycleErrorCode(err) == "" {
		t.Fatal("cached summary bypassed current draft revalidation")
	}
}
