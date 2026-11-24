package lifecycle

import (
	"context"
	"errors"
	"testing"

	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	ws "github.com/calypr/loom/internal/explorer/workspace"
)

func TestRoutedFrameCommandsReachSemanticAuthorization(t *testing.T) {
	service := newTestService(t, &fakeStore{}, Config{})
	authorized := ports.AuthorizedCapability{Snapshot: capability.Snapshot{Token: "snapshot"}}
	workspace := ws.Workspace{Documents: []ws.Document{{
		Output: ws.Output{ID: "patients"}, RootResourceType: "Patient",
		Route: ws.RouteNode{OccurrenceID: ws.RootOccurrenceID, ResourceType: "Patient"},
	}}}
	identity := capability.ConstructionChoiceIdentity{
		SnapshotToken: authorized.Snapshot.Token, Kind: capability.ConstructionChoiceSourceSemanticFrame,
		Route:  []capability.ConstructionRouteStep{{FromResourceType: "Patient", ToResourceType: "Observation"}},
		Source: capability.SemanticFrameChoiceSource{Family: capability.SemanticFrameFamily{ResourceType: "Observation"}},
	}
	for _, kind := range []string{authoringv2.CommandSetFrameSource, authoringv2.CommandReplaceFrameSource} {
		t.Run(kind, func(t *testing.T) {
			command := authoringv2.Command{Type: kind, OutputID: "patients"}
			_, err := service.prepareFrameSourceCommand(context.Background(), "project", "explorer", authorized, workspace, ws.CatalogSnapshot{}, command, identity)
			var failure *ports.Error
			if !errors.As(err, &failure) || failure.Stage != "commands" || failure.Code != "CATALOG_UNAVAILABLE" {
				t.Fatalf("expected routed frame to reach semantic authorization, got %v", err)
			}
		})
	}
}
