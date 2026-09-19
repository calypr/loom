package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
	"github.com/calypr/loom/internal/explorer/capability"
)

func TestBrowseSemanticInventoryScopesAndContext(t *testing.T) {
	for _, scope := range []authscope.ReadScope{
		{Mode: authscope.ReadScopeUnrestricted},
		{Mode: authscope.ReadScopeRestricted, AuthResourcePaths: []string{"/allowed"}},
		{Mode: authscope.ReadScopeRestricted},
	} {
		t.Run(fmt.Sprintf("%s-%d", scope.Mode, len(scope.AuthResourcePaths)), func(t *testing.T) {
			snapshot := readySnapshot("project-a", "generation-a", "token", scope)
			snapshot.Nodes = []capability.Node{{ResourceType: "Observation", RowRootEligible: true}, {ResourceType: "Specimen", RowRootEligible: true}}
			calls := 0
			buildID := "build-a"
			service := &Service{config: Config{
				Capability: CapabilityResolver{ForCompilation: func(context.Context, string, string) (AuthorizedCapability, error) {
					return AuthorizedCapability{Snapshot: snapshot, Scope: scope}, nil
				}},
				SemanticInventory: func(_ context.Context, opts catalog.SemanticInventoryPageOptions) (catalog.SemanticInventoryPage, error) {
					calls++
					if opts.Project != "project-a" || opts.DatasetGeneration != "generation-a" || opts.AuthResourcePathsUnrestricted == nil || *opts.AuthResourcePathsUnrestricted != (scope.Mode == authscope.ReadScopeUnrestricted) || len(opts.AuthResourcePaths) != len(scope.AuthResourcePaths) {
						t.Fatalf("scope lost: %#v", opts)
					}
					if opts.Cursor != "" && opts.Cursor != "inner-page" {
						t.Fatalf("cursor was not unwrapped: %q", opts.Cursor)
					}
					return catalog.SemanticInventoryPage{State: catalog.SemanticInventoryComplete, Build: catalog.SemanticInventoryBuild{BuildID: buildID, Checkpoint: "private", ScannedResources: 9000}, Entries: []catalog.SemanticInventoryEntry{{ConceptID: "concept", BindingID: "binding", Observation: catalog.SemanticObservation{Key: catalog.SemanticObservationKey{System: "system", Code: "code", Version: "v1"}, Population: 3}}}, NextCursor: "inner-page"}, nil
				},
			}}
			req := BrowseSemanticInventoryRequest{Project: "project-a", ExplorerID: "explorer", SnapshotToken: "token", RowRoot: "Observation", Limit: 50}
			first, err := service.BrowseSemanticInventory(context.Background(), req)
			if err != nil || len(first.Entries) != 1 || first.Entries[0].Occurrences != 3 || first.Entries[0].CodingVersion != "v1" || first.ContextToken == "" || first.NextCursor == "" {
				t.Fatalf("first=%#v err=%v", first, err)
			}
			req.Cursor = first.NextCursor
			second, err := service.BrowseSemanticInventory(context.Background(), req)
			if err != nil || second.ContextToken != first.ContextToken || calls != 2 {
				t.Fatalf("second=%#v calls=%d err=%v", second, calls, err)
			}
			req.RowRoot = "Specimen"
			if _, err := service.BrowseSemanticInventory(context.Background(), req); err == nil {
				t.Fatal("cursor accepted after changing row root")
			}
			req.RowRoot = "Observation"
			buildID = "build-b"
			if _, err := service.BrowseSemanticInventory(context.Background(), req); err == nil {
				t.Fatal("cursor accepted after changing inventory build")
			}
		})
	}
}

func TestBrowseSemanticInventoryRejectsInvalidScopeBeforeRead(t *testing.T) {
	scope := authscope.ReadScope{Mode: authscope.ReadScopeRestricted, AuthResourcePaths: []string{"/allowed"}}
	snapshot := readySnapshot("project-a", "generation-a", "token", scope)
	snapshot.Nodes = []capability.Node{{ResourceType: "Observation", RowRootEligible: true}}
	service := &Service{config: Config{
		Capability: CapabilityResolver{ForCompilation: func(context.Context, string, string) (AuthorizedCapability, error) {
			return AuthorizedCapability{Snapshot: snapshot, Scope: authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}}, nil
		}},
		SemanticInventory: func(context.Context, catalog.SemanticInventoryPageOptions) (catalog.SemanticInventoryPage, error) {
			t.Fatal("inventory queried with mismatched scope")
			return catalog.SemanticInventoryPage{}, nil
		},
	}}
	_, err := service.BrowseSemanticInventory(context.Background(), BrowseSemanticInventoryRequest{Project: "project-a", ExplorerID: "explorer", SnapshotToken: "token", RowRoot: "Observation"})
	var failure *Error
	if !errors.As(err, &failure) || failure.Code != "STALE_AUTHORIZATION_SCOPE" {
		t.Fatalf("error=%v", err)
	}
}

func TestBrowseSemanticInventoryUnknownRemainsUnknown(t *testing.T) {
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	snapshot := readySnapshot("project-a", "generation-a", "token", scope)
	snapshot.Nodes = []capability.Node{{ResourceType: "Observation", RowRootEligible: true}}
	service := &Service{config: Config{
		Capability: CapabilityResolver{ForCompilation: func(context.Context, string, string) (AuthorizedCapability, error) {
			return AuthorizedCapability{Snapshot: snapshot, Scope: scope}, nil
		}},
		SemanticInventory: func(context.Context, catalog.SemanticInventoryPageOptions) (catalog.SemanticInventoryPage, error) {
			return catalog.SemanticInventoryPage{State: catalog.SemanticInventoryUnknown}, nil
		},
	}}
	result, err := service.BrowseSemanticInventory(context.Background(), BrowseSemanticInventoryRequest{Project: "project-a", ExplorerID: "explorer", SnapshotToken: "token", RowRoot: "Observation"})
	if err != nil || result.State != catalog.SemanticInventoryUnknown || result.Entries == nil || len(result.Entries) != 0 {
		t.Fatalf("result=%#v error=%v", result, err)
	}
}
