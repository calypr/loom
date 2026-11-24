package selectiondb

import (
	store "github.com/calypr/loom/internal/store/arango"
)

type client = store.TransactionalRowQueryer

type SelectionStore struct{ client client }

func NewSelectionStore(c client) *SelectionStore {
	return &SelectionStore{client: c}
}
