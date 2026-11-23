package arango

import (
	"context"
	"fmt"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/arango/db"
	"github.com/calypr/loom/internal/explorer/arango/publicationdb"
	"github.com/calypr/loom/internal/explorer/arango/selectiondb"
	"github.com/calypr/loom/internal/explorer/arango/workspacedb"
	explorerselection "github.com/calypr/loom/internal/explorer/selection"

	store "github.com/calypr/loom/internal/store/arango"
)

type client interface {
	store.RowQueryer
	WithTransaction(context.Context, store.TransactionCollections, store.TransactionFunc) error
}

type InterpretationStore struct{ client client }

type DraftStore struct{ client client }

type ExplicitGroupStore struct {
	client     client
	selections selectiondb.SelectionStore
}

type Store struct {
	DraftStore
	selectiondb.SelectionStore
	workspacedb.RevisionStore
	workspacedb.WorkspaceStore
	ExplicitGroupStore
	publicationdb.Store
	InterpretationStore
	client client
}

func New(client client) (*Store, error) {
	if client == nil {
		return nil, fmt.Errorf("Explorer Arango client is required")
	}
	selections := selectiondb.NewSelectionStore(client)
	publicationStore, err := publicationdb.NewStore(client)
	if err != nil {
		return nil, err
	}
	return &Store{
		DraftStore:          DraftStore{client: client},
		SelectionStore:      selections,
		RevisionStore:       workspacedb.NewRevisionStore(client),
		WorkspaceStore:      workspacedb.NewWorkspaceStore(client),
		ExplicitGroupStore:  ExplicitGroupStore{client: client, selections: selections},
		Store:               *publicationStore,
		InterpretationStore: InterpretationStore{client: client},
		client:              client,
	}, nil
}

func explorerKey(project, id string) string { return "explorer_" + db.Key(project, id) }

var _ explorer.ExplorerStore = (*Store)(nil)
var _ explorer.DraftRevisionReader = (*Store)(nil)

func (s *Store) ValidateSelectionReferences(ctx context.Context, project, generation string, scope authscope.ReadScope, refs []explorerselection.ResourceRef) error {
	selectionStore := selectiondb.NewSelectionStore(s.client)
	return selectionStore.ValidateSelectionReferences(ctx, project, generation, scope, refs)
}
