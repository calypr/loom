package arango

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/calypr/loom/internal/explorer"
	store "github.com/calypr/loom/internal/store/arango"
)

type client interface {
	store.RowQueryer
	WithTransaction(context.Context, store.TransactionCollections, store.TransactionFunc) error
}

type Store struct{ client client }

func New(client client) (*Store, error) {
	if client == nil {
		return nil, fmt.Errorf("Explorer Arango client is required")
	}
	return &Store{client: client}, nil
}

func key(parts ...string) string {
	h := sha256.New()
	for _, part := range parts {
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(part))
	}
	return hex.EncodeToString(h.Sum(nil))
}

func explorerKey(project, id string) string { return "explorer_" + key(project, id) }

var _ explorer.Store = (*Store)(nil)
