package published

import (
	"context"
	publication "github.com/calypr/loom/internal/dataframe/publication"
)

type State = publication.State

const authResourcePathColumn = "auth_resource_path"

const (
	StateReady = publication.StateReady
)

type BundleCatalog = publication.BundleCatalog
type StaleBundleCatalog = publication.StaleBundleCatalog
type BundleExecution = publication.BundleExecution
type BundleOutputRecord = publication.BundleOutputRecord

const BundleReady = publication.BundleReady

func ResolvePublishedOutput(ctx context.Context, catalog BundleCatalog, project, generation, alias string) (Materialization, error) {
	return publication.ResolvePublishedOutput(ctx, catalog, project, generation, alias)
}

func ListPublishedOutputs(ctx context.Context, catalog BundleCatalog, project, generation string) ([]Materialization, error) {
	return publication.ListPublishedOutputs(ctx, catalog, project, generation)
}

func publishedMaterialization(execution BundleExecution, output BundleOutputRecord, alias string) Materialization {
	return Materialization{ID: execution.ID + ":" + output.Name, Name: alias, Revision: execution.ID, Project: execution.Project, DatasetGeneration: execution.DatasetGeneration, State: StateReady, ScopeUnrestricted: len(execution.AuthResourcePaths) == 0, AuthResourcePaths: append([]string(nil), execution.AuthResourcePaths...), Columns: output.Columns, PhysicalTable: output.PhysicalTable, RowCount: output.RowCount, CreatedAt: execution.CreatedAt, ReadyAt: execution.ReadyAt}
}

type Column = publication.PhysicalColumn

type Materialization = publication.Materialization
