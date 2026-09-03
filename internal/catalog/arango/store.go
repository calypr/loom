// Package arango contains the Arango persistence adapter for catalog facts.
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

type Store struct {
	client client
}

func New(client client) (*Store, error) {
	if client == nil {
		return nil, fmt.Errorf("catalog Arango client is required")
	}
	return &Store{client: client}, nil
}
