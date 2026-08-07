package materializationapi

import (
	"context"
	"testing"
	"time"

	"github.com/calypr/loom/generated/graphql/graph/model"
	"github.com/calypr/loom/internal/authscope"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	bundlepublication "github.com/calypr/loom/internal/dataframe/publication"
	dfpublished "github.com/calypr/loom/internal/dataframe/published"
)

type emptyPublicationCatalog struct {
	bundlepublication.BundleCatalog
}

func (emptyPublicationCatalog) ListExecutions(context.Context, bundlepublication.BundleState, time.Time) ([]bundlepublication.BundleExecution, error) {
	return []bundlepublication.BundleExecution{}, nil
}

func TestNoAuthCandidateProjectsIncludeObservedUnpublishedProjects(t *testing.T) {
	service := NewService(Config{CandidateProjects: func(context.Context) ([]string, error) {
		return []string{"staged", "failed"}, nil
	}})
	projects, err := service.projects(context.Background(), &authscope.Principal{Subject: "anonymous"})
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 2 || projects[0] != "staged" || projects[1] != "failed" {
		t.Fatalf("candidate projects = %#v", projects)
	}
}

func TestProjectsReportsEmptyPublicationCatalogAsDatasetNotFound(t *testing.T) {
	service := &Service{reader: &dfpublished.Reader{Catalog: emptyPublicationCatalog{}}}

	_, err := service.projects(context.Background(), &authscope.Principal{})
	userErr, ok := dataframeerrors.AsUserError(err)
	if !ok || userErr.Code() != string(dataframeerrors.CodeDatasetNotFound) {
		t.Fatalf("projects() error = %v, want DATASET_NOT_FOUND", err)
	}
}

func TestResolveSelectorRejectsAmbiguousInputs(t *testing.T) {
	service := NewService(Config{DefaultRecipe: "default", DefaultTranslationVersion: "v1"})
	legacy := "Patient"
	_, err := service.resolveSelector(&model.DataframeSelectorInput{Recipe: "other", TranslationVersion: "v2", Output: "Patient"}, &legacy)
	userErr, ok := dataframeerrors.AsUserError(err)
	if !ok || userErr.Code() != string(dataframeerrors.CodeInvalidSelector) {
		t.Fatalf("error = %v", err)
	}
}

func TestResolveLegacyDataTypeUsesPromotedDefaultContract(t *testing.T) {
	service := NewService(Config{DefaultRecipe: "default", DefaultTranslationVersion: "v7"})
	legacy := "Patient"
	selector, err := service.resolveSelector(nil, &legacy)
	if err != nil {
		t.Fatal(err)
	}
	if selector.Recipe != "default" || selector.TranslationVersion != "v7" || selector.Output != "Patient" {
		t.Fatalf("selector = %#v", selector)
	}
}

func TestResolveLegacyDataTypeObservesExplicitContractPromotion(t *testing.T) {
	recipeName, version := "default", "v1"
	service := NewService(Config{DefaultContract: func() (string, string) { return recipeName, version }})
	legacy := "Patient"
	first, err := service.resolveSelector(nil, &legacy)
	if err != nil {
		t.Fatal(err)
	}
	version = "v2"
	second, err := service.resolveSelector(nil, &legacy)
	if err != nil {
		t.Fatal(err)
	}
	if first.TranslationVersion != "v1" || second.TranslationVersion != "v2" {
		t.Fatalf("selectors before/after promotion = %#v %#v", first, second)
	}
}

func TestProjectFilterNarrowsBeforeFederation(t *testing.T) {
	projects := []string{"a", "b", "c"}
	filtered := filterProjects(projects, []dfpublished.Filter{{Column: "project_id", Op: "IN", Value: []any{"b", "c"}}})
	if len(filtered) != 2 || filtered[0] != "b" || filtered[1] != "c" {
		t.Fatalf("filtered = %#v", filtered)
	}
}

func TestProjectStatusesNeverExposeUnauthorizedProjects(t *testing.T) {
	statuses := filterAuthorizedStatuses([]string{"allowed"}, []dfpublished.ProjectStatus{{ProjectID: "allowed"}, {ProjectID: "secret"}})
	if len(statuses) != 1 || statuses[0].ProjectID != "allowed" {
		t.Fatalf("statuses = %#v", statuses)
	}
}
