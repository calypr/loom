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

func TestResolveSelectorRequiresCompleteExplicitSelector(t *testing.T) {
	selector, err := resolveSelector(&model.DataframeSelectorInput{Recipe: "documents", TranslationVersion: "v2", Output: "Patient"})
	if err != nil {
		t.Fatal(err)
	}
	if selector.Recipe != "documents" || selector.TranslationVersion != "v2" || selector.Output != "Patient" {
		t.Fatalf("selector = %#v", selector)
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
