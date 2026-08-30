package dataframe

import (
	"context"
	"testing"

	"github.com/calypr/loom/generated/graphql/graph/model"
	"github.com/calypr/loom/internal/authscope"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	dfmaterialization "github.com/calypr/loom/internal/dataframe/published"
)

func TestResolveSelectorRequiresCompleteExplicitSelector(t *testing.T) {
	selector, err := resolveSelector(&model.DataframeSelectorInput{Recipe: "documents", TranslationVersion: "v2", Output: "Patient"})
	if err != nil {
		t.Fatal(err)
	}
	if selector.Recipe != "documents" || selector.TranslationVersion != "v2" || selector.Output != "Patient" {
		t.Fatalf("selector = %#v", selector)
	}
}

func TestResolveSelectorRejectsMissingSelector(t *testing.T) {
	_, err := resolveSelector(nil)
	userErr, ok := dataframeerrors.AsUserError(err)
	if !ok || userErr.Code() != string(dataframeerrors.CodeInvalidSelector) {
		t.Fatalf("error = %v", err)
	}
}

func TestAuthorizeProjectUsesPrincipalProjectGrant(t *testing.T) {
	service := NewService(Config{})
	principal := &authscope.Principal{Subject: "user", Projects: []string{"P1"}}
	if _, err := service.authorizeProject(context.Background(), principal, "P1"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.authorizeProject(context.Background(), principal, "P2"); err == nil {
		t.Fatal("unauthorized project was accepted")
	}
}

func TestAuthorizeProjectRequiresProject(t *testing.T) {
	service := NewService(Config{})
	_, err := service.authorizeProject(context.Background(), &authscope.Principal{}, " ")
	userErr, ok := dataframeerrors.AsUserError(err)
	if !ok || userErr.Code() != string(dataframeerrors.CodeInvalidRequest) {
		t.Fatalf("error = %v", err)
	}
}

func TestAuthorizeRestrictedMaterializationRequiresResourcePathsWithoutResolver(t *testing.T) {
	service := NewService(Config{})
	principal := &authscope.Principal{Subject: "user", Projects: []string{"P1"}}
	_, err := service.authorizeMaterialization(context.Background(), principal, dfmaterialization.Materialization{Project: "P1"})
	userErr, ok := dataframeerrors.AsUserError(err)
	if !ok || userErr.Code() != string(dataframeerrors.CodeDatasetNotFound) {
		t.Fatalf("error = %v", err)
	}
}

func TestAuthorizeUnrestrictedMaterializationWithoutResolver(t *testing.T) {
	service := NewService(Config{})
	principal := &authscope.Principal{Subject: "user", Projects: []string{"P1"}}
	access, err := service.authorizeMaterialization(context.Background(), principal, dfmaterialization.Materialization{Project: "P1", ScopeUnrestricted: true})
	if err != nil {
		t.Fatal(err)
	}
	if !access.unrestricted || len(access.authResourcePaths) != 0 {
		t.Fatalf("access = %#v", access)
	}
}
