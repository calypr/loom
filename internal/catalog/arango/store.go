package arango

import (
	"context"
	"fmt"

	store "github.com/calypr/loom/internal/store/arango"
)

type client interface {
	store.RowQueryer
	store.BatchInserter
	store.AQLExecutor
	CollectionExists(context.Context, string) (bool, error)
	Bootstrap(context.Context, store.BootstrapSpec) error
}

type FieldStore struct{ client client }

type RelationshipStore struct{ client client }

type ResourceStore struct{ client client }

type Store struct {
	FieldStore
	RelationshipStore
	ResourceStore
}

func New(client client) (*Store, error) {
	if client == nil {
		return nil, fmt.Errorf("catalog Arango client is required")
	}
	return &Store{
		FieldStore:        FieldStore{client: client},
		RelationshipStore: RelationshipStore{client: client},
		ResourceStore:     ResourceStore{client: client},
	}, nil
}
