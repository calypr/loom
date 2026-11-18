package explorerapi

import (
	"context"
	"errors"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/constructioninputs"
)

func TestConstructionInputsDirectHandlerChecksProjectReadBeforeCatalog(t *testing.T) {
	handler := &Handlers{
		authorizeRead: func(context.Context, *authscope.Principal, string) error { return errors.New("denied") },
	}
	_, err := handler.readConstructionInputs(context.Background(), "project-a", "builder-a", &constructioninputs.Request{
		SnapshotToken: "snapshot", ExpectedDraftVersion: 1, ExpectedDraftDigest: "digest",
	})
	var authoringErr *explorer.AuthoringError
	if !errors.As(err, &authoringErr) || authoringErr.Status != 403 {
		t.Fatalf("project read denial = %v, want 403 authoring error", err)
	}
}
