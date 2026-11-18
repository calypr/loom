package server

import (
	"context"
	"testing"

	loomapi "github.com/calypr/loom/api/loomapi"
)

func TestRegisterRoutesRequiresExplorerHandlers(t *testing.T) {
	if err := registerRoutes(nil, nil, nil, nil); err == nil || err.Error() != "create Explorer HTTP routes: Explorer handlers are required" {
		t.Fatalf("registerRoutes() error = %v, want missing Explorer handlers", err)
	}
}

func TestExplorerOperationKeepsUnavailableStatusWithoutApplication(t *testing.T) {
	routes := &HTTPRoutes{explorer: &explorerHTTPHandlers{}}
	response, err := routes.CreateExplorerSelection(context.Background(), loomapi.CreateExplorerSelectionRequestObject{})
	if err != nil {
		t.Fatalf("CreateExplorerSelection() error = %v", err)
	}
	if _, ok := response.(loomapi.CreateExplorerSelection503JSONResponse); !ok {
		t.Fatalf("CreateExplorerSelection() response = %T, want 503", response)
	}
}
