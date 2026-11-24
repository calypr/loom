package arango

import (
	"fmt"

	store "github.com/calypr/loom/internal/store/arango"
)

type SemanticInventoryStore struct {
	client store.CollectionBootstrapClient
}

type SemanticCoverageStore struct {
	client    store.CollectionBootstrapClient
	inventory SemanticInventoryStore
}

type Store struct {
	SemanticInventoryStore
	SemanticCoverageStore
}

func New(c store.CollectionBootstrapClient) (*Store, error) {
	if c == nil {
		return nil, fmt.Errorf("semantic Arango client is required")
	}
	inventory := SemanticInventoryStore{client: c}
	return &Store{
		SemanticInventoryStore: inventory,
		SemanticCoverageStore:  SemanticCoverageStore{client: c, inventory: inventory},
	}, nil
}
