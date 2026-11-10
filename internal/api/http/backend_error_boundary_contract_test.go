package httpapi

// TestBackendErrorBoundaryContract is the transport side of the arch-backend-03
// boundary: MapDataframeError consumes Loom errors produced at the adapter
// seams (never vendor types) and produces the existing public semantics —
// correct status, code, message, and safe details — with no SQL, query text,
// bind values, constraint names, or credentials in any public payload.
// Equivalent supported database failures map identically across backends.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/arangodb/go-driver/v2/arangodb/shared"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	arangostore "github.com/calypr/loom/internal/store/arango"
	postgresstore "github.com/calypr/loom/internal/store/postgres"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func classifiedArangoFailure(t *testing.T, driverErr error) error {
	t.Helper()
	classified, ok := arangostore.TranslateQueryError(fmt.Errorf("arango query: %w", driverErr))
	if !ok {
		t.Fatalf("adapter fixture was not classified: %v", driverErr)
	}
	return classified
}

func classifiedPostgresFailure(t *testing.T, driverErr error) error {
	t.Helper()
	translated, ok := postgresstore.TranslateError(fmt.Errorf("postgres rows: query: %w", driverErr))
	if !ok {
		t.Fatalf("boundary fixture was not classified: %v", driverErr)
	}
	return translated
}

func mustBody(t *testing.T, mapped MappedError) string {
	t.Helper()
	encoded, err := json.Marshal(mapped.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func TestBackendErrorBoundaryContract(t *testing.T) {
	t.Run("map_consumes_only_loom_errors", func(t *testing.T) {
		// Raw vendor failures reaching the transport unclassified are
		// redacted to INTERNAL_ERROR with no vendor detail: the mapper never
		// interprets them itself.
		rawArango := fmt.Errorf("arango query: %w", shared.ArangoError{HasError: true, Code: 500, ErrorNum: 4, ErrorMessage: "boom hunter2"})
		mapped := MapDataframeError(rawArango, "req-raw")
		if mapped.Status != http.StatusInternalServerError || mapped.Body.Error.Code != "INTERNAL_ERROR" {
			t.Fatalf("raw vendor error mapped to %d %q; transport must not interpret vendor failures", mapped.Status, mapped.Body.Error.Code)
		}
		if body := mustBody(t, mapped); strings.Contains(body, "hunter2") || strings.Contains(body, "boom") {
			t.Fatalf("raw vendor detail leaked: %s", body)
		}
		rawPG := &pgconn.PgError{Code: "XX001", Message: "internal server catastrophe hunter2"}
		mapped = MapDataframeError(rawPG, "req-raw-pg")
		if mapped.Body.Error.Code != "INTERNAL_ERROR" {
			t.Fatalf("raw PG error interpreted by transport: %q", mapped.Body.Error.Code)
		}
		if body := mustBody(t, mapped); strings.Contains(body, "hunter2") || strings.Contains(body, "catastrophe") {
			t.Fatalf("raw PG detail leaked: %s", body)
		}
	})

	t.Run("equivalent_backend_failures_map_identically", func(t *testing.T) {
		// The ArangoDB configured-limit rejection and the PostgreSQL
		// server-policy cancel (SQLSTATE 57014) are equivalent supported
		// failures: same public code family, same retry discipline. Uses the
		// merged boundary's own severity literal spelling ("ERROR" as sent,
		// "FATAL"/"PANIC" where relevant), never new ones.
		arangoMapped := MapDataframeError(classifiedArangoFailure(t, shared.ArangoError{
			HasError: true, Code: 500, ErrorNum: shared.ErrResourceLimit, ErrorMessage: "AQL: query would use more memory than allowed",
		}), "req-a")
		pgMapped := MapDataframeError(classifiedPostgresFailure(t, &pgconn.PgError{
			Code: "57014", SeverityUnlocalized: "ERROR", Message: "canceling statement due to statement_timeout",
		}), "req-p")
		if arangoMapped.Status != http.StatusServiceUnavailable || pgMapped.Status != http.StatusServiceUnavailable {
			t.Fatalf("statuses = %d/%d, want 503/503", arangoMapped.Status, pgMapped.Status)
		}
		if arangoMapped.Body.Error.Retryable || pgMapped.Body.Error.Retryable {
			t.Fatal("neither backend's limit failure may advertise a blanket retry")
		}
		if pgMapped.Body.Error.Code != "QUERY_RESOURCE_LIMIT_EXCEEDED" {
			t.Fatalf("PG cancel code = %q, want QUERY_RESOURCE_LIMIT_EXCEEDED", pgMapped.Body.Error.Code)
		}
		for _, mapped := range []MappedError{arangoMapped, pgMapped} {
			body := mustBody(t, mapped)
			for _, forbidden := range []string{"AQL", "statement_timeout", "SELECT", "bind", "hunter2"} {
				if strings.Contains(strings.ToLower(body), strings.ToLower(forbidden)) {
					t.Fatalf("public payload leaked %q: %s", forbidden, body)
				}
			}
		}
	})

	t.Run("public_details_stay_safe", func(t *testing.T) {
		classified := classifiedArangoFailure(t, shared.ArangoError{
			HasError: true, Code: 500, ErrorNum: shared.ErrResourceLimit,
			ErrorMessage: `AQL: memory cap hit while evaluating {"token":"secret-token-value","password":"hunter2"}`,
		})
		mapped := MapDataframeError(classified, "req-safe")
		body := mustBody(t, mapped)
		for _, forbidden := range []string{"secret-token-value", "hunter2", "AQL", "memory cap"} {
			if strings.Contains(strings.ToLower(body), strings.ToLower(forbidden)) {
				t.Fatalf("public payload leaked %q: %s", forbidden, body)
			}
		}
		if mapped.Body.Error.Details["backend"] != "arangodb" || mapped.Body.Error.Details["resource"] != "query_memory" {
			t.Fatalf("safe details missing: %v", mapped.Body.Error.Details)
		}
	})

	t.Run("cancellation_and_deadline_identity_through_wrapping", func(t *testing.T) {
		canceled := fmt.Errorf("stream aborted: %w", context.Canceled)
		mapped := MapDataframeError(canceled, "req-c")
		if mapped.Body.Error.Code != "CLIENT_CANCELED" || mapped.Status != 499 {
			t.Fatalf("canceled = %d %q", mapped.Status, mapped.Body.Error.Code)
		}
		deadline := fmt.Errorf("stream deadline: %w", context.DeadlineExceeded)
		mapped = MapDataframeError(deadline, "req-d")
		if mapped.Body.Error.Code != "BACKEND_UNAVAILABLE" || !mapped.Body.Error.Retryable {
			t.Fatalf("deadline = %d %q retryable=%t", mapped.Status, mapped.Body.Error.Code, mapped.Body.Error.Retryable)
		}
		// PG boundary keeps the sentinel reachable and never marks a caller
		// cancel retryable.
		pgCanceled := fmt.Errorf("acquire: %w: %w", postgresstore.ErrConnect, context.Canceled)
		translated, ok := postgresstore.TranslateError(pgCanceled)
		if !ok || !errors.Is(translated, context.Canceled) {
			t.Fatalf("boundary lost cancellation identity: %v", translated)
		}
		mapped = MapDataframeError(translated, "req-pc")
		if mapped.Body.Error.Retryable {
			t.Fatal("caller cancellation must never be retryable")
		}
	})

	t.Run("unique_violation_never_becomes_a_cas_conflict", func(t *testing.T) {
		translated := classifiedPostgresFailure(t, &pgconn.PgError{
			Code: "23505", SeverityUnlocalized: "ERROR",
			Message:        `duplicate key value violates unique constraint "uq_cas_revision"`,
			ConstraintName: "uq_cas_revision",
		})
		if errors.Is(translated, postgresstore.ErrUniqueConflict) {
			t.Fatal("unproven unique violation presented as domain conflict")
		}
		mapped := MapDataframeError(translated, "req-u")
		if mapped.Body.Error.Code != "INTERNAL_ERROR" || mapped.Body.Error.Retryable {
			t.Fatalf("unique violation mapped = %d %q retryable=%t", mapped.Status, mapped.Body.Error.Code, mapped.Body.Error.Retryable)
		}
		// The merged boundary's stable public fields (backend, condition,
		// sqlstate) may appear; server-provided message text, constraint
		// names, and column material may not.
		body := mustBody(t, mapped)
		for _, forbidden := range []string{"uq_cas_revision", "duplicate key", "violates unique constraint"} {
			if strings.Contains(strings.ToLower(body), strings.ToLower(forbidden)) {
				t.Fatalf("public payload leaked %q: %s", forbidden, body)
			}
		}
		// A targeted CAS caller that proves the statement meaning can still
		// upgrade it privately; the public mapping is unchanged either way.
		if _, ok := postgresstore.AsUniqueConflict(translated); !ok {
			t.Fatal("targeted upgrade path lost")
		}
	})

	t.Run("ambiguous_commit_is_distinguishable_from_proven_rollback", func(t *testing.T) {
		proven, ok := postgresstore.TranslateCommitError(fmt.Errorf("commit: %w", pgx.ErrTxCommitRollback))
		if !ok {
			t.Fatal("proven rollback not classified")
		}
		if errors.Is(proven, postgresstore.ErrCommitOutcomeUnknown) {
			t.Fatal("proven rollback labeled ambiguous")
		}
		ambiguous, ok := postgresstore.TranslateCommitError(fmt.Errorf("commit: %w",
			&pgconn.PgError{Code: "57P01", SeverityUnlocalized: "FATAL", Message: "terminating connection due to administrator command"}))
		if !ok {
			t.Fatal("mid-commit session death not classified")
		}
		if !errors.Is(ambiguous, postgresstore.ErrCommitOutcomeUnknown) {
			t.Fatal("mid-commit session death must be outcome-unknown")
		}
		provenMapped := MapDataframeError(proven, "req-proven")
		ambiguousMapped := MapDataframeError(ambiguous, "req-amb")
		if provenMapped.Body.Error.Retryable || ambiguousMapped.Body.Error.Retryable {
			t.Fatal("no commit outcome may advertise a blanket retry")
		}
		// Both stay distinguishable to callers through sentinels even though
		// both redact publicly.
		if ambiguousMapped.Body.Error.Code != provenMapped.Body.Error.Code {
			t.Fatal("public codes unexpectedly differ")
		}
	})

	t.Run("serialization_conflicts_stay_policy_only", func(t *testing.T) {
		translated := classifiedPostgresFailure(t, &pgconn.PgError{Code: "40001", SeverityUnlocalized: "ERROR", Message: "could not serialize access due to concurrent update"})
		if !postgresstore.RetryableTransactionConflict(translated) {
			t.Fatal("operation policy recognition lost")
		}
		userErr, isUser := dataframeerrors.AsUserError(translated)
		if !isUser || userErr.Retryable() {
			t.Fatalf("boundary marked serialization failure blanket-retryable: %#v", translated)
		}
		mapped := MapDataframeError(translated, "req-s")
		if mapped.Body.Error.Retryable {
			t.Fatal("transport must not advertise retry for transaction conflicts")
		}
		body := mustBody(t, mapped)
		// The stable condition name and SQLSTATE are the boundary's public
		// fields; the server's message text stays redacted.
		if strings.Contains(strings.ToLower(body), "could not serialize access") {
			t.Fatalf("public payload leaked the server message: %s", body)
		}
	})
}
