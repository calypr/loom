package publicationdb

import (
	"context"
	"fmt"

	"github.com/calypr/loom/internal/explorer/arango/db"

	store "github.com/calypr/loom/internal/store/arango"
)

type client interface {
	store.RowQueryer
	WithTransaction(context.Context, store.TransactionCollections, store.TransactionFunc) error
}

type ReceiptStore struct{ client client }

func NewReceiptStore(c client) ReceiptStore {
	return ReceiptStore{client: c}
}

type PublicationStore struct{ client client }

type ViewBuilderStore struct{ client client }

type Store struct {
	ReceiptStore
	PublicationStore
	ViewBuilderStore
	client client
}

func NewStore(c client) (*Store, error) {
	if c == nil {
		return nil, fmt.Errorf("publication Arango client is required")
	}
	return &Store{
		ReceiptStore:     NewReceiptStore(c),
		PublicationStore: PublicationStore{client: c},
		ViewBuilderStore: ViewBuilderStore{client: c},
		client:           c,
	}, nil
}

func explorerDocumentKey(project, id string) string { return "explorer_" + db.Key(project, id) }
