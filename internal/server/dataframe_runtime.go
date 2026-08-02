package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
	"github.com/calypr/loom/internal/dataframe/runtime"
	publication "github.com/calypr/loom/internal/publication"
	arangostore "github.com/calypr/loom/internal/store/arango"
)

// dataframeComponents owns optional publication degradation state. Keeping
// this state together prevents a failed ClickHouse/bootstrap path from being
// mistaken for a failed core Arango server.
type dataframeComponents struct {
	logger      *slog.Logger
	degradation error
}

type discoveryComponents struct {
	cache              *catalog.Cache
	discoverFields     func(context.Context, catalog.PopulatedFieldOptions) ([]catalog.PopulatedField, error)
	discoverReferences func(context.Context, catalog.PopulatedReferenceOptions) ([]catalog.PopulatedReference, error)
}

func newDiscoveryComponents() discoveryComponents {
	cache := catalog.NewCache()
	return discoveryComponents{cache: cache, discoverFields: cache.DiscoverFields(catalog.DiscoverPopulatedFields), discoverReferences: cache.DiscoverReferences(catalog.DiscoverPopulatedReferences)}
}

func newDataframeService(connOpts arangostore.ConnectionOptions, scopeResolver *authscope.ScopeResolver, activeManifestResolver publication.ActiveResolver) *runtime.Service {
	return runtime.NewService(runtime.ServiceConfig{ConnectionOptions: connOpts, ScopeResolver: scopeResolver, ActiveManifestResolver: activeManifestResolver})
}

func (c *dataframeComponents) record(stage string, cause error) {
	if cause == nil {
		return
	}
	c.degradation = errors.Join(c.degradation, fmt.Errorf("%s: %w", stage, cause))
	if c.logger != nil {
		c.logger.Error("dataframe startup degraded", "stage", stage, "error", cause)
	}
}
