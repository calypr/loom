package arango

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/arangodb/go-driver/v2/arangodb/shared"
	store "github.com/calypr/loom/internal/backends/arango/internal/store"
	"github.com/calypr/loom/internal/explorer"
)

type draftTransactionFailureClient struct {
	*draftHistoryClient
	failure  error
	attempts int
}

func (c *draftTransactionFailureClient) WithTransaction(context.Context, store.TransactionCollections, store.TransactionFunc) error {
	c.attempts++
	return c.failure
}
func TestSaveDraftTransactionErrorClassification(t *testing.T) {
	conflict := shared.ArangoError{HasError: true, Code: 409, ErrorNum: shared.ErrArangoConflict, ErrorMessage: "write conflict"}
	unique := shared.ArangoError{HasError: true, Code: 409, ErrorNum: 1210, ErrorMessage: "duplicate revision"}
	other := shared.ArangoError{HasError: true, Code: 409, ErrorNum: 1234, ErrorMessage: "other conflict"}
	transport := errors.New("transport unavailable")
	for _, tc := range []struct {
		name      string
		failure   error
		normalize bool
	}{
		{"raw write conflict", conflict, true},
		{"wrapped write conflict", fmt.Errorf("insert history: %w", conflict), true},
		{"existing domain conflict", explorer.ErrDraftConflict, false},
		{"unique constraint", unique, false},
		{"unrelated HTTP conflict", other, false},
		{"cancellation", context.Canceled, false},
		{"deadline", context.DeadlineExceeded, false},
		{"transport", transport, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &draftTransactionFailureClient{draftHistoryClient: &draftHistoryClient{}, failure: tc.failure}
			s, _ := New(client)
			saved, err := s.SaveDraft(context.Background(), explorer.Explorer{Project: "p", ExplorerID: "e"}, 1, "digest")
			if saved != nil || client.attempts != 1 {
				t.Fatalf("saved=%v transaction attempts=%d", saved, client.attempts)
			}
			if tc.normalize {
				var cause shared.ArangoError
				if !errors.Is(err, explorer.ErrDraftConflict) || !errors.Is(err, tc.failure) || !errors.As(err, &cause) || cause.ErrorNum != shared.ErrArangoConflict {
					t.Fatalf("lost domain classification or backend cause: %v", err)
				}
			} else if err != tc.failure {
				t.Fatalf("unrelated error changed: got %v want %v", err, tc.failure)
			}
		})
	}
}
