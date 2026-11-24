package arango

import (
	"fmt"

	store "github.com/calypr/loom/internal/store/arango"
)

type FieldStore struct {
	client store.CollectionBootstrapClient
}

type RelationshipStore struct {
	client store.CollectionBootstrapClient
}

type ResourceStore struct {
	client store.CollectionBootstrapClient
}

type Store struct {
	FieldStore
	RelationshipStore
	ResourceStore
}

func New(client store.CollectionBootstrapClient) (*Store, error) {
	if client == nil {
		return nil, fmt.Errorf("catalog Arango client is required")
	}
	return &Store{
		FieldStore:        FieldStore{client: client},
		RelationshipStore: RelationshipStore{client: client},
		ResourceStore:     ResourceStore{client: client},
	}, nil
}
