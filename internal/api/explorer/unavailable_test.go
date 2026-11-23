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
	"github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle"
	"github.com/gofiber/fiber/v3"
)

func TestBuilderUnavailableResponseExplainsResourceInventoryRebuild(t *testing.T) {
	evidence := capabilityfixtures.StaticEvidence{
		Inventory:     catalog.ResourceInventoryResult{Available: false, Complete: false, Status: catalog.EvidenceUnavailable},
		Relationships: catalog.RelationshipObservationResult{Values: []catalog.RelationshipObservation{}, Available: true, Complete: true, Status: catalog.EvidenceEmpty},
		Fields:        catalog.FieldEnrichmentResult{Values: []catalog.FieldEnrichmentObservation{}, Available: true, Complete: true, Status: catalog.EvidenceEmpty},
	}
	resolver, err := capabilityresolver.NewResolver(evidence, nil, capabilityfixtures.StaticActiveManifest{Manifest: unavailableCapabilityManifest(t)}, capabilityfixtures.NewStore())
	if err != nil {
		t.Fatal(err)
	}
	service, err := explorer.NewService(newTestExplorerStore())
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
	registerGeneratedExplorerTestRoutes(app, authscope.AllowAllAuthorizer{}, func(context.Context, *authscope.Principal, string) error { return nil }, service, config)
	response := requestJSON(t, app, http.MethodGet, "/api/v1/projects/project-a/explorers/default/authoring/v2/builder", "")
	if response.StatusCode != http.StatusServiceUnavailable || !strings.Contains(response.Body, `"code":"CAPABILITY_UNAVAILABLE"`) || !strings.Contains(response.Body, "resource inventory is unavailable") || !strings.Contains(response.Body, "rebuild the generation inventory") {
		t.Fatalf("unavailable Builder response status=%d body=%s", response.StatusCode, response.Body)
	}
	if strings.Contains(response.Body, `"code":"INTERNAL_ERROR"`) || strings.Contains(response.Body, `"nodes":[]`) {
		t.Fatalf("unavailable resource inventory became an internal error or empty catalog: %s", response.Body)
	}
}

func unavailableCapabilityManifest(t *testing.T) dataset.Manifest {
	t.Helper()
	ref, err := dataset.NewRef("project-a", "generation-a")
	if err != nil {
		t.Fatal(err)
	}
	schema, err := dataset.NewSchemaSnapshot("urn:test", "R5", strings.Repeat("a", 64), []string{"Patient"})
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := dataset.NewManifest(ref, schema)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err = manifest.Transition(dataset.StateStaged)
	if err != nil {
		t.Fatal(err)
	}
	return manifest
}
