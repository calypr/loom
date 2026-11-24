package workspacedb

import (
	store "github.com/calypr/loom/internal/store/arango"
)

type client = store.TransactionalRowQueryer

type RevisionStore struct{ client client }

func NewRevisionStore(c client) *RevisionStore {
	return &RevisionStore{client: c}
}

type WorkspaceStore struct{ client client }

func NewWorkspaceStore(c client) *WorkspaceStore {
	return &WorkspaceStore{client: c}
}
