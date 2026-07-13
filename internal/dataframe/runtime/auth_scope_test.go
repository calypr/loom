package runtime

import (
	"context"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
)

type restrictedEmptyResourceAccess struct{}

func (restrictedEmptyResourceAccess) GetAllowedResources(context.Context, string, string, string) ([]string, error) {
	return []string{"/programs/example/projects/allowed"}, nil
}

func restrictedEmptyScopeResolver() *authscope.ScopeResolver {
	return authscope.NewScopeResolver(authscope.ScopeResolverConfig{
		ResourceAccess: restrictedEmptyResourceAccess{},
		ListExistingAuthResourcePaths: func(context.Context, catalog.AuthResourcePathOptions) ([]string, error) {
			return []string{"example-different"}, nil
		},
	})
}

func TestServiceRestrictedEmptyScopeStaysRestrictedInCatalogAndAQL(t *testing.T) {
	catalogCalls := 0
	svc := NewService(ServiceConfig{
		ScopeResolver: restrictedEmptyScopeResolver(),
		DiscoverReferences: func(_ context.Context, opts catalog.PopulatedReferenceOptions) ([]catalog.PopulatedReference, error) {
			assertRestrictedEmptyReferenceOptions(t, opts)
			return []catalog.PopulatedReference{}, nil
		},
		DiscoverFields: func(_ context.Context, opts catalog.PopulatedFieldOptions) ([]catalog.PopulatedField, error) {
			catalogCalls++
			assertRestrictedEmptyFieldOptions(t, opts)
			return []catalog.PopulatedField{}, nil
		},
		ExecuteRows: func(_ context.Context, _ ExecuteQueryOptions, query string, bindVars map[string]any, _ func(map[string]any) error) error {
			if got, ok := bindVars["auth_resource_paths_unrestricted"].(bool); !ok || got {
				t.Fatalf("dataframe unrestricted bind = %#v, want false", bindVars["auth_resource_paths_unrestricted"])
			}
			if !strings.Contains(query, "LET root_scope_allowed = @auth_resource_paths_unrestricted == true OR root.auth_resource_path IN @auth_resource_paths") ||
				!strings.Contains(query, "FILTER root_scope_allowed == @scope_allowed") {
				t.Fatalf("dataframe query lost root auth scope:\n%s", query)
			}
			return nil
		},
	})

	ctx := authscope.ContextWithPrincipal(context.Background(), &authscope.Principal{
		AuthorizationHeader: "Bearer header.payload.signature",
	})
	result, err := svc.Run(ctx, RunRequest{Builder: Builder{
		Project:          "P1",
		RootResourceType: "Patient",
	}})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.RowCount != 0 {
		t.Fatalf("result row count = %d, want no rows from the scoped executor", result.RowCount)
	}
	if catalogCalls == 0 {
		t.Fatal("expected scope-aware catalog validation calls")
	}
}

func TestGenericPhysicalPlanRestrictedEmptyScopeBindsFalse(t *testing.T) {
	plan, err := BuildGenericPhysicalPlan(SemanticPlan{
		Version:       1,
		Project:       "P1",
		AuthScopeMode: authscope.ReadScopeRestricted,
		Root:          SemanticNode{Alias: "root", ResourceType: "Patient"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := plan.BindVars["auth_resource_paths_unrestricted"].(bool); !ok || got {
		t.Fatalf("physical unrestricted bind = %#v, want false", plan.BindVars["auth_resource_paths_unrestricted"])
	}
	rendered, err := RenderPhysicalPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := rendered.BindVars["auth_resource_paths_unrestricted"].(bool); !ok || got {
		t.Fatalf("rendered physical unrestricted bind = %#v, want false", rendered.BindVars["auth_resource_paths_unrestricted"])
	}
}

func assertRestrictedEmptyFieldOptions(t *testing.T, opts catalog.PopulatedFieldOptions) {
	t.Helper()
	if opts.AuthResourcePathsUnrestricted == nil || *opts.AuthResourcePathsUnrestricted {
		t.Fatalf("field catalog scope mode = %#v, want explicit false", opts.AuthResourcePathsUnrestricted)
	}
	if len(opts.AuthResourcePaths) != 0 {
		t.Fatalf("field catalog paths = %#v, want empty", opts.AuthResourcePaths)
	}
}

func assertRestrictedEmptyReferenceOptions(t *testing.T, opts catalog.PopulatedReferenceOptions) {
	t.Helper()
	if opts.AuthResourcePathsUnrestricted == nil || *opts.AuthResourcePathsUnrestricted {
		t.Fatalf("reference catalog scope mode = %#v, want explicit false", opts.AuthResourcePathsUnrestricted)
	}
	if len(opts.AuthResourcePaths) != 0 {
		t.Fatalf("reference catalog paths = %#v, want empty", opts.AuthResourcePaths)
	}
}
