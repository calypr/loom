package selectiondb

import (
	"context"

	store "github.com/calypr/loom/internal/store/arango"
)

type client interface {
	store.RowQueryer
	WithTransaction(context.Context, store.TransactionCollections, store.TransactionFunc) error
}

type SelectionStore struct{ client client }

func NewSelectionStore(c client) SelectionStore {
	return SelectionStore{client: c}
}
