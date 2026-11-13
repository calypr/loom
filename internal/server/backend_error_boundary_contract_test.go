package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/calypr/loom/internal/backends/postgres/control"
	"net/http"
	"strings"
	"testing"

	"github.com/arangodb/go-driver/v2/arangodb/shared"
	httpapi "github.com/calypr/loom/internal/api/http"
	arangofactory "github.com/calypr/loom/internal/backends/arango"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func driveAQLAdapter(t *testing.T, adapterFailure error) error {
	t.Helper()
	adapter := configuredAQLSourceRows(nil, "contract", func(context.Context, dataframeexecution.RowsRequest, dataframeexecution.RowVisitor) error {
		return adapterFailure
	})
	err := adapter(context.Background(), dataframeexecution.RowsRequest{Command: testAQLCommand(t, "RETURN 1", nil), BatchSize: 10}, func(map[string]any) error { return nil })
	if err == nil {
		t.Fatal("adapter failure was swallowed by the composition")
	}
	return err
}

func arangoAdapterFailure(driverErr error) error {
	classified, ok := arangofactory.TranslateQueryError(fmt.Errorf("arango query: %w", driverErr))
	if !ok {
		panic("fixture error was not classified by the adapter")
	}
	return classified
}

func bodyJSON(t *testing.T, mapped httpapi.MappedError) string {
	t.Helper()
	encoded, err := json.Marshal(mapped.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func TestBackendErrorBoundaryContract(t *testing.T) {
	t.Run("arangodb_resource_limits_keep_existing_public_semantics", func(t *testing.T) {
		cases := []struct {
			name       string
			driverErr  shared.ArangoError
			wantCode   string
			wantStatus int
			resource   string
		}{
			{
				name:       "memory_limit",
				driverErr:  shared.ArangoError{HasError: true, Code: 500, ErrorNum: shared.ErrResourceLimit, ErrorMessage: "AQL: query would use more memory than allowed"},
				wantCode:   "QUERY_MEMORY_LIMIT_EXCEEDED",
				wantStatus: http.StatusServiceUnavailable,
				resource:   "query_memory",
			},
			{
				name:       "resource_limit",
				driverErr:  shared.ArangoError{HasError: true, Code: 500, ErrorNum: shared.ErrResourceLimit, ErrorMessage: "resource limit exceeded"},
				wantCode:   "QUERY_RESOURCE_LIMIT_EXCEEDED",
				wantStatus: http.StatusServiceUnavailable,
				resource:   "query",
			},
			{
				name:       "backend_out_of_memory",
				driverErr:  shared.ArangoError{HasError: true, Code: 500, ErrorNum: shared.ErrOutOfMemory, ErrorMessage: "out of memory"},
				wantCode:   "QUERY_BACKEND_OUT_OF_MEMORY",
				wantStatus: http.StatusServiceUnavailable,
				resource:   "memory",
			},
		}
		for _, testCase := range cases {
			t.Run(testCase.name, func(t *testing.T) {
				driven := driveAQLAdapter(t, arangoAdapterFailure(testCase.driverErr))
				userErr, ok := dataframeerrors.AsUserError(driven)
				if !ok || userErr.Code() != testCase.wantCode {
					t.Fatalf("composition lost the classification: %#v", driven)
				}
				mapped := httpapi.MapDataframeError(driven, "req-1")
				if mapped.Status != testCase.wantStatus {
					t.Fatalf("status = %d, want %d", mapped.Status, testCase.wantStatus)
				}
				if mapped.Body.Error.Code != testCase.wantCode {
					t.Fatalf("code = %q, want %q", mapped.Body.Error.Code, testCase.wantCode)
				}
				if mapped.Body.Error.Retryable {
					t.Fatal("resource-limit failures must not advertise a blanket retry")
				}
				if got := mapped.Body.Error.Details["backend"]; got != "arangodb" {
					t.Fatalf("backend detail = %v", got)
				}
				if got := mapped.Body.Error.Details["resource"]; got != testCase.resource {
					t.Fatalf("resource detail = %v, want %q", got, testCase.resource)
				}
				if !errors.Is(driven, testCase.driverErr) {
					t.Fatal("vendor cause identity lost through composition")
				}
			})
		}
	})

	t.Run("arangodb_assertions_keep_existing_public_semantics", func(t *testing.T) {
		for _, code := range []dataframeerrors.ErrorCode{
			dataframeerrors.CodeTablePivotCellCardinality,
			dataframeerrors.CodeTablePivotUnlistedCategory,
			dataframeerrors.CodeConstructionExpansionEmpty,
			dataframeerrors.CodeExplicitGroupUnassignedMember,
			dataframeerrors.CodeRelationshipCardinalityViolation,
		} {
			t.Run(string(code), func(t *testing.T) {
				driverErr := shared.ArangoError{HasError: true, Code: 500, ErrorNum: shared.ErrQueryUserAssert, ErrorMessage: "AQL: " + string(code) + " (while executing)"}
				driven := driveAQLAdapter(t, arangoAdapterFailure(driverErr))
				mapped := httpapi.MapDataframeError(driven, "req-1")
				if mapped.Body.Error.Code != string(code) {
					t.Fatalf("code = %q, want %q", mapped.Body.Error.Code, code)
				}
				if mapped.Status != http.StatusUnprocessableEntity {
					t.Fatalf("status = %d, want 422", mapped.Status)
				}
				if mapped.Body.Error.Retryable {
					t.Fatal("assertion failures must not be retryable")
				}
				if body := bodyJSON(t, mapped); strings.Contains(body, "AQL") {
					t.Fatalf("public body leaked AQL text: %s", body)
				}
			})
		}
	})

	t.Run("postgres_failures_produce_equivalent_public_semantics", func(t *testing.T) {
		pgErr := &pgconn.PgError{
			Code: "57014", SeverityUnlocalized: "ERROR",
			Message: `canceling statement due to statement_timeout; while binding {"secret":"hunter2","bind":"SELECT SECRET_COLUMN"}`,
		}
		translated, ok := control.TranslateError(fmt.Errorf("postgres rows: query: %w", pgErr))
		if !ok {
			t.Fatal("boundary did not classify the server cancel")
		}
		userErr, isUser := dataframeerrors.AsUserError(translated)
		if !isUser || userErr.Code() != string(dataframeerrors.CodeQueryResourceLimitExceeded) {
			t.Fatalf("PG cancel did not reach the shared resource-limit semantics: %#v", translated)
		}
		if userErr.Retryable() {
			t.Fatal("server cancel must not advertise a blanket retry")
		}
		mapped := httpapi.MapDataframeError(translated, "req-2")
		if mapped.Status != http.StatusServiceUnavailable || mapped.Body.Error.Code != "QUERY_RESOURCE_LIMIT_EXCEEDED" {
			t.Fatalf("mapped = %d %q, want 503 QUERY_RESOURCE_LIMIT_EXCEEDED", mapped.Status, mapped.Body.Error.Code)
		}
		if got := mapped.Body.Error.Details["backend"]; got != "postgres" {
			t.Fatalf("backend detail = %v, want postgres", got)
		}
		body := bodyJSON(t, mapped)
		for _, forbidden := range []string{"hunter2", "SECRET_COLUMN", "SELECT", "statement_timeout", "canceling"} {
			if strings.Contains(strings.ToLower(body), strings.ToLower(forbidden)) {
				t.Fatalf("public payload leaked %q: %s", forbidden, body)
			}
		}
	})

	t.Run("cancellation_and_deadline_identity_survive_wrapping", func(t *testing.T) {
		canceled := fmt.Errorf("arango stream: %w", context.Canceled)
		if err := preserveConfiguredQueryError(canceled); !errors.Is(err, context.Canceled) {
			t.Fatalf("preview route lost cancellation identity: %v", err)
		}
		mapped := httpapi.MapDataframeError(canceled, "req-3")
		if mapped.Body.Error.Code != "CLIENT_CANCELED" || mapped.Status != 499 {
			t.Fatalf("canceled mapped = %d %q", mapped.Status, mapped.Body.Error.Code)
		}
		deadline := fmt.Errorf("arango stream: %w", context.DeadlineExceeded)
		if err := preserveConfiguredQueryError(deadline); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("composition rewrote the deadline failure: %v", err)
		}
		mapped = httpapi.MapDataframeError(deadline, "req-4")
		if mapped.Body.Error.Code != "BACKEND_UNAVAILABLE" || !mapped.Body.Error.Retryable {
			t.Fatalf("deadline mapped = %d %q retryable=%t", mapped.Status, mapped.Body.Error.Code, mapped.Body.Error.Retryable)
		}
		poolWait := fmt.Errorf("acquire: %w: %w", control.ErrConnect, context.Canceled)
		translated, ok := control.TranslateError(poolWait)
		if !ok {
			t.Fatalf("pool cancellation not classified by the boundary: %v", poolWait)
		}
		if !errors.Is(translated, context.Canceled) {
			t.Fatalf("PG translation lost the cancellation sentinel: %v", translated)
		}
		if userErr, isUser := dataframeerrors.AsUserError(translated); isUser && userErr.Retryable() {
			t.Fatal("caller cancellation must never be marked retryable")
		}
	})

	t.Run("unknown_unique_violation_is_not_a_cas_conflict", func(t *testing.T) {
		pgErr := &pgconn.PgError{
			Code: "23505", SeverityUnlocalized: "ERROR",
			Message:        `duplicate key value violates unique constraint "uq_explorer_draft_revision_internal"`,
			ConstraintName: "uq_explorer_draft_revision_internal",
			Detail:         `Key (draft_id)=(42) already exists.`,
		}
		translated, ok := control.TranslateError(fmt.Errorf("postgres rows: query: %w", pgErr))
		if !ok {
			t.Fatal("boundary did not classify the unique violation")
		}
		if errors.Is(translated, control.ErrUniqueConflict) {
			t.Fatal("a bare unique violation was labeled as a proven domain conflict")
		}
		if control.RetryableTransactionConflict(translated) {
			t.Fatal("unique violation must not invite transaction retries")
		}
		mapped := httpapi.MapDataframeError(translated, "req-5")
		if mapped.Body.Error.Code != "INTERNAL_ERROR" || mapped.Status != http.StatusInternalServerError {
			t.Fatalf("mapped = %d %q, want 500 INTERNAL_ERROR", mapped.Status, mapped.Body.Error.Code)
		}
		if mapped.Body.Error.Retryable {
			t.Fatal("unproven unique violation must not be retryable")
		}
		body := bodyJSON(t, mapped)
		for _, forbidden := range []string{"uq_explorer_draft_revision_internal", "draft_id", "duplicate key", "violates unique constraint", "already exists"} {
			if strings.Contains(strings.ToLower(body), strings.ToLower(forbidden)) {
				t.Fatalf("public payload leaked constraint material %q: %s", forbidden, body)
			}
		}
	})

	t.Run("ambiguous_commit_stays_distinguishable_from_proven_rollback", func(t *testing.T) {
		proven, ok := control.TranslateCommitError(fmt.Errorf("commit: %w", pgx.ErrTxCommitRollback))
		if !ok {
			t.Fatal("commit rollback not classified")
		}
		if errors.Is(proven, control.ErrCommitOutcomeUnknown) {
			t.Fatal("a proven rollback must not be labeled ambiguous")
		}
		ambiguousPG := &pgconn.PgError{Code: "57P01", SeverityUnlocalized: "FATAL", Message: "terminating connection due to administrator command"}
		ambiguous, ok := control.TranslateCommitError(fmt.Errorf("commit: %w", ambiguousPG))
		if !ok {
			t.Fatal("mid-commit shutdown not classified")
		}
		if !errors.Is(ambiguous, control.ErrCommitOutcomeUnknown) {
			t.Fatal("session death mid-commit must be labeled outcome-unknown")
		}
		var translatedErr *control.TranslatedError
		if !errors.As(ambiguous, &translatedErr) || !translatedErr.AmbiguousCommit {
			t.Fatalf("ambiguous flag missing: %#v", ambiguous)
		}
		mappedAmbiguous := httpapi.MapDataframeError(ambiguous, "req-6")
		mappedProven := httpapi.MapDataframeError(proven, "req-7")
		if mappedAmbiguous.Body.Error.Retryable || mappedProven.Body.Error.Retryable {
			t.Fatal("commit outcomes must never advertise a blanket retry")
		}
		for _, mapped := range []httpapi.MappedError{mappedAmbiguous, mappedProven} {
			body := bodyJSON(t, mapped)
			for _, forbidden := range []string{"terminating connection due to administrator command", "commit unexpectedly resulted in rollback"} {
				if strings.Contains(strings.ToLower(body), strings.ToLower(forbidden)) {
					t.Fatalf("public payload leaked %q: %s", forbidden, body)
				}
			}
		}
	})

	t.Run("retryable_transaction_states_do_not_cause_blanket_write_retries", func(t *testing.T) {
		serialization := &pgconn.PgError{Code: "40001", SeverityUnlocalized: "ERROR", Message: "could not serialize access due to concurrent update"}
		translated, ok := control.TranslateError(fmt.Errorf("postgres rows: query: %w", serialization))
		if !ok {
			t.Fatal("serialization failure not classified")
		}
		if !control.RetryableTransactionConflict(translated) {
			t.Fatal("operation policy must still recognize the serialization conflict")
		}
		userErr, isUser := dataframeerrors.AsUserError(translated)
		if !isUser || userErr.Retryable() {
			t.Fatalf("boundary marked the serialization failure blanket-retryable: %#v", translated)
		}
		mapped := httpapi.MapDataframeError(translated, "req-8")
		if mapped.Body.Error.Retryable {
			t.Fatal("transport must not advertise retry for a serialization conflict")
		}
		body := bodyJSON(t, mapped)
		if strings.Contains(strings.ToLower(body), "could not serialize access") {
			t.Fatalf("public payload leaked the server message: %s", body)
		}
	})
}
