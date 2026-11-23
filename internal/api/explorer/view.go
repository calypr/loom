package explorerapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/calypr/loom/internal/explorer/viewer"

	loomapi "github.com/calypr/loom/api/loomapi"
	httpapi "github.com/calypr/loom/internal/api/http"
	"github.com/calypr/loom/internal/explorer"
)

func viewBuilderFailure(err error) error {
	switch {
	case errors.Is(err, explorer.ErrDraftConflict):
		return legacyOperationFailure(http.StatusConflict, "VIEW_CONFLICT", "This view or its published data changed. Reload before saving; your unsaved edits have been kept.", err)
	case errors.Is(err, explorer.ErrNotFound):
		return legacyOperationFailure(http.StatusNotFound, "VIEW_NOT_FOUND", "The saved view or published dataframe was not found", err)
	case errors.Is(err, viewer.ErrInvalidViewBuilder):
		return legacyOperationFailure(http.StatusUnprocessableEntity, "INVALID_VIEW", err.Error(), err)
	default:
		return legacyOperationFailure(http.StatusServiceUnavailable, "VIEW_BUILDER_UNAVAILABLE", "View storage is temporarily unavailable; retry the request", err)
	}
}

func decodeViewBuilderBody(body any, target any) error {
	if body == nil {
		return legacyOperationFailure(http.StatusBadRequest, "INVALID_REQUEST", "A view request body is required", nil)
	}
	raw, err := json.Marshal(body)
	if err == nil {
		err = httpapi.DecodeStrict(raw, target)
	}
	if err != nil {
		return legacyOperationFailure(http.StatusBadRequest, "INVALID_REQUEST", "The view request contains invalid or unknown fields", err)
	}
	return nil
}

func viewBuilderJSON(value viewer.ViewBuilderState) (loomapi.RawJSON, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var result loomapi.RawJSON
	err = json.Unmarshal(raw, &result)
	return result, err
}
