package publicationdb

import (
	"context"

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
