package queryapi

import (
	"context"
	"testing"

	"github.com/calypr/loom/generated/graphql/graph/model"
	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
	"github.com/calypr/loom/internal/dataframe/runtime"
)

type dataframeBuilderRestrictedEmptyResourceAccess struct{}

func (dataframeBuilderRestrictedEmptyResourceAccess) GetAllowedResources(context.Context, string, string, string) ([]string, error) {
	return []string{"/programs/example/projects/allowed"}, nil
}

func dataframeBuilderRestrictedEmptyScopeResolver() *authscope.ScopeResolver {
	return authscope.NewScopeResolver(authscope.ScopeResolverConfig{
		ResourceAccess: dataframeBuilderRestrictedEmptyResourceAccess{},
		ListExistingAuthResourcePaths: func(context.Context, catalog.AuthResourcePathOptions) ([]string, error) {
			return []string{"example-different"}, nil
		},
	})
}

func dataframeBuilderRestrictedEmptyContext() context.Context {
	return authscope.ContextWithPrincipal(context.Background(), &authscope.Principal{
		AuthorizationHeader: "Bearer header.payload.signature",
	})
}

func TestRunPreservesRestrictedEmptyScopeIntoDataframeService(t *testing.T) {
	resolver := dataframeBuilderRestrictedEmptyScopeResolver()
	preparedCatalogCalls := 0

	dataframes := runtime.NewService(runtime.ServiceConfig{
		// This intentionally has no ScopeResolver. The one-shot GraphQL recipe
		// is already catalog- and scope-resolved before reaching execution, so
		// the runtime must not perform a second recipe preparation pass.
		ExecuteRows: func(_ context.Context, _ runtime.ExecuteQueryOptions, _ string, bindVars map[string]any, _ func(map[string]any) error) error {
			if got, ok := bindVars["auth_resource_paths_unrestricted"].(bool); !ok || got {
				t.Fatalf("dataframe AQL unrestricted bind = %#v, want false", bindVars["auth_resource_paths_unrestricted"])
			}
			return nil
		},
	})
	service := NewService(Config{
		ScopeResolver: resolver,
		DiscoverFields: func(_ context.Context, options catalog.PopulatedFieldOptions) ([]catalog.PopulatedField, error) {
			preparedCatalogCalls++
			assertRestrictedEmptyFieldScope(t, options)
			return []catalog.PopulatedField{}, nil
		},
		Dataframes: dataframes,
	})

	result, err := service.Run(dataframeBuilderRestrictedEmptyContext(), model.FhirDataframeInput{
		Project:          "P1",
		RootResourceType: "Patient",
	}, nil)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.RowCount != 0 {
		t.Fatalf("result row count = %d, want no fake rows", result.RowCount)
	}
	if preparedCatalogCalls == 0 {
		t.Fatalf("prepared catalog calls = %d, want scoped preparation", preparedCatalogCalls)
	}
}

func TestIntrospectKeepsRestrictedEmptyCatalogMode(t *testing.T) {
	resolver := dataframeBuilderRestrictedEmptyScopeResolver()
	fieldCalls := 0
	referenceCalls := 0
	service := NewService(Config{
		ScopeResolver: resolver,
		DiscoverFields: func(_ context.Context, options catalog.PopulatedFieldOptions) ([]catalog.PopulatedField, error) {
			fieldCalls++
			assertRestrictedEmptyFieldScope(t, options)
			return []catalog.PopulatedField{}, nil
		},
		DiscoverReferences: func(_ context.Context, options catalog.PopulatedReferenceOptions) ([]catalog.PopulatedReference, error) {
			referenceCalls++
			assertRestrictedEmptyReferenceScope(t, options)
			return []catalog.PopulatedReference{}, nil
		},
	})
	ctx := dataframeBuilderRestrictedEmptyContext()

	if _, err := service.Introspect(ctx, IntrospectionRequest{Project: "P1", RootResourceType: "Patient"}); err != nil {
		t.Fatalf("Introspect() error = %v", err)
	}
	if fieldCalls == 0 || referenceCalls == 0 {
		t.Fatalf("catalog calls = fields %d references %d, want both", fieldCalls, referenceCalls)
	}
}

func assertRestrictedEmptyFieldScope(t *testing.T, options catalog.PopulatedFieldOptions) {
	t.Helper()
	if options.AuthResourcePathsUnrestricted == nil || *options.AuthResourcePathsUnrestricted {
		t.Fatalf("field catalog scope = %#v, want explicit false", options.AuthResourcePathsUnrestricted)
	}
	if len(options.AuthResourcePaths) != 0 {
		t.Fatalf("field catalog paths = %#v, want empty", options.AuthResourcePaths)
	}
}

func assertRestrictedEmptyReferenceScope(t *testing.T, options catalog.PopulatedReferenceOptions) {
	t.Helper()
	if options.AuthResourcePathsUnrestricted == nil || *options.AuthResourcePathsUnrestricted {
		t.Fatalf("reference catalog scope = %#v, want explicit false", options.AuthResourcePathsUnrestricted)
	}
	if len(options.AuthResourcePaths) != 0 {
		t.Fatalf("reference catalog paths = %#v, want empty", options.AuthResourcePaths)
	}
}
