package publicationdb

import (
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

func explorerDocumentKey(project, id string) string { return db.ExplorerKey(project, id) }
