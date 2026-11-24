package arango

import (
	"github.com/calypr/loom/internal/explorer/arango/db"
	"github.com/calypr/loom/internal/explorer/arango/selectiondb"
	store "github.com/calypr/loom/internal/store/arango"
)

type client = store.TransactionalRowQueryer

type InterpretationStore struct{ client client }

type DraftStore struct{ client client }

type ExplicitGroupStore struct {
	client     client
	selections *selectiondb.SelectionStore
}

func NewDraftStore(client client) *DraftStore {
	return &DraftStore{client: client}
}

func NewExplicitGroupStore(client client, selections *selectiondb.SelectionStore) *ExplicitGroupStore {
	return &ExplicitGroupStore{client: client, selections: selections}
}

func NewInterpretationStore(client client) *InterpretationStore {
	return &InterpretationStore{client: client}
}

func explorerKey(project, id string) string { return db.ExplorerKey(project, id) }
