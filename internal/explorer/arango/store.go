package arango

import (
	"context"
	"fmt"
	"github.com/calypr/loom/internal/explorer/arango/db"
	"github.com/calypr/loom/internal/explorer/arango/selectiondb"

	"github.com/calypr/loom/internal/explorer"
	store "github.com/calypr/loom/internal/store/arango"
)

type client interface {
	store.RowQueryer
	WithTransaction(context.Context, store.TransactionCollections, store.TransactionFunc) error
}

type ReceiptStore struct{ client client }

type InterpretationStore struct{ client client }

type ViewBuilderStore struct{ client client }

type PublicationStore struct{ client client }

type DraftStore struct{ client client }

type ExplicitGroupStore struct {
	client     client
	selections selectiondb.SelectionStore
}

type Store struct {
	DraftStore
	selectiondb.SelectionStore
	ExplicitGroupStore
	ReceiptStore
	InterpretationStore
	ViewBuilderStore
	PublicationStore
	client client
}

func New(client client) (*Store, error) {
	if client == nil {
		return nil, fmt.Errorf("Explorer Arango client is required")
	}
	selections := selectiondb.NewSelectionStore(client)
	return &Store{
		DraftStore:          DraftStore{client: client},
		SelectionStore:      selections,
		ExplicitGroupStore:  ExplicitGroupStore{client: client, selections: selections},
		ReceiptStore:        ReceiptStore{client: client},
		InterpretationStore: InterpretationStore{client: client},
		ViewBuilderStore:    ViewBuilderStore{client: client},
		PublicationStore:    PublicationStore{client: client},
		client:              client,
	}, nil
}

func explorerKey(project, id string) string { return "explorer_" + db.Key(project, id) }

var _ explorer.Store = (*Store)(nil)
var _ explorer.DraftRevisionReader = (*Store)(nil)
