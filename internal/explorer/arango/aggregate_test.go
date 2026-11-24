package arango

import (
	"fmt"

	"github.com/calypr/loom/internal/explorer/arango/publicationdb"
	"github.com/calypr/loom/internal/explorer/arango/selectiondb"
	"github.com/calypr/loom/internal/explorer/arango/workspacedb"
)

type Store struct {
	*DraftStore
	*selectiondb.SelectionStore
	*workspacedb.RevisionStore
	*workspacedb.WorkspaceStore
	*ExplicitGroupStore
	*publicationdb.Store
	*InterpretationStore
	*WorkspaceProjectionStore
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
		DraftStore:               NewDraftStore(client),
		SelectionStore:           selections,
		RevisionStore:            workspacedb.NewRevisionStore(client),
		WorkspaceStore:           workspacedb.NewWorkspaceStore(client),
		ExplicitGroupStore:       NewExplicitGroupStore(client, selections),
		Store:                    publicationStore,
		InterpretationStore:      NewInterpretationStore(client),
		WorkspaceProjectionStore: NewWorkspaceProjectionStore(client),
	}, nil
}
