package queryapi

import (
	"context"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
	"github.com/calypr/loom/internal/dataframe"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
)

type fhirLimitResourceAccess struct {
	write bool
}

func (f fhirLimitResourceAccess) GetAllowedResources(_ context.Context, _ string, method, _ string) ([]string, error) {
	if method == string(authscope.PermissionWrite) && !f.write {
		return nil, nil
	}
	return []string{"/programs/example/projects/allowed"}, nil
}

func TestFHIRLimitRequiresProjectWriteAccessAboveReadCap(t *testing.T) {
	for _, test := range []struct {
		name      string
		write     bool
		limit     int
		wantError bool
	}{
		{name: "read-only-at-cap", limit: FHIRMaxReadLimit},
		{name: "read-only-over-cap", limit: FHIRMaxReadLimit + 1, wantError: true},
		{name: "writer-over-cap", write: true, limit: FHIRMaxReadLimit + 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			scopeResolver := authscope.NewScopeResolver(authscope.ScopeResolverConfig{
				ResourceAccess: fhirLimitResourceAccess{write: test.write},
				ListExistingAuthResourcePaths: func(context.Context, catalog.AuthResourcePathOptions) ([]string, error) {
					return []string{"example-allowed"}, nil
				},
			})
			service := NewService(Config{
				ScopeResolver: scopeResolver,
				DiscoverFields: func(context.Context, catalog.PopulatedFieldOptions) ([]catalog.PopulatedField, error) {
					return nil, nil
				},
				Dataframes: dataframe.NewService(dataframe.ServiceConfig{
					ExecuteRows: func(context.Context, dataframe.ExecuteQueryOptions, string, map[string]any, func(map[string]any) error) error {
						return nil
					},
				}),
			})
			ctx := authscope.ContextWithPrincipal(context.Background(), &authscope.Principal{
				AuthorizationHeader: "Bearer header.payload.signature",
			})

			_, err := service.ListFHIR(ctx, FHIRListRequest{
				Project: "P1", ResourceType: "Patient", Limit: test.limit,
			})
			if test.wantError {
				userErr, ok := err.(dataframeerrors.UserError)
				if !ok || userErr.Code() != string(dataframeerrors.CodeInvalidLimit) {
					t.Fatalf("ListFHIR() error = %v, want INVALID_LIMIT", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ListFHIR() error = %v", err)
			}
		})
	}
}
