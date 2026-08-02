package clickhouse

import (
	"context"
	"github.com/calypr/loom/internal/dataframe/publication"
)

type BundleExecution = publication.BundleExecution
type BundlePointer = publication.BundlePointer
type BundleIdentity = publication.BundleIdentity
type BundleOutputRecord = publication.BundleOutputRecord
type BundleState = publication.BundleState
type Column = publication.PhysicalColumn

const (
	BundleReady   = publication.BundleReady
	BundleFailed  = publication.BundleFailed
	BundleLoading = publication.BundleLoading
)

var ErrBundleNotFound = publication.ErrBundleNotFound
var ErrBundlePointerConflict = publication.ErrBundlePointerConflict

func ResolvePublishedOutput(ctx context.Context, catalog publication.BundleCatalog, project, generation, alias string) (publication.Materialization, error) {
	return publication.ResolvePublishedOutput(ctx, catalog, project, generation, alias)
}
