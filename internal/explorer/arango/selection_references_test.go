package arango

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/explorer"
	store "github.com/calypr/loom/internal/store/arango"
)

type selectionReferenceClient struct {
	rows  []map[string]any
	binds map[string]any
}

func (c *selectionReferenceClient) QueryRows(_ context.Context, query string, _ int, binds map[string]any, visit store.RowVisitor) error {
	if !strings.Contains(query, "selectionReferenceValidation") && !strings.Contains(query, "FOR d IN @@resource_collection") {
		return nil
	}
	c.binds = binds
	for _, row := range c.rows {
		if err := visit(row); err != nil {
			return err
		}
	}
	return nil
}

func (c *selectionReferenceClient) WithTransaction(context.Context, store.TransactionCollections, store.TransactionFunc) error {
	return errors.New("unexpected transaction")
}

func TestValidateSelectionReferencesBindsGenerationAndScope(t *testing.T) {
	client := &selectionReferenceClient{rows: []map[string]any{{"key": "files001", "id": "files001"}, {"key": "files002", "id": "files002"}}}
	persistence := &Store{client: client}
	refs := []explorer.ResourceRef{
		{Project: "loom_dev", Generation: "generation-a", ResourceType: "DocumentReference", ID: "files001"},
		{Project: "loom_dev", Generation: "generation-a", ResourceType: "DocumentReference", ID: "files002"},
		{Project: "loom_dev", Generation: "generation-a", ResourceType: "DocumentReference", ID: "files001"},
	}
	err := persistence.ValidateSelectionReferences(context.Background(), "loom_dev", "generation-a", authscope.ReadScope{Mode: authscope.ReadScopeRestricted, AuthResourcePaths: []string{"allowed"}}, refs)
	if err != nil {
		t.Fatal(err)
	}
	if got := client.binds["project"]; got != "loom_dev" {
		t.Fatalf("project bind = %#v", got)
	}
	if got := client.binds["generation"]; got != "generation-a" {
		t.Fatalf("generation bind = %#v", got)
	}
	if got := client.binds["auth_unrestricted"]; got != false {
		t.Fatalf("unrestricted bind = %#v", got)
	}
}

func TestValidateSelectionReferencesFailsClosedForScopeAndMissingIDs(t *testing.T) {
	persistence := &Store{client: &selectionReferenceClient{}}
	ref := explorer.ResourceRef{Project: "project", Generation: "generation-a", ResourceType: "DocumentReference", ID: "guessed"}
	if err := persistence.ValidateSelectionReferences(context.Background(), "project", "generation-a", authscope.ReadScope{Mode: authscope.ReadScopeRestricted, AuthResourcePaths: []string{"allowed"}}, []explorer.ResourceRef{ref}); !errors.Is(err, explorer.ErrResourceRefScopeMismatch) {
		t.Fatalf("restricted missing reference error = %v", err)
	}
	if err := persistence.ValidateSelectionReferences(context.Background(), "project", "generation-a", authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}, []explorer.ResourceRef{ref}); !errors.Is(err, explorer.ErrSelectionNotFound) {
		t.Fatalf("unrestricted missing reference error = %v", err)
	}
	foreign := ref
	foreign.Project = "other"
	if err := persistence.ValidateSelectionReferences(context.Background(), "project", "generation-a", authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}, []explorer.ResourceRef{foreign}); !errors.Is(err, explorer.ErrResourceRefScopeMismatch) {
		t.Fatalf("foreign reference error = %v", err)
	}
	aliasStore := &Store{client: &selectionReferenceClient{rows: []map[string]any{{"key": "physical_key", "id": "logical-id"}}}}
	physicalKey := explorer.ResourceRef{Project: "project", Generation: "generation-a", ResourceType: "DocumentReference", ID: "physical_key"}
	if err := aliasStore.ValidateSelectionReferences(context.Background(), "project", "generation-a", authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}, []explorer.ResourceRef{physicalKey}); !errors.Is(err, explorer.ErrSelectionNotFound) {
		t.Fatalf("physical key was accepted as logical ID: %v", err)
	}
}
