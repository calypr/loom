package materializationapi

import (
	"context"
	"testing"
	"time"

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

func TestProjectsReportsEmptyPublicationCatalogAsDatasetNotFound(t *testing.T) {
	service := &Service{reader: &dfpublished.Reader{Catalog: emptyPublicationCatalog{}}}

	_, err := service.projects(context.Background(), &authscope.Principal{})
	userErr, ok := dataframeerrors.AsUserError(err)
	if !ok || userErr.Code() != string(dataframeerrors.CodeDatasetNotFound) {
		t.Fatalf("projects() error = %v, want DATASET_NOT_FOUND", err)
	}
}
