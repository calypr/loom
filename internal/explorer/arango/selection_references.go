package arango

import (
	"context"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/explorer/arango/selectiondb"
	explorerselection "github.com/calypr/loom/internal/explorer/selection"
)

func (s *Store) ValidateSelectionReferences(ctx context.Context, project, generation string, scope authscope.ReadScope, refs []explorerselection.ResourceRef) error {
	selectionStore := selectiondb.NewSelectionStore(s.client)
	return selectionStore.ValidateSelectionReferences(ctx, project, generation, scope, refs)
}
