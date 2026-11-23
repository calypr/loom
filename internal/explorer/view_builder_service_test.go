package explorer

import (
	"context"
	"errors"
	"testing"

	"github.com/calypr/loom/internal/explorer/viewer"
)

type defaultViewBuilderStore struct {
	ExplorerStore
	ViewBuilderRepository
}

func (defaultViewBuilderStore) Get(_ context.Context, project, id string) (*Explorer, error) {
	if project == "HTAN_INT-BForePC" && id == "recipe-view" {
		return &Explorer{Project: project, ExplorerID: id}, nil
	}
	return nil, ErrNotFound
}

func (defaultViewBuilderStore) GetViewBuilderSettings(context.Context, string) (viewer.ViewBuilderSettings, error) {
	return viewer.ViewBuilderSettings{DefaultConfigID: "recipe-view"}, nil
}

func TestLoadExplorerStateResolvesProjectDefault(t *testing.T) {
	service, err := NewService(defaultViewBuilderStore{})
	if err != nil {
		t.Fatal(err)
	}
	state, err := service.LoadExplorerState(context.Background(), "HTAN_INT/BForePC", "default")
	if err != nil {
		t.Fatal(err)
	}
	if state.ExplorerID != "recipe-view" {
		t.Fatalf("expected selected project view, got %q", state.ExplorerID)
	}
	if _, err := service.LoadExplorerState(context.Background(), "HTAN_INT/BForePC", "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("explicit missing Explorer returned %v", err)
	}
}
