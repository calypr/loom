package httpapi_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	httpapi "github.com/calypr/loom/internal/api/http"

	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
)

func TestRecipeControlForeignFaultStaysInternal(t *testing.T) {
	err := errors.New("arango: invalid collection name")
	status, response := httpapi.MapServiceError(err, context.Background())
	if status != http.StatusInternalServerError || response.Error.Code != string(dataframeerrors.CodeInternalError) {
		t.Fatalf("foreign control fault mapped to %d %s", status, response.Error.Code)
	}
	if strings.Contains(response.Error.Message, err.Error()) {
		t.Fatalf("foreign control fault exposed backend text: %q", response.Error.Message)
	}
}
