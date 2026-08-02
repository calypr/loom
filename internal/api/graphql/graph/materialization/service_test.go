package materializationapi

import (
	"context"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	dfmaterialization "github.com/calypr/loom/internal/dataframe/materialization"
)

func TestAuthorizePublishedAllowsUnrestrictedPrincipal(t *testing.T) {
	service := NewService(Config{})
	ctx := authscope.ContextWithPrincipal(context.Background(), &authscope.Principal{Subject: "operator"})
	if err := service.authorizePublished(ctx, dfmaterialization.Materialization{Project: "project", ScopeUnrestricted: true}); err != nil {
		t.Fatalf("authorizePublished() error = %v", err)
	}
}

func TestAuthorizePublishedHidesOtherProject(t *testing.T) {
	service := NewService(Config{})
	ctx := authscope.ContextWithPrincipal(context.Background(), &authscope.Principal{Projects: []string{"allowed"}})
	err := service.authorizePublished(ctx, dfmaterialization.Materialization{Project: "other", ScopeUnrestricted: true})
	if got := dataframeerrors.Normalize(err).Code(); got != string(dataframeerrors.CodeDatasetNotFound) {
		t.Fatalf("authorizePublished() code = %q, want %q", got, dataframeerrors.CodeDatasetNotFound)
	}
}
