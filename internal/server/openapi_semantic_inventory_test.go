package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	loomapi "github.com/calypr/loom/generated/loomapi"
	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle"
	"github.com/gofiber/fiber/v3"
)

func TestBrowseSemanticInventoryThroughPublicAPI(t *testing.T) {
	scope := authscope.ReadScope{Mode: authscope.ReadScopeRestricted, AuthResourcePaths: []string{"/allowed"}}
	digest := sha256.Sum256([]byte(string(scope.Mode) + "\x00/allowed"))
	snapshot := capability.Snapshot{
		Token: "snapshot", Status: capability.StatusReady, Complete: true,
		Identity: capability.SnapshotIdentity{Project: "project-a", Generation: "generation-a", AuthorizationScopeDigest: hex.EncodeToString(digest[:])},
		Nodes:    []capability.Node{{ResourceType: "Observation", RowRootEligible: true}},
	}
	domain, err := explorer.NewService(newTestExplorerStore())
	if err != nil {
		t.Fatal(err)
	}
	reads := 0
	config := lifecycle.Config{
		Capability: lifecycle.CapabilityResolver{ForCompilation: func(context.Context, string, string) (lifecycle.AuthorizedCapability, error) {
			return lifecycle.AuthorizedCapability{Snapshot: snapshot, Scope: scope}, nil
		}},
		SemanticInventory: func(_ context.Context, opts catalog.SemanticInventoryPageOptions) (catalog.SemanticInventoryPage, error) {
			reads++
			if opts.AuthResourcePathsUnrestricted == nil || *opts.AuthResourcePathsUnrestricted || len(opts.AuthResourcePaths) != 1 || opts.AuthResourcePaths[0] != "/allowed" || opts.Query != "glucose" || opts.Limit != 2 {
				t.Fatalf("unsafe or lost browse options: %#v", opts)
			}
			return catalog.SemanticInventoryPage{State: catalog.SemanticInventoryComplete, Build: catalog.SemanticInventoryBuild{BuildID: "build", Checkpoint: "secret-checkpoint", ScannedResources: 9000}, Entries: []catalog.SemanticInventoryEntry{{ConceptID: "concept", BindingID: "binding", Observation: catalog.SemanticObservation{Population: 7, Key: catalog.SemanticObservationKey{Code: "glucose", System: "system"}}}}}, nil
		},
	}
	denied := false
	app := fiber.New()
	registerGeneratedExplorerTestRoutes(app, authscope.AllowAllAuthorizer{}, func(context.Context, *authscope.Principal, string) error {
		if denied {
			return errors.New("denied")
		}
		return nil
	}, domain, config)
	path := "/api/v1/projects/project-a/explorers/explorer/authoring/v2/semantic-inventory"
	body := `{"snapshotToken":"snapshot","rowRoot":"Observation","query":"glucose","limit":2}`
	response := requestJSON(t, app, http.MethodPost, path, body)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.StatusCode, response.Body)
	}
	var result loomapi.SemanticInventoryBrowseResponse
	if err := json.Unmarshal([]byte(response.Body), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Entries) != 1 || result.Entries[0].Occurrences != 7 || result.Entries[0].Code != "glucose" || result.ContextToken == "" {
		t.Fatalf("result=%#v", result)
	}
	for _, test := range []struct {
		body   string
		status int
	}{
		{`{"snapshotToken":"snapshot","rowRoot":"Observation","limit":51}`, http.StatusBadRequest},
		{`{"snapshotToken":"snapshot","rowRoot":"Observation","query":"glucose","limit":2,"authResourcePaths":["/secret"]}`, http.StatusOK},
		{`{"snapshotToken":"old","rowRoot":"Observation"}`, http.StatusConflict},
	} {
		response := requestJSON(t, app, http.MethodPost, path, test.body)
		if response.StatusCode != test.status {
			t.Fatalf("status=%d expected=%d body=%s", response.StatusCode, test.status, response.Body)
		}
	}
	denied = true
	response = requestJSON(t, app, http.MethodPost, path, body)
	if response.StatusCode != http.StatusForbidden || reads != 2 {
		t.Fatalf("denied status=%d reads=%d body=%s", response.StatusCode, reads, response.Body)
	}
}
