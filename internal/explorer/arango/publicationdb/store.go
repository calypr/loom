package publicationdb

import (
	"fmt"

	"github.com/calypr/loom/internal/explorer/arango/db"

	store "github.com/calypr/loom/internal/store/arango"
)

type client = store.TransactionalRowQueryer

type ReceiptStore struct{ client client }

func NewReceiptStore(c client) *ReceiptStore {
	return &ReceiptStore{client: c}
}

type PublicationStore struct{ client client }

type ViewBuilderStore struct{ client client }

func NewPublicationStore(c client) *PublicationStore {
	return &PublicationStore{client: c}
}

func NewViewBuilderStore(c client) *ViewBuilderStore {
	return &ViewBuilderStore{client: c}
}

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
		ReceiptStore:     *NewReceiptStore(c),
		PublicationStore: *NewPublicationStore(c),
		ViewBuilderStore: *NewViewBuilderStore(c),
		client:           c,
	}, nil
}

func explorerDocumentKey(project, id string) string { return db.ExplorerKey(project, id) }
