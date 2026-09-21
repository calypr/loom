package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"testing"

	loomapi "github.com/calypr/loom/generated/loomapi"
	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle"
	"github.com/gofiber/fiber/v3"
)

func TestBrowseFeatureCatalogThroughPublicAPI(t *testing.T) {
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	digest := sha256.Sum256([]byte(string(scope.Mode) + "\x00"))
	snapshot := capability.Snapshot{
		Token: "snapshot", Status: capability.StatusReady, Complete: true,
		Identity: capability.SnapshotIdentity{Project: "project-a", Generation: "generation-a", AuthorizationScopeDigest: hex.EncodeToString(digest[:])},
		Nodes:    []capability.Node{{ID: "patient", ResourceType: "Patient", RowRootEligible: true}},
		Candidates: []capability.Candidate{
			{ID: "birth-date", NodeID: "patient", ResourceType: "Patient", FieldPath: "birthDate", Label: "Patient.birthDate", LogicalType: "date", Cardinality: "optional_one", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar}, ObservedDocumentCount: 5},
			{ID: "raw-name", NodeID: "patient", ResourceType: "Patient", FieldPath: "name[]", Label: "Patient.name", LogicalType: "object", Cardinality: "many", ProjectionModes: []capability.ProjectionMode{capability.ProjectionArray}, ObservedDocumentCount: 5},
		},
	}
	domain, err := explorer.NewService(newTestExplorerStore())
	if err != nil {
		t.Fatal(err)
	}
	config := lifecycle.Config{Capability: lifecycle.CapabilityResolver{ForCompilation: func(context.Context, string, string) (lifecycle.AuthorizedCapability, error) {
		return lifecycle.AuthorizedCapability{Snapshot: snapshot, Scope: scope}, nil
	}}}
	app := fiber.New()
	registerGeneratedExplorerTestRoutes(app, authscope.AllowAllAuthorizer{}, func(context.Context, *authscope.Principal, string) error { return nil }, domain, config)
	path := "/api/v1/projects/project-a/explorers/explorer/authoring/v2/feature-catalog"
	response := requestJSON(t, app, http.MethodPost, path, `{"snapshotToken":"snapshot","rowRoot":"Patient","section":"FIELDS","limit":50}`)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.StatusCode, response.Body)
	}
	var result loomapi.FeatureCatalogBrowseResponse
	if err := json.Unmarshal([]byte(response.Body), &result); err != nil {
		t.Fatal(err)
	}
	if result.Section != loomapi.FeatureCatalogBrowseResponseSectionFIELDS || len(result.Entries) != 1 || result.Entries[0].Title != "Birth date" || result.Entries[0].FeatureId != "field:birth-date" || result.Entries[0].ConstructionChoice == nil {
		t.Fatalf("feature catalog = %#v", result)
	}
	source, err := result.Entries[0].Source.AsFeatureCatalogFieldSource()
	if err != nil || source.Kind != loomapi.FeatureCatalogFieldSourceKindFIELD || source.CandidateId != "birth-date" {
		t.Fatalf("field source=%#v err=%v", source, err)
	}
	response = requestJSON(t, app, http.MethodPost, path, `{"snapshotToken":"snapshot","rowRoot":"Patient","section":"RAW"}`)
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid section status=%d body=%s", response.StatusCode, response.Body)
	}
}
