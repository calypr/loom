package server

import (
	"context"
	"fmt"

	"github.com/calypr/loom/generated/graphql/graph/resolver"
	dumpapi "github.com/calypr/loom/internal/api/bulk/dump"
	loadapi "github.com/calypr/loom/internal/api/bulk/load"
	clickhousegraphql "github.com/calypr/loom/internal/api/graphql/flat"
	graphapi "github.com/calypr/loom/internal/api/graphql/graph"
	materializationapi "github.com/calypr/loom/internal/api/graphql/graph/materialization"
	api "github.com/calypr/loom/internal/api/http"
	"github.com/calypr/loom/internal/authscope"
	publication "github.com/calypr/loom/internal/publication"
)

type manifestReader interface {
	ReadManifest(context.Context, publication.Ref) (publication.Manifest, error)
	ResolveActiveManifest(context.Context, string) (publication.Manifest, error)
}

func registerRoutes(server *api.HTTPServer, importService *loadapi.Service, authorizer authscope.Authorizer, scopeResolver *authscope.ScopeResolver, disableSingleResourceImports bool, rawQuery dumpapi.QueryRowsClient, manifests manifestReader, dataframeExporter *materializationapi.Service, graphResolver *resolver.Resolver) error {
	loadHandler, err := loadapi.NewHandler(loadapi.Config{Service: importService, Authorizer: authorizer, ScopeResolver: scopeResolver, DisableSingleResourceImports: disableSingleResourceImports})
	if err != nil {
		return fmt.Errorf("create load handler: %w", err)
	}
	loadHandler.RegisterRoutes(server.App())
	dumpapi.NewHandler(dumpapi.Config{RawExporter: dumpapi.ArangoRawExporter{Query: rawQuery, Manifests: manifests}, DataframeExporter: dataframeExporter, ScopeResolver: scopeResolver, DisableSingleResourceImports: disableSingleResourceImports}).RegisterRoutes(server.App())
	graphapi.RegisterRoutes(server.App(), graphapi.RouteConfig{Handler: graphapi.NewHandler(graphResolver), Playground: graphapi.NewPlaygroundHandler("/graphql/graph"), Sandbox: graphapi.NewApolloSandboxHandler("/graphql/graph")})
	clickhousegraphql.RegisterRoutes(server.App(), clickhousegraphql.NewHandler(dataframeExporter))
	return nil
}
