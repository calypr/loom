package explorerapi

import (
	"context"
	"testing"

	loomapi "github.com/calypr/loom/api/loomapi"
)

func TestExplorerOperationKeepsUnavailableStatusWithoutApplication(t *testing.T) {
	handlers := &Handlers{}
	response, err := handlers.CreateExplorerSelection(context.Background(), loomapi.CreateExplorerSelectionRequestObject{})
	if err != nil {
		t.Fatalf("CreateExplorerSelection() error = %v", err)
	}
	if _, ok := response.(loomapi.CreateExplorerSelection503JSONResponse); !ok {
		t.Fatalf("CreateExplorerSelection() response = %T, want 503", response)
	}
}
