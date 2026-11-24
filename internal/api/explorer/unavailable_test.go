package explorerapi

import (
	"context"
	"net/http"
	"strings"
	"testing"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/testkit/capabilityfixtures"

	capabilityresolver "github.com/calypr/loom/internal/explorer/capability/resolver"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle"
	explorerfixtures "github.com/calypr/loom/internal/testkit/explorerfixtures"
	"github.com/gofiber/fiber/v3"
)

func TestBuilderUnavailableResponseExplainsResourceInventoryRebuild(t *testing.T) {
	evidence := capabilityfixtures.StaticEvidence{
		Inventory:     catalog.ResourceInventoryResult{Available: false, Complete: false, Status: catalog.EvidenceUnavailable},
		Relationships: catalog.RelationshipObservationResult{Values: []catalog.RelationshipObservation{}, Available: true, Complete: true, Status: catalog.EvidenceEmpty},
		Fields:        catalog.FieldEnrichmentResult{Values: []catalog.FieldEnrichmentObservation{}, Available: true, Complete: true, Status: catalog.EvidenceEmpty},
	}
	resolver, err := capabilityresolver.NewResolver(evidence, nil, capabilityfixtures.StaticActiveManifest{Manifest: capabilityfixtures.StagedManifest(t, "project-a", "generation-a")}, capabilityfixtures.NewStore())
	if err != nil {
		t.Fatal(err)
	}
	backingStore := newTestExplorerStore()
	service, err := explorerfixtures.NewService(backingStore)
	if err != nil {
		t.Fatal(err)
	}
	config := lifecycle.Config{Capability: ports.CapabilityResolver{
		Current: func(ctx context.Context, project, _, generation string) (capability.Snapshot, error) {
			return resolver.Resolve(ctx, project, generation)
		},
		Catalog: ws.CatalogFromCapability,
	}}
	app := fiber.New()
	registerGeneratedExplorerTestRoutes(app, authscope.AllowAllAuthorizer{}, func(context.Context, *authscope.Principal, string) error { return nil }, service, config, backingStore)
	response := requestJSON(t, app, http.MethodGet, "/api/v1/projects/project-a/explorers/default/authoring/v2/builder", "")
	if response.StatusCode != http.StatusServiceUnavailable || !strings.Contains(response.Body, `"code":"CAPABILITY_UNAVAILABLE"`) || !strings.Contains(response.Body, "resource inventory is unavailable") || !strings.Contains(response.Body, "rebuild the generation inventory") {
		t.Fatalf("unavailable Builder response status=%d body=%s", response.StatusCode, response.Body)
	}
	if strings.Contains(response.Body, `"code":"INTERNAL_ERROR"`) || strings.Contains(response.Body, `"nodes":[]`) {
		t.Fatalf("unavailable resource inventory became an internal error or empty catalog: %s", response.Body)
	}
}
