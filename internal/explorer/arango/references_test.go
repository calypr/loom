package arango

import (
	"context"
	"errors"
	"strings"
	"testing"

	explorerselection "github.com/calypr/loom/internal/explorer/selection"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/explorer/arango/selectiondb"
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
	persistence := selectiondb.NewSelectionStore(client)
	refs := []explorerselection.ResourceRef{
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
	persistence := selectiondb.NewSelectionStore(&selectionReferenceClient{})
	ref := explorerselection.ResourceRef{Project: "project", Generation: "generation-a", ResourceType: "DocumentReference", ID: "guessed"}
	if err := persistence.ValidateSelectionReferences(context.Background(), "project", "generation-a", authscope.ReadScope{Mode: authscope.ReadScopeRestricted, AuthResourcePaths: []string{"allowed"}}, []explorerselection.ResourceRef{ref}); !errors.Is(err, explorerselection.ErrResourceRefScopeMismatch) {
		t.Fatalf("restricted missing reference error = %v", err)
	}
	if err := persistence.ValidateSelectionReferences(context.Background(), "project", "generation-a", authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}, []explorerselection.ResourceRef{ref}); !errors.Is(err, explorerselection.ErrSelectionNotFound) {
		t.Fatalf("unrestricted missing reference error = %v", err)
	}
	foreign := ref
	foreign.Project = "other"
	if err := persistence.ValidateSelectionReferences(context.Background(), "project", "generation-a", authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}, []explorerselection.ResourceRef{foreign}); !errors.Is(err, explorerselection.ErrResourceRefScopeMismatch) {
		t.Fatalf("foreign reference error = %v", err)
	}
	aliasStore := selectiondb.NewSelectionStore(&selectionReferenceClient{rows: []map[string]any{{"key": "physical_key", "id": "logical-id"}}})
	physicalKey := explorerselection.ResourceRef{Project: "project", Generation: "generation-a", ResourceType: "DocumentReference", ID: "physical_key"}
	if err := aliasStore.ValidateSelectionReferences(context.Background(), "project", "generation-a", authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}, []explorerselection.ResourceRef{physicalKey}); !errors.Is(err, explorerselection.ErrSelectionNotFound) {
		t.Fatalf("physical key was accepted as logical ID: %v", err)
	}
}

func TestValidateSelectionReferencesUsesResourceStorageProjectIdentity(t *testing.T) {
	client := &selectionReferenceClient{rows: []map[string]any{{"id": "file001"}}}
	persistence := selectiondb.NewSelectionStore(client)
	err := persistence.ValidateSelectionReferences(context.Background(), "study/project", "generation-a", authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}, []explorerselection.ResourceRef{{
		Project: "study/project", Generation: "generation-a", ResourceType: "DocumentReference", ID: "file001",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if got := client.binds["project"]; got != "study-project" {
		t.Fatalf("resource storage project = %v, want study-project", got)
	}
}
