package postgres

// Runtime error-boundary tests for errors.go (ticket pg-runtime-error-boundary).
//
// Coverage mirrors the admission error-condition bar: SQLSTATE 57014,
// 40001, 40P01, 23505, 25P02, admin shutdown (57P01), and connection loss
// each map to a stable typed Loom error, and ZERO SQL text, bound argument
// values, DSN material, or schema/constraint names may appear in any
// rendered error surface (asserted on rendered strings, including the JSON
// HTTP payload). Real PostgreSQL 18 witnesses come from
// LOOM_TEST_POSTGRES_URL; fixtures are uniquely owned as verify_reb_* and
// audited ownership-scoped (exact names plus own-prefix baseline count —
// never a global sweep). Offline runs skip the live tests honestly; the
// synthetic suite still runs everywhere.
//
// Non-vacuity: every guard has a paired control that passes (a translation
// that dropped or swallowed everything would fail its controls), each live
// condition pins the witnessed raw SQLSTATE before translation, and each
// predicate has negative controls on near-miss errors.

import (
	"context"
	"crypto/rand"
	"database/sql/driver"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	httpapi "github.com/calypr/loom/internal/api/http"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ---------------------------------------------------------------------------
// Secret canaries and rendering assertions (synthetic suite).
// ---------------------------------------------------------------------------

// Synthetic canaries that must never appear in any rendered error surface.
const (
	rebCanarySQL        = "SELECT reb_canary_select FROM reb_schema_canary.reb_table_canary WHERE reb_col_canary = 'reb_bind_canary'"
	rebCanaryDSN        = "postgres://rebuser:rebpassword@rebhost:5432/rebdb"
	rebCanaryConstraint = "reb_secret_ux"
	rebCanarySchema     = "reb_secret_schema"
	rebCanaryTable      = "reb_secret_table"
	rebCanaryDetail     = "key (reb_col_canary)=(reb_bind_canary) already exists."
)

var rebForbidden = []string{
	"reb_canary_select", "reb_schema_canary", "reb_table_canary",
	"reb_col_canary", "reb_bind_canary",
	"rebuser", "rebpassword", "rebhost", "rebdb",
	rebCanaryConstraint, rebCanarySchema, rebCanaryTable,
	"already exists", "duplicate key", "canceling statement",
	"deadlock detected", "could not serialize", "current transaction is aborted",
}

// rebSecretPgError builds a server error whose every free-text field carries
// leakage bait: messages, details, hints, schema/table/constraint names, and
// the internal query text.
func rebSecretPgError(severity, code string) *pgconn.PgError {
	return &pgconn.PgError{
		Severity:            severity,
		SeverityUnlocalized: severity,
		Code:                code,
		Message:             fmt.Sprintf("leak bait for %s: duplicate key value violates unique constraint %q", code, rebCanaryConstraint),
		Detail:              rebCanaryDetail,
		Hint:                "use " + rebCanarySQL,
		InternalQuery:       rebCanarySQL,
		Where:               "reb_frame_function() " + rebCanarySQL,
		SchemaName:          rebCanarySchema,
		TableName:           rebCanaryTable,
		ColumnName:          "reb_col_canary",
		ConstraintName:      rebCanaryConstraint,
	}
}

// rebHttpJSON renders the exact HTTP payload the transport would write.
func rebHttpJSON(t *testing.T, err error) (string, httpapi.MappedError) {
	t.Helper()
	mapped := httpapi.MapDataframeError(err, "reb-test")
	body, err2 := json.Marshal(mapped.Body)
	if err2 != nil {
		t.Fatalf("marshal mapped body: %v", err2)
	}
	return string(body), mapped
}

// rebAssertSafeRendering asserts zero leakage across every rendered surface.
func rebAssertSafeRendering(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("nil error handed to rendering assertion")
	}
	renderings := map[string]string{
		"Error()":   err.Error(),
		"%v":        fmt.Sprintf("%v", err),
		"%+v":       fmt.Sprintf("%+v", err),
		"%s":        fmt.Sprintf("%s", err),
		"publicMsg": dataframeerrors.PublicMessage(err),
	}
	body, _ := rebHttpJSON(t, err)
	renderings["httpJSON"] = body
	if userErr, ok := dataframeerrors.AsUserError(err); ok {
		details, _ := json.Marshal(userErr.Details())
		renderings["details"] = string(details)
		renderings["userErrText"] = userErr.Error()
	}
	for surface, text := range renderings {
		for _, forbidden := range rebForbidden {
			if strings.Contains(text, forbidden) {
				t.Fatalf("%s leaks forbidden token %q: %s", surface, forbidden, text)
			}
		}
	}
}

// rebUserErr extracts the embedded typed error for semantic assertions.
func rebUserErr(t *testing.T, err error) dataframeerrors.UserError {
	t.Helper()
	userErr, ok := dataframeerrors.AsUserError(err)
	if !ok {
		t.Fatalf("translated error carries no typed Loom error: %#v", err)
	}
	return userErr
}

// ---------------------------------------------------------------------------
// Guard non-vacuity: pass-through and idempotence.
// ---------------------------------------------------------------------------

func TestTranslatePostgresErrorNilAndPassThrough(t *testing.T) {
	if translated, ok := TranslateError(nil); translated != nil || ok {
		t.Fatalf("TranslateError(nil) = (%v, %v), want (nil, false)", translated, ok)
	}

	foreign := errors.New("private backend details reb-not-postgres")
	if translated, ok := TranslateError(foreign); translated != foreign || ok {
		t.Fatalf("TranslateError(foreign) = (%v, %v), want unchanged,false so Arango behavior stays untouched", translated, ok)
	}

	typed := dataframeerrors.NewError(dataframeerrors.CodeStaleCursor, "")
	if translated, ok := TranslateError(typed); translated != typed || !ok {
		t.Fatalf("TranslateError(typed) = (%v, %v), want unchanged,true (composition, never re-wrap)", translated, ok)
	}

	first, ok := TranslateError(rebSecretPgError("ERROR", sqlStateDeadlockDetected))
	if !ok {
		t.Fatal("first translation refused a PostgreSQL error")
	}
	if _, isTranslated := first.(*TranslatedError); !isTranslated {
		t.Fatalf("first translation produced %T, want *TranslatedError", first)
	}
	if second, ok := TranslateError(first); second != first || !ok {
		t.Fatalf("re-translating a translated error = (%v, %v), want identical pass-through", second, ok)
	}
	if second, ok := TranslateError(fmt.Errorf("reclassified: %w", first)); second == nil || !ok {
		t.Fatalf("re-translating a wrapped translated error = (%v, %v), want accepted unchanged", second, ok)
	}
}

func TestTranslatePostgresErrorPredicatesNonVacuity(t *testing.T) {
	uniqueErr, ok := TranslateError(rebSecretPgError("ERROR", sqlStateUniqueViolation))
	if !ok {
		t.Fatal("unique violation not translated")
	}
	deadlockErr, ok := TranslateError(rebSecretPgError("ERROR", sqlStateDeadlockDetected))
	if !ok {
		t.Fatal("deadlock not translated")
	}
	abortedErr, ok := TranslateError(rebSecretPgError("ERROR", sqlStateAbortedTransaction))
	if !ok {
		t.Fatal("aborted transaction not translated")
	}

	if !IsUniqueViolation(uniqueErr) {
		t.Fatal("IsUniqueViolation must recognize the unique violation (positive control)")
	}
	if IsUniqueViolation(deadlockErr) {
		t.Fatal("IsUniqueViolation must reject a deadlock")
	}
	if IsUniqueViolation(nil) || IsUniqueViolation(errors.New("foreign reb-not-postgres")) {
		t.Fatal("IsUniqueViolation must reject nil and foreign errors")
	}

	if !IsAbortedTransaction(abortedErr) {
		t.Fatal("IsAbortedTransaction must recognize 25P02 (positive control)")
	}
	if IsAbortedTransaction(uniqueErr) {
		t.Fatal("IsAbortedTransaction must reject a unique violation")
	}

	if !RetryableTransactionConflict(deadlockErr) {
		t.Fatal("RetryableTransactionConflict must recognize the deadlock (positive control)")
	}
	serialErr, _ := TranslateError(rebSecretPgError("ERROR", sqlStateSerializationFailure))
	if !RetryableTransactionConflict(serialErr) {
		t.Fatal("RetryableTransactionConflict must recognize a serialization failure")
	}
	if RetryableTransactionConflict(uniqueErr) {
		t.Fatal("RetryableTransactionConflict must reject a unique violation")
	}
	if RetryableTransactionConflict(errors.New("foreign reb-not-postgres")) {
		t.Fatal("RetryableTransactionConflict must reject foreign errors")
	}

	// Raw (untranslated) chains also drive the predicates: policy code may
	// consult them before any translation happened.
	if !RetryableTransactionConflict(fmt.Errorf("seam: %w", rebSecretPgError("ERROR", sqlStateSerializationFailure))) {
		t.Fatal("RetryableTransactionConflict must see raw unwrapped-chain SQLSTATEs")
	}

	if _, ok := TranslateCommitError(errors.New("foreign reb-not-postgres")); ok {
		t.Fatal("TranslateCommitError must reject foreign errors")
	}
	if _, ok := TranslateCommitError(nil); ok {
		t.Fatal("TranslateCommitError(nil) must reject nil")
	}
}

// ---------------------------------------------------------------------------
// Connectivity conditions (synthetic).
// ---------------------------------------------------------------------------

func TestTranslatePostgresErrorConnectionConditions(t *testing.T) {
	t.Run("client connect failure", func(t *testing.T) {
		raw := &ConnectError{Op: "open", Err: fmt.Errorf("dial rebhost:5432: %w", syscall.ECONNREFUSED)}
		translated, ok := TranslateError(raw)
		if !ok {
			t.Fatal("client ConnectError not translated")
		}
		translatedErr, isType := translated.(*TranslatedError)
		if !isType || translatedErr.Condition != ConditionConnectionFailure {
			t.Fatalf("condition = %#v, want connection_failure", translated)
		}
		if !errors.Is(translated, ErrConnect) {
			t.Fatal("translated connect failure must still match ErrConnect (pg-store-01 contract)")
		}
		userErr := rebUserErr(t, translated)
		if userErr.Code() != string(dataframeerrors.CodeBackendUnavailable) || !userErr.Retryable() {
			t.Fatalf("connect failure = %s retryable=%v, want retryable BACKEND_UNAVAILABLE", userErr.Code(), userErr.Retryable())
		}
		_, mapped := rebHttpJSON(t, translated)
		if mapped.Status != 503 || !mapped.Body.Error.Retryable {
			t.Fatalf("mapped = %#v, want 503 retryable", mapped)
		}
		rebAssertSafeRendering(t, translated)
	})

	t.Run("acquire deadline", func(t *testing.T) {
		raw := &ConnectError{Op: "acquire", Err: context.DeadlineExceeded}
		translated, ok := TranslateError(raw)
		if !ok {
			t.Fatal("acquire deadline not translated")
		}
		if !errors.Is(translated, context.DeadlineExceeded) {
			t.Fatal("acquire deadline must stay Is-visible for lifecycle classification")
		}
		if !errors.Is(translated, ErrConnect) {
			t.Fatal("translated acquire failure must match ErrConnect")
		}
		userErr := rebUserErr(t, translated)
		if userErr.Code() != string(dataframeerrors.CodeBackendUnavailable) || !userErr.Retryable() {
			t.Fatalf("acquire deadline = %s retryable=%v, want retryable BACKEND_UNAVAILABLE", userErr.Code(), userErr.Retryable())
		}
		rebAssertSafeRendering(t, translated)
	})

	t.Run("acquire canceled", func(t *testing.T) {
		raw := &ConnectError{Op: "acquire", Err: fmt.Errorf("wait: %w", context.Canceled)}
		translated, ok := TranslateError(raw)
		if !ok {
			t.Fatal("canceled acquire not translated")
		}
		if !errors.Is(translated, context.Canceled) {
			t.Fatal("canceled acquire must stay Is-visible as context.Canceled")
		}
		translatedErr := translated.(*TranslatedError)
		if translatedErr.Condition != ConditionRequestCanceled {
			t.Fatalf("condition = %s, want request_canceled", translatedErr.Condition)
		}
		userErr := rebUserErr(t, translated)
		if userErr.Code() != string(dataframeerrors.CodeClientCanceled) || userErr.Retryable() {
			t.Fatalf("canceled acquire = %s retryable=%v, want non-retryable CLIENT_CANCELED", userErr.Code(), userErr.Retryable())
		}
		_, mapped := rebHttpJSON(t, translated)
		if mapped.Status != 499 {
			t.Fatalf("mapped status = %d, want 499", mapped.Status)
		}
		rebAssertSafeRendering(t, translated)
	})

	t.Run("driver conn closed", func(t *testing.T) {
		raw := fmt.Errorf("stream: %w", pgconn.ErrConnClosed)
		translated, ok := TranslateError(raw)
		if !ok {
			t.Fatal("pgconn.ErrConnClosed not translated")
		}
		if translatedErr, isType := translated.(*TranslatedError); !isType || translatedErr.Condition != ConditionConnectionLost {
			t.Fatalf("condition = %#v, want connection_lost", translated)
		}
		userErr := rebUserErr(t, translated)
		if userErr.Code() != string(dataframeerrors.CodeBackendUnavailable) || !userErr.Retryable() {
			t.Fatalf("conn closed = %s retryable=%v, want retryable BACKEND_UNAVAILABLE", userErr.Code(), userErr.Retryable())
		}
		rebAssertSafeRendering(t, translated)
	})

	t.Run("bad conn", func(t *testing.T) {
		raw := fmt.Errorf("pool: %w", driver.ErrBadConn)
		translated, ok := TranslateError(raw)
		if !ok {
			t.Fatal("driver.ErrBadConn not translated")
		}
		userErr := rebUserErr(t, translated)
		if userErr.Code() != string(dataframeerrors.CodeBackendUnavailable) || !userErr.Retryable() {
			t.Fatalf("bad conn = %s retryable=%v, want retryable BACKEND_UNAVAILABLE", userErr.Code(), userErr.Retryable())
		}
		rebAssertSafeRendering(t, translated)
	})

	t.Run("pure context errors stay owned sentinels", func(t *testing.T) {
		// Cancellation not routed through the pool must pass through
		// untouched so existing lifecycle semantics keep owning it.
		canceled := fmt.Errorf("query: %w", context.Canceled)
		if translated, ok := TranslateError(canceled); translated != canceled || ok {
			t.Fatalf("pure context.Canceled = (%v, %v), want pass-through", translated, ok)
		}
		deadline := fmt.Errorf("query: %w", context.DeadlineExceeded)
		if translated, ok := TranslateError(deadline); translated != deadline || ok {
			t.Fatalf("pure context.DeadlineExceeded = (%v, %v), want pass-through", translated, ok)
		}
	})
}

// ---------------------------------------------------------------------------
// Server SQLSTATE conditions (synthetic, full evidence bar).
// ---------------------------------------------------------------------------

type rebConditionCase struct {
	name        string
	sqlState    string
	severity    string
	wantCond    DriverCondition
	wantCode    string
	wantRetry   bool
	wantHTTP    int
	predicateOn func(error) bool
}

func TestTranslatePostgresErrorServerConditions(t *testing.T) {
	tests := []rebConditionCase{
		{
			name: "query_canceled_57014", sqlState: "57014", severity: "ERROR",
			wantCond: ConditionQueryCanceled, wantCode: string(dataframeerrors.CodeQueryResourceLimitExceeded),
			wantRetry: false, wantHTTP: 503,
		},
		{
			name: "serialization_40001", sqlState: "40001", severity: "ERROR",
			wantCond: ConditionSerializationFailure, wantCode: string(dataframeerrors.CodeInternalError),
			wantRetry: false, wantHTTP: 500,
			predicateOn: RetryableTransactionConflict,
		},
		{
			name: "deadlock_40P01", sqlState: "40P01", severity: "ERROR",
			wantCond: ConditionDeadlockDetected, wantCode: string(dataframeerrors.CodeInternalError),
			wantRetry: false, wantHTTP: 500,
			predicateOn: RetryableTransactionConflict,
		},
		{
			name: "unique_23505", sqlState: "23505", severity: "ERROR",
			wantCond: ConditionUniqueViolation, wantCode: string(dataframeerrors.CodeInternalError),
			wantRetry: false, wantHTTP: 500,
			predicateOn: IsUniqueViolation,
		},
		{
			name: "aborted_25P02", sqlState: "25P02", severity: "ERROR",
			wantCond: ConditionAbortedTransaction, wantCode: string(dataframeerrors.CodeInternalError),
			wantRetry: false, wantHTTP: 500,
			predicateOn: IsAbortedTransaction,
		},
		{
			name: "admin_shutdown_57P01", sqlState: "57P01", severity: "FATAL",
			wantCond: ConditionAdminShutdown, wantCode: string(dataframeerrors.CodeBackendUnavailable),
			wantRetry: true, wantHTTP: 503,
		},
		{
			name: "crash_shutdown_57P02", sqlState: "57P02", severity: "FATAL",
			wantCond: ConditionCrashShutdown, wantCode: string(dataframeerrors.CodeBackendUnavailable),
			wantRetry: true, wantHTTP: 503,
		},
		{
			name: "cannot_connect_now_57P03", sqlState: "57P03", severity: "FATAL",
			wantCond: ConditionCannotConnectNow, wantCode: string(dataframeerrors.CodeBackendUnavailable),
			wantRetry: true, wantHTTP: 503,
		},
		{
			name: "connection_failure_08006", sqlState: "08006", severity: "FATAL",
			wantCond: ConditionConnectionLost, wantCode: string(dataframeerrors.CodeBackendUnavailable),
			wantRetry: true, wantHTTP: 503,
		},
		{
			name: "idle_in_tx_25P03", sqlState: "25P03", severity: "FATAL",
			wantCond: ConditionSessionTerminated, wantCode: string(dataframeerrors.CodeBackendUnavailable),
			wantRetry: true, wantHTTP: 503,
		},
		{
			name: "unclassified_42601", sqlState: "42601", severity: "ERROR",
			wantCond: ConditionServerRejected, wantCode: string(dataframeerrors.CodeInternalError),
			wantRetry: false, wantHTTP: 500,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			raw := rebSecretPgError(test.severity, test.sqlState)
			wrapped := fmt.Errorf("adapter seam: %w", raw)
			translated, ok := TranslateError(wrapped)
			if !ok {
				t.Fatal("server error not translated")
			}
			translatedErr, isType := translated.(*TranslatedError)
			if !isType {
				t.Fatalf("translated type = %T, want *TranslatedError", translated)
			}
			if translatedErr.Condition != test.wantCond {
				t.Fatalf("condition = %s, want %s", translatedErr.Condition, test.wantCond)
			}
			if translatedErr.SQLState != test.sqlState {
				t.Fatalf("sqlstate = %s, want %s", translatedErr.SQLState, test.sqlState)
			}
			if translatedErr.AmbiguousCommit {
				t.Fatal("query-path translation must not flag commit ambiguity")
			}
			if translatedErr.Cause() != wrapped {
				t.Fatal("raw cause must stay available to server-side logging via Cause()")
			}
			userErr := rebUserErr(t, translated)
			if userErr.Code() != test.wantCode || userErr.Retryable() != test.wantRetry {
				t.Fatalf("typed = %s retryable=%v, want %s retryable=%v", userErr.Code(), userErr.Retryable(), test.wantCode, test.wantRetry)
			}
			body, mapped := rebHttpJSON(t, translated)
			if mapped.Status != test.wantHTTP {
				t.Fatalf("HTTP status = %d, want %d (%s)", mapped.Status, test.wantHTTP, body)
			}
			if mapped.Body.Error.Retryable != test.wantRetry {
				t.Fatalf("HTTP retryable = %v, want %v (no blanket retry)", mapped.Body.Error.Retryable, test.wantRetry)
			}
			if mapped.Body.Error.Details["backend"] != "postgres" {
				t.Fatalf("details lack the postgres backend tag: %#v", mapped.Body.Error.Details)
			}
			if mapped.Body.Error.Details["condition"] != string(test.wantCond) {
				t.Fatalf("details condition = %#v, want %s", mapped.Body.Error.Details["condition"], test.wantCond)
			}
			if translatedErr.SQLState != "" && mapped.Body.Error.Details["sqlstate"] != test.sqlState {
				t.Fatalf("details sqlstate = %#v, want %s", mapped.Body.Error.Details["sqlstate"], test.sqlState)
			}
			if test.predicateOn != nil && !test.predicateOn(translated) {
				t.Fatalf("dedicated predicate rejected its own condition")
			}
			rebAssertSafeRendering(t, translated)

			// The raw (untranslated) form must also render safely at the
			// transport: MapDataframeError redacts unknown causes.
			rawBody, rawMapped := rebHttpJSON(t, wrapped)
			if rawMapped.Body.Error.Code != "INTERNAL_ERROR" {
				t.Fatalf("raw untranslated mapped = %s, want INTERNAL_ERROR redaction", rawMapped.Body.Error.Code)
			}
			for _, forbidden := range rebForbidden {
				if strings.Contains(rawBody, forbidden) {
					t.Fatalf("raw redaction leaked %q in %s", forbidden, rawBody)
				}
			}
		})
	}
}

func TestTranslatePostgresErrorUniqueConflictIsTargeted(t *testing.T) {
	// Acceptance: unique conflict becomes a domain conflict only for a
	// targeted CAS/insert path, never for every unique violation.
	raw := rebSecretPgError("ERROR", sqlStateUniqueViolation)

	translated, ok := TranslateError(raw)
	if !ok {
		t.Fatal("unique violation not translated")
	}
	if _, isConflict := translated.(*UniqueConflictError); isConflict {
		t.Fatal("generic translation must not produce a domain conflict")
	}
	userErr := rebUserErr(t, translated)
	if strings.Contains(userErr.Code(), "CONFLICT") {
		t.Fatalf("generic translation must not advertise a conflict code, got %s", userErr.Code())
	}

	// A targeted CAS/insert caller that proved the statement's meaning gets
	// the domain-conflict form.
	conflict, ok := AsUniqueConflict(fmt.Errorf("cas insert: %w", raw))
	if !ok || conflict == nil {
		t.Fatal("targeted CAS path must obtain the unique conflict")
	}
	if !errors.Is(conflict, ErrUniqueConflict) {
		t.Fatal("conflict must match ErrUniqueConflict")
	}
	rebAssertSafeRendering(t, conflict)

	// Non-vacuity: near misses are refused.
	if _, ok := AsUniqueConflict(rebSecretPgError("ERROR", sqlStateDeadlockDetected)); ok {
		t.Fatal("AsUniqueConflict must refuse a deadlock")
	}
	if _, ok := AsUniqueConflict(errors.New("foreign reb-not-postgres")); ok {
		t.Fatal("AsUniqueConflict must refuse foreign errors")
	}
}

// ---------------------------------------------------------------------------
// Commit classification: ambiguous outcomes stay ambiguous.
// ---------------------------------------------------------------------------

type rebSafeToRetryError struct{ error }

func (rebSafeToRetryError) SafeToRetry() bool { return true }

// TestTranslatePostgresErrorLocalizedSeverityKeepsCommitAmbiguous pins the
// ambiguous-COMMIT guarantee against localized server sessions: PostgreSQL
// translates the protocol Severity field (S) under non-English lc_messages
// while SeverityUnlocalized (V) stays English, so classification must read
// V. A localized "SCHWERWIEGEND" (FATAL) at COMMIT must still be ambiguous
// and never retryable, never falling through to the retryable admin-shutdown
// advertisement.
func TestTranslatePostgresErrorLocalizedSeverityKeepsCommitAmbiguous(t *testing.T) {
	tests := []struct {
		name          string
		severity      string
		unlocalized   string
		sqlState      string
		wantCondition DriverCondition
	}{
		{"german_fatal", "SCHWERWIEGEND", "FATAL", sqlStateAdminShutdown, ConditionAdminShutdown},
		{"plain_fatal", "FATAL", "FATAL", sqlStateAdminShutdown, ConditionAdminShutdown},
		{"localized_severity_with_panic_v", "PANIK", "PANIC", sqlStateAdminShutdown, ConditionAdminShutdown},
		{"unlocalized_missing_falls_back", "SCHWERWIEGEND", "", sqlStateAdminShutdown, ConditionAdminShutdown},
		// On a non-session-end SQLSTATE only the unlocalized field can
		// recognize FATAL: the old localized-only comparison would miss it.
		{"fatal_v_on_internal_error_state", "SCHWERWIEGEND", "FATAL", "XX001", ConditionServerRejected},
		{"english_severity_fallback", "FATAL", "", "XX001", ConditionServerRejected},
		// Session-death SQLSTATE classes stay uncertain even when BOTH
		// severity fields read non-fatal: these messages terminate the
		// session by protocol definition, so the COMMIT outcome is unknown
		// whatever the severity strings claim. Without this short-circuit a
		// pure severity check would fall into translateSQLState and return
		// a retryable advertisement for an unconfirmed commit.
		{"session_end_57P01_nonfatal_severity", "SCHWERWIEGEND", "ERROR", sqlStateAdminShutdown, ConditionAdminShutdown},
		{"session_end_08006_nonfatal_severity", "FEHLER", "ERROR", "08006", ConditionConnectionLost},
		{"session_end_25P03_nonfatal_severity", "FEHLER", "ERROR", sqlStateIdleInTxTerminated18, ConditionSessionTerminated},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			pgErr := rebSecretPgError(test.severity, test.sqlState)
			pgErr.SeverityUnlocalized = test.unlocalized
			translated, ok := TranslateCommitError(fmt.Errorf("commit: %w", pgErr))
			if !ok {
				t.Fatal("localized commit failure not translated")
			}
			translatedErr := translated.(*TranslatedError)
			if !translatedErr.AmbiguousCommit || !errors.Is(translated, ErrCommitOutcomeUnknown) {
				t.Fatalf("localized severity defeated the ambiguous-COMMIT guarantee: %#v", translatedErr)
			}
			if translatedErr.Condition != test.wantCondition {
				t.Fatalf("condition = %s, want %s", translatedErr.Condition, test.wantCondition)
			}
			if userErr := rebUserErr(t, translated); userErr.Retryable() {
				t.Fatalf("localized FATAL severity produced a retryable advertisement for an unconfirmed commit: %#v", userErr)
			}
			_, mapped := rebHttpJSON(t, translated)
			if mapped.Body.Error.Retryable {
				t.Fatal("HTTP must not advertise retry for a localized FATAL mid-commit failure")
			}
			rebAssertSafeRendering(t, translated)
		})
	}
	// Non-vacuity: an unlocalized ERROR severity must NOT be ambiguous —
	// the server answered the commit.
	pgErr := rebSecretPgError("FEHLER", sqlStateUniqueViolation)
	pgErr.SeverityUnlocalized = "ERROR"
	translated, ok := TranslateCommitError(fmt.Errorf("commit: %w", pgErr))
	if !ok {
		t.Fatal("localized commit ERROR not translated")
	}
	if translatedErr := translated.(*TranslatedError); translatedErr.AmbiguousCommit || errors.Is(translated, ErrCommitOutcomeUnknown) {
		t.Fatalf("localized ERROR severity wrongly flagged ambiguous: %#v", translatedErr)
	}
}

func TestTranslatePostgresErrorCommitClassification(t *testing.T) {
	t.Run("fatal mid-commit is ambiguous", func(t *testing.T) {
		raw := fmt.Errorf("commit: %w", rebSecretPgError("FATAL", sqlStateAdminShutdown))
		translated, ok := TranslateCommitError(raw)
		if !ok {
			t.Fatal("commit FATAL not translated")
		}
		translatedErr := translated.(*TranslatedError)
		if !translatedErr.AmbiguousCommit {
			t.Fatal("FATAL at commit must flag the outcome as ambiguous")
		}
		if !errors.Is(translated, ErrCommitOutcomeUnknown) {
			t.Fatal("ambiguous commit must match ErrCommitOutcomeUnknown")
		}
		if translatedErr.Condition != ConditionAdminShutdown || translatedErr.SQLState != sqlStateAdminShutdown {
			t.Fatalf("condition = %s/%s, want admin_shutdown/57P01", translatedErr.Condition, translatedErr.SQLState)
		}
		userErr := rebUserErr(t, translated)
		if userErr.Retryable() {
			t.Fatal("an ambiguous commit must never be retryable")
		}
		_, mapped := rebHttpJSON(t, translated)
		if mapped.Body.Error.Retryable || mapped.Status != 500 {
			t.Fatalf("ambiguous commit mapped = %#v, want non-retryable 500", mapped)
		}
		// Never a rollback claim: the rendered contract says nothing about
		// whether the write landed.
		for _, forbidden := range []string{"rolled back", "rollback confirmed", "not written", "no write"} {
			if strings.Contains(strings.ToLower(translated.Error()), forbidden) {
				t.Fatalf("ambiguous commit rendering claims %q: %s", forbidden, translated.Error())
			}
		}
		rebAssertSafeRendering(t, translated)
	})

	t.Run("server-answered commit error is definitive", func(t *testing.T) {
		// A non-FATAL error answered the COMMIT: the server rejected it.
		raw := fmt.Errorf("commit: %w", rebSecretPgError("ERROR", sqlStateUniqueViolation))
		translated, ok := TranslateCommitError(raw)
		if !ok {
			t.Fatal("commit ERROR not translated")
		}
		translatedErr := translated.(*TranslatedError)
		if translatedErr.AmbiguousCommit {
			t.Fatal("a server-answered commit must not be flagged ambiguous")
		}
		if errors.Is(translated, ErrCommitOutcomeUnknown) {
			t.Fatal("a server-answered commit must not claim unknown outcome")
		}
		if translatedErr.Condition != ConditionUniqueViolation {
			t.Fatalf("condition = %s, want unique_violation", translatedErr.Condition)
		}
		rebAssertSafeRendering(t, translated)
	})

	t.Run("driver-confirmed rollback is definitive", func(t *testing.T) {
		translated, ok := TranslateCommitError(fmt.Errorf("commit: %w", pgx.ErrTxCommitRollback))
		if !ok {
			t.Fatal("ErrTxCommitRollback not translated")
		}
		translatedErr := translated.(*TranslatedError)
		if translatedErr.Condition != ConditionCommitRejected || translatedErr.AmbiguousCommit {
			t.Fatalf("commit rollback = %#v, want definitive commit_rejected", translatedErr)
		}
		rebAssertSafeRendering(t, translated)
	})

	t.Run("tx closed is definitive", func(t *testing.T) {
		translated, ok := TranslateCommitError(fmt.Errorf("commit: %w", pgx.ErrTxClosed))
		if !ok {
			t.Fatal("ErrTxClosed not translated")
		}
		if translatedErr := translated.(*TranslatedError); translatedErr.Condition != ConditionTransactionClosed {
			t.Fatalf("condition = %s, want transaction_closed", translatedErr.Condition)
		}
		rebAssertSafeRendering(t, translated)
	})

	t.Run("pre-send transport failure is definitive not-sent", func(t *testing.T) {
		raw := &ConnectError{Op: "acquire", Err: rebSafeToRetryError{errors.New("rebhost:5432 refused")}}
		translated, ok := TranslateCommitError(raw)
		if !ok {
			t.Fatal("pre-send connect failure not translated")
		}
		translatedErr := translated.(*TranslatedError)
		if translatedErr.AmbiguousCommit {
			t.Fatal("a failure the driver guarantees occurred before send cannot be ambiguous")
		}
		userErr := rebUserErr(t, translated)
		if userErr.Code() != string(dataframeerrors.CodeBackendUnavailable) || !userErr.Retryable() {
			t.Fatalf("pre-send failure = %s retryable=%v, want retryable BACKEND_UNAVAILABLE", userErr.Code(), userErr.Retryable())
		}
		rebAssertSafeRendering(t, translated)
	})

	t.Run("post-send transport failure is ambiguous", func(t *testing.T) {
		raw := fmt.Errorf("commit flush: %w", pgconn.ErrConnClosed)
		translated, ok := TranslateCommitError(raw)
		if !ok {
			t.Fatal("post-send conn closed not translated")
		}
		translatedErr := translated.(*TranslatedError)
		if !translatedErr.AmbiguousCommit || !errors.Is(translated, ErrCommitOutcomeUnknown) {
			t.Fatalf("post-send failure = %#v, want ambiguous with ErrCommitOutcomeUnknown", translatedErr)
		}
		if translatedErr.Condition != ConditionConnectionLost {
			t.Fatalf("condition = %s, want connection_lost", translatedErr.Condition)
		}
		_, mapped := rebHttpJSON(t, translated)
		if mapped.Body.Error.Retryable {
			t.Fatal("ambiguous commit must never advertise retry")
		}
		rebAssertSafeRendering(t, translated)
	})

	t.Run("canceled commit is ambiguous not client-canceled", func(t *testing.T) {
		raw := &ConnectError{Op: "query", Err: fmt.Errorf("flush: %w", context.Canceled)}
		translated, ok := TranslateCommitError(raw)
		if !ok {
			t.Fatal("canceled commit not translated")
		}
		translatedErr := translated.(*TranslatedError)
		if !translatedErr.AmbiguousCommit {
			t.Fatal("a commit canceled mid-flight has an unknown outcome")
		}
		if translatedErr.Condition == ConditionRequestCanceled {
			t.Fatal("commit phase must not present caller-cancellation semantics over the unknown write")
		}
		if !errors.Is(translated, context.Canceled) {
			t.Fatal("the caller's cancellation must stay Is-visible")
		}
		rebAssertSafeRendering(t, translated)
	})
}

// ---------------------------------------------------------------------------
// Races: concurrent failures sharing the same raw errors must not share
// mutable state.
// ---------------------------------------------------------------------------

func TestTranslatePostgresErrorConcurrentTranslationSharesNoState(t *testing.T) {
	sharedRaw := []error{
		rebSecretPgError("ERROR", sqlStateDeadlockDetected),
		rebSecretPgError("FATAL", sqlStateAdminShutdown),
		&ConnectError{Op: "acquire", Err: context.DeadlineExceeded},
		fmt.Errorf("stream: %w", pgconn.ErrConnClosed),
	}
	want := make([]string, len(sharedRaw))
	for i, raw := range sharedRaw {
		translated, ok := TranslateError(raw)
		if !ok {
			t.Fatalf("baseline translation %d refused", i)
		}
		want[i] = fmt.Sprintf("%s|%s", translated, translated.(*TranslatedError).Condition)
	}

	const goroutines = 8
	var wg sync.WaitGroup
	results := make([][]string, goroutines)
	for worker := 0; worker < goroutines; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			rounds := make([]string, len(sharedRaw))
			for round := 0; round < 25; round++ {
				for i, raw := range sharedRaw {
					translated, ok := TranslateError(raw)
					if !ok {
						results[worker] = []string{fmt.Sprintf("worker %d refused input %d", worker, i)}
						return
					}
					// Concurrent predicate consultation over the same
					// shared error must also be race-free.
					_ = RetryableTransactionConflict(translated)
					_ = IsUniqueViolation(translated)
					_ = httpapi.MapDataframeError(translated, "race")
					rounds[i] = fmt.Sprintf("%s|%s", translated, translated.(*TranslatedError).Condition)
				}
			}
			results[worker] = rounds
		}(worker)
	}
	wg.Wait()
	for worker, rounds := range results {
		if len(rounds) != len(want) {
			t.Fatalf("worker %d: %v", worker, rounds)
		}
		for i := range want {
			if rounds[i] != want[i] {
				t.Fatalf("worker %d input %d: %s, want %s (shared-state interference)", worker, i, rounds[i], want[i])
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Real PostgreSQL 18 acceptance (LOOM_TEST_POSTGRES_URL).
// ---------------------------------------------------------------------------

const (
	rebFixturePrefix  = "verify_reb_"
	rebFixtureNameFmt = rebFixturePrefix + "%d_%s"
)

var rebFixturePattern = regexp.MustCompile(`^verify_reb_[0-9]+_[0-9a-f]+$`)

// Ownership registry: databases this file verified into existence, audited
// by exact name plus an own-prefix baseline count (never a global sweep).
var (
	rebOwnedMu     sync.Mutex
	rebOwnedByName = map[string]string{} // database -> owned schema
)

func rebRegister(db, schema string) {
	rebOwnedMu.Lock()
	defer rebOwnedMu.Unlock()
	rebOwnedByName[db] = schema
}

var (
	rebBaselineOnce  sync.Once
	rebBaselineCount int64
	rebBaselineErr   error
)

func rebCaptureBaseline(raw string) {
	rebBaselineOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		admin, err := pgxpool.New(ctx, raw)
		if err != nil {
			rebBaselineErr = fmt.Errorf("connect for baseline: %w", err)
			return
		}
		defer admin.Close()
		var count int64
		if err := admin.QueryRow(ctx,
			"SELECT count(*) FROM pg_database WHERE datname LIKE 'verify\\_reb\\_%' ESCAPE '\\'").
			Scan(&count); err != nil {
			rebBaselineErr = fmt.Errorf("count baseline: %w", err)
			return
		}
		rebBaselineCount = count
	})
}

func rebURL(t *testing.T) string {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv("LOOM_TEST_POSTGRES_URL"))
	if raw == "" {
		t.Skip("set LOOM_TEST_POSTGRES_URL to run the PostgreSQL 18 error-boundary acceptance tests")
	}
	return raw
}

type rebService struct {
	ctx    context.Context
	admin  *pgxpool.Pool // service database
	iso    *pgxpool.Pool // isolated database (witness reads)
	schema string
	dsn    string
	scrub  func(any) string
	fail   func(step string, err error)
	// canaries this test scattered into the isolated database; every
	// rendered error surface must stay free of them.
	canaries []string
}

func newRebService(t *testing.T) *rebService {
	t.Helper()
	raw := rebURL(t)
	rebCaptureBaseline(raw)
	if rebBaselineErr != nil {
		t.Fatalf("capture pg_database baseline: %v", rebBaselineErr)
	}

	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		t.Fatal("LOOM_TEST_POSTGRES_URL must be a postgres:// or postgresql:// URL")
	}

	var secrets []string
	secrets = append(secrets, raw)
	if u.User != nil {
		secrets = append(secrets, u.User.String())
		if pw, ok := u.User.Password(); ok && pw != "" {
			secrets = append(secrets, pw)
		}
	}
	scrub := func(v any) string {
		s := fmt.Sprintf("%v", v)
		for _, secret := range secrets {
			if secret != "" {
				s = strings.ReplaceAll(s, secret, "<redacted>")
			}
		}
		return s
	}
	fail := func(step string, err error) {
		t.Fatalf("%s: %s", step, scrub(err))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
	t.Cleanup(cancel)

	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		fail("connect to test service", err)
	}
	t.Cleanup(admin.Close)

	// PostgreSQL 18 only: refuse any other server rather than claim parity.
	var versionNum int64
	var version string
	if err := admin.QueryRow(ctx,
		"SELECT current_setting('server_version_num')::int8, current_setting('server_version')").
		Scan(&versionNum, &version); err != nil {
		fail("read server version", err)
	}
	if err := CheckServerVersion(versionNum, version); err != nil {
		t.Fatalf("test service is not the required backend: %v", err)
	}

	dbName := fmt.Sprintf(rebFixtureNameFmt, time.Now().UnixNano(), rebRandomHex(t))
	if !rebFixturePattern.MatchString(dbName) {
		t.Fatalf("isolated database name %q does not match the required pattern", dbName)
	}
	if _, err := admin.Exec(ctx, `CREATE DATABASE "`+dbName+`"`); err != nil {
		fail("create isolated database", err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(context.WithoutCancel(ctx),
			`DROP DATABASE IF EXISTS "`+dbName+`" WITH (FORCE)`); err != nil {
			t.Errorf("drop isolated database: %s", scrub(err))
		}
	})

	if q := u.Query(); q.Get("dbname") != "" {
		q.Del("dbname")
		u.RawQuery = q.Encode()
	}
	u.Path = "/" + dbName
	dsn := u.String()
	secrets = append(secrets, dsn)

	iso, err := pgxpool.New(ctx, dsn)
	if err != nil {
		fail("connect to isolated database", err)
	}
	t.Cleanup(iso.Close)

	schema := fmt.Sprintf(rebFixtureNameFmt, time.Now().UnixNano(), rebRandomHex(t))
	if !rebFixturePattern.MatchString(schema) {
		t.Fatalf("owned schema name %q does not match the required pattern", schema)
	}
	if _, err := iso.Exec(ctx, `CREATE SCHEMA "`+schema+`"`); err != nil {
		fail("create owned schema", err)
	}
	t.Cleanup(func() {
		if _, err := iso.Exec(context.WithoutCancel(ctx),
			`DROP SCHEMA IF EXISTS "`+schema+`" CASCADE`); err != nil {
			t.Errorf("drop owned schema: %s", scrub(err))
		}
	})

	rebRegister(dbName, schema)
	return &rebService{ctx: ctx, admin: admin, iso: iso, schema: schema, dsn: dsn, scrub: scrub, fail: fail}
}

func rebRandomHex(t *testing.T) string {
	t.Helper()
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("generate isolation suffix: %v", err)
	}
	return hex.EncodeToString(b[:])
}

// rebQualified quotes the owned schema and appends an already-quoted table.
func (s *rebService) rebQualified(table string) string {
	return strconv.Quote(s.schema) + "." + table
}

// rebScrubbedRendering asserts every rendered surface of err stays free of
// this fixture's canaries and the connection-string secrets.
func (s *rebService) rebScrubbedRendering(t *testing.T, where string, err error) {
	t.Helper()
	forbidden := append([]string{}, s.canaries...)
	forbidden = append(forbidden, s.dsn, "postgres://", "postgresql://")
	if u, uerr := url.Parse(s.dsn); uerr == nil && u.User != nil {
		forbidden = append(forbidden, u.User.String())
		if pw, ok := u.User.Password(); ok && pw != "" {
			forbidden = append(forbidden, pw)
		}
	}
	renderings := map[string]string{
		"Error()": err.Error(),
		"%v":      fmt.Sprintf("%v", err),
	}
	body, _ := rebHttpJSON(t, err)
	renderings["httpJSON"] = body
	if userErr, ok := dataframeerrors.AsUserError(err); ok {
		details, _ := json.Marshal(userErr.Details())
		renderings["details"] = string(details)
	}
	for surface, text := range renderings {
		for _, token := range forbidden {
			if token != "" && strings.Contains(text, token) {
				// Never print the offending surface raw: it may carry the
				// connection string. Report the surface name only.
				t.Fatalf("%s: %s leaks a forbidden canary token (%d chars); rendered=%s",
					where, surface, len(token), strings.ReplaceAll(s.scrub(text), token, "<canary>"))
			}
		}
	}
}

// rebWitnessSQLState extracts the raw SQLSTATE a server failure witnessed
// before translation (non-vacuity: the condition really fired).
func rebWitnessSQLState(t *testing.T, err error) string {
	t.Helper()
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("failure is not a server SQLSTATE error: %v", err)
	}
	return pgErr.Code
}

func (s *rebService) client(t *testing.T, maxConns int32) *Client {
	t.Helper()
	client, err := Open(s.ctx, Config{
		DSN:            s.dsn,
		MaxConns:       maxConns,
		MinConns:       0,
		AcquireTimeout: 30 * time.Second,
	})
	if err != nil {
		s.fail("open store client", err)
	}
	t.Cleanup(client.Close)
	return client
}

func (s *rebService) acquire(t *testing.T, client *Client) *pgxpool.Conn {
	t.Helper()
	conn, err := client.Acquire(s.ctx)
	if err != nil {
		s.fail("acquire", err)
	}
	t.Cleanup(conn.Release)
	return conn
}

// TestTranslatePostgresErrorRealServerConditions drives every admitted
// error condition against the live PostgreSQL 18 server, witnesses the raw
// SQLSTATE, translates it, and asserts the typed contract plus rendered
// redaction.
func TestTranslatePostgresErrorRealServerConditions(t *testing.T) {
	t.Run("query_canceled_57014", func(t *testing.T) {
		s := newRebService(t)
		s.canaries = []string{s.schema}
		client := s.client(t, 1)
		conn := s.acquire(t, client)
		if _, err := conn.Exec(s.ctx, "SET statement_timeout = '80ms'"); err != nil {
			s.fail("set statement_timeout", err)
		}
		_, err := conn.Exec(s.ctx, "SELECT pg_sleep(3)")
		if err == nil {
			t.Fatal("statement_timeout did not fire: non-vacuity control broken")
		}
		if code := rebWitnessSQLState(t, err); code != sqlStateQueryCanceled {
			t.Fatalf("witnessed SQLSTATE %s, want 57014", code)
		}
		translated, ok := TranslateError(err)
		if !ok {
			t.Fatal("57014 not translated")
		}
		translatedErr := translated.(*TranslatedError)
		if translatedErr.Condition != ConditionQueryCanceled {
			t.Fatalf("condition = %s, want query_canceled", translatedErr.Condition)
		}
		userErr := rebUserErr(t, translated)
		if userErr.Code() != string(dataframeerrors.CodeQueryResourceLimitExceeded) || userErr.Retryable() {
			t.Fatalf("typed = %s retryable=%v, want non-retryable QUERY_RESOURCE_LIMIT_EXCEEDED", userErr.Code(), userErr.Retryable())
		}
		s.rebScrubbedRendering(t, "57014", translated)
		// Control: the same session still executes fast statements, so the
		// translation is not a dropped connection in disguise.
		var one int
		if err := conn.QueryRow(s.ctx, "SELECT 1").Scan(&one); err != nil || one != 1 {
			t.Fatalf("post-cancel control failed: %v", err)
		}
	})

	t.Run("serialization_failure_40001", func(t *testing.T) {
		s := newRebService(t)
		table := `"reb_ssi_canary_table"`
		s.canaries = []string{s.schema, "reb_ssi_canary_table"}
		if _, err := s.iso.Exec(s.ctx, "CREATE TABLE "+s.rebQualified(table)+" (id int)"); err != nil {
			s.fail("create ssi table", err)
		}
		if _, err := s.iso.Exec(s.ctx,
			"INSERT INTO "+s.rebQualified(table)+" SELECT generate_series(1,100)"); err != nil {
			s.fail("seed ssi table", err)
		}
		tx1, err := s.iso.BeginTx(s.ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
		if err != nil {
			s.fail("begin tx1", err)
		}
		tx2, err := s.iso.BeginTx(s.ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
		if err != nil {
			s.fail("begin tx2", err)
		}
		var sum int
		if err := tx1.QueryRow(s.ctx, "SELECT sum(id) FROM "+s.rebQualified(table)+" WHERE id <= 50").Scan(&sum); err != nil {
			s.fail("tx1 read", err)
		}
		if err := tx2.QueryRow(s.ctx, "SELECT sum(id) FROM "+s.rebQualified(table)+" WHERE id > 50").Scan(&sum); err != nil {
			s.fail("tx2 read", err)
		}
		if _, err := tx1.Exec(s.ctx, "UPDATE "+s.rebQualified(table)+" SET id = id + 100 WHERE id > 50"); err != nil {
			s.fail("tx1 write", err)
		}
		if _, err := tx2.Exec(s.ctx, "UPDATE "+s.rebQualified(table)+" SET id = id - 100 WHERE id <= 50"); err != nil {
			s.fail("tx2 write", err)
		}
		e1 := tx1.Commit(s.ctx)
		e2 := tx2.Commit(s.ctx)
		victims := []error{}
		for _, e := range []error{e1, e2} {
			if e == nil {
				continue
			}
			if code := rebWitnessSQLState(t, e); code != sqlStateSerializationFailure {
				t.Fatalf("SSI loser failed with SQLSTATE %s, want 40001", code)
			}
			victims = append(victims, e)
		}
		if len(victims) != 1 {
			t.Fatalf("expected exactly one SSI loser, got %d (errors: %v / %v)", len(victims), s.scrub(e1), s.scrub(e2))
		}
		loser := victims[0]
		translated, ok := TranslateError(loser)
		if !ok {
			t.Fatal("40001 not translated")
		}
		translatedErr := translated.(*TranslatedError)
		if translatedErr.Condition != ConditionSerializationFailure {
			t.Fatalf("condition = %s, want serialization_failure", translatedErr.Condition)
		}
		userErr := rebUserErr(t, translated)
		if userErr.Retryable() {
			t.Fatal("serialization failure must not be blanket-retryable")
		}
		if !RetryableTransactionConflict(translated) {
			t.Fatal("operation policy must still recognize the retryable conflict")
		}
		_, mapped := rebHttpJSON(t, translated)
		if mapped.Body.Error.Retryable {
			t.Fatal("HTTP must not advertise retry for 40001")
		}
		s.rebScrubbedRendering(t, "40001", translated)

		// Witness: exactly one transaction's update landed — the loser's
		// writes really vanished and the winner's really persisted.
		var shifted int
		if err := s.iso.QueryRow(s.ctx,
			"SELECT count(*) FROM "+s.rebQualified(table)+" WHERE id > 100").Scan(&shifted); err != nil {
			s.fail("witness ssi", err)
		}
		winnerLow := e1 == nil
		if (shifted == 50) != winnerLow {
			t.Fatalf("witness shifted=%d, winner-low=%v: concurrent outcome not witnessed", shifted, winnerLow)
		}
	})

	t.Run("deadlock_40P01", func(t *testing.T) {
		s := newRebService(t)
		table := `"reb_deadlock_canary_table"`
		s.canaries = []string{s.schema, "reb_deadlock_canary_table"}
		if _, err := s.iso.Exec(s.ctx,
			"CREATE TABLE "+s.rebQualified(table)+" (id int PRIMARY KEY, v int)"); err != nil {
			s.fail("create deadlock table", err)
		}
		if _, err := s.iso.Exec(s.ctx,
			"INSERT INTO "+s.rebQualified(table)+" VALUES (1,0),(2,0)"); err != nil {
			s.fail("seed deadlock table", err)
		}
		tx1, err := s.iso.BeginTx(s.ctx, pgx.TxOptions{})
		if err != nil {
			s.fail("begin tx1", err)
		}
		tx2, err := s.iso.BeginTx(s.ctx, pgx.TxOptions{})
		if err != nil {
			s.fail("begin tx2", err)
		}
		if _, err := tx1.Exec(s.ctx, "UPDATE "+s.rebQualified(table)+" SET v = v + 1 WHERE id = 1"); err != nil {
			s.fail("tx1 first lock", err)
		}
		if _, err := tx2.Exec(s.ctx, "UPDATE "+s.rebQualified(table)+" SET v = v + 1 WHERE id = 2"); err != nil {
			s.fail("tx2 first lock", err)
		}
		blocked := make(chan error, 1)
		go func() {
			_, err := tx1.Exec(s.ctx, "UPDATE "+s.rebQualified(table)+" SET v = v + 1 WHERE id = 2")
			blocked <- err
		}()
		time.Sleep(400 * time.Millisecond)
		_, e2 := tx2.Exec(s.ctx, "UPDATE "+s.rebQualified(table)+" SET v = v + 1 WHERE id = 1")
		e1 := <-blocked
		var loser error
		for _, e := range []error{e1, e2} {
			if e == nil {
				continue
			}
			if code := rebWitnessSQLState(t, e); code != sqlStateDeadlockDetected {
				t.Fatalf("deadlock loser failed with SQLSTATE %s, want 40P01", code)
			}
			loser = e
		}
		if loser == nil {
			t.Fatal("no deadlock victim: non-vacuity control broken")
		}
		translated, ok := TranslateError(loser)
		if !ok {
			t.Fatal("40P01 not translated")
		}
		translatedErr := translated.(*TranslatedError)
		if translatedErr.Condition != ConditionDeadlockDetected {
			t.Fatalf("condition = %s, want deadlock_detected", translatedErr.Condition)
		}
		if rebUserErr(t, translated).Retryable() {
			t.Fatal("deadlock must not be blanket-retryable")
		}
		if !RetryableTransactionConflict(translated) {
			t.Fatal("policy predicate must recognize the deadlock")
		}
		s.rebScrubbedRendering(t, "40P01", translated)

		// Convergence witness: the surviving transaction commits and its
		// two increments are visible; the victim wrote nothing.
		var winner, loserTx pgx.Tx
		if e1 == nil {
			winner, loserTx = tx1, tx2
		} else {
			winner, loserTx = tx2, tx1
		}
		if err := winner.Commit(s.ctx); err != nil {
			s.fail("winner commit", err)
		}
		_ = loserTx.Rollback(s.ctx)
		var row1, row2 int
		if err := s.iso.QueryRow(s.ctx,
			"SELECT v FROM "+s.rebQualified(table)+" WHERE id = 1").Scan(&row1); err != nil {
			s.fail("witness row1", err)
		}
		if err := s.iso.QueryRow(s.ctx,
			"SELECT v FROM "+s.rebQualified(table)+" WHERE id = 2").Scan(&row2); err != nil {
			s.fail("witness row2", err)
		}
		if row1 != 1 || row2 != 1 {
			t.Fatalf("witness (row1,row2)=(%d,%d), want (1,1): winner +1/+1, victim 0/0", row1, row2)
		}
	})

	t.Run("unique_violation_23505", func(t *testing.T) {
		s := newRebService(t)
		table := `"reb_unique_canary_table"`
		constraint := "reb_unique_canary_ux"
		s.canaries = []string{s.schema, "reb_unique_canary_table", constraint, "reb_dup_key_canary"}
		if _, err := s.iso.Exec(s.ctx, "CREATE TABLE "+s.rebQualified(table)+
			" (id text CONSTRAINT "+constraint+" UNIQUE)"); err != nil {
			s.fail("create unique table", err)
		}
		if _, err := s.iso.Exec(s.ctx,
			"INSERT INTO "+s.rebQualified(table)+" VALUES ('reb_dup_key_canary')"); err != nil {
			s.fail("seed unique table", err)
		}
		_, err := s.iso.Exec(s.ctx, "INSERT INTO "+s.rebQualified(table)+" VALUES ('reb_dup_key_canary')")
		if err == nil {
			t.Fatal("duplicate key accepted: non-vacuity control broken")
		}
		if code := rebWitnessSQLState(t, err); code != sqlStateUniqueViolation {
			t.Fatalf("witnessed SQLSTATE %s, want 23505", code)
		}
		translated, ok := TranslateError(err)
		if !ok {
			t.Fatal("23505 not translated")
		}
		translatedErr := translated.(*TranslatedError)
		if translatedErr.Condition != ConditionUniqueViolation {
			t.Fatalf("condition = %s, want unique_violation", translatedErr.Condition)
		}
		userErr := rebUserErr(t, translated)
		if userErr.Code() != string(dataframeerrors.CodeInternalError) || userErr.Retryable() {
			t.Fatalf("generic 23505 = %s retryable=%v, want non-retryable INTERNAL_ERROR (no unproven domain conflict)",
				userErr.Code(), userErr.Retryable())
		}
		s.rebScrubbedRendering(t, "23505 generic", translated)

		// Targeted CAS/insert path: this test code issued the guarded
		// insert itself and owns the constraint, so the conflict is proven.
		conflict, ok := AsUniqueConflict(err)
		if !ok || conflict == nil {
			t.Fatal("targeted insert path must obtain the proven conflict")
		}
		s.rebScrubbedRendering(t, "23505 conflict", conflict)

		// Control: a different key inserts fine — the guard fires only on
		// the actual violation.
		if _, err := s.iso.Exec(s.ctx,
			"INSERT INTO "+s.rebQualified(table)+" VALUES ('reb_other_key_canary')"); err != nil {
			t.Fatalf("control insert refused: %s", s.scrub(err))
		}
	})

	t.Run("aborted_transaction_25P02", func(t *testing.T) {
		s := newRebService(t)
		table := `"reb_abort_canary_table"`
		s.canaries = []string{s.schema, "reb_abort_canary_table", "reb_after_abort_canary"}
		if _, err := s.iso.Exec(s.ctx,
			"CREATE TABLE "+s.rebQualified(table)+" (id text PRIMARY KEY)"); err != nil {
			s.fail("create abort table", err)
		}
		client := s.client(t, 1)
		conn := s.acquire(t, client)
		tx, err := conn.BeginTx(s.ctx, pgx.TxOptions{})
		if err != nil {
			s.fail("begin tx", err)
		}
		if _, err := tx.Exec(s.ctx, "INSERT INTO "+s.rebQualified(table)+" VALUES ('reb_first_canary')"); err != nil {
			s.fail("first insert", err)
		}
		if _, err := tx.Exec(s.ctx, "INSERT INTO "+s.rebQualified(table)+" VALUES ('reb_first_canary')"); err == nil {
			t.Fatal("duplicate accepted inside tx: non-vacuity control broken")
		}
		_, err = tx.Exec(s.ctx, "INSERT INTO "+s.rebQualified(table)+" VALUES ('reb_after_abort_canary')")
		if err == nil {
			t.Fatal("statement after abort accepted: non-vacuity control broken")
		}
		if code := rebWitnessSQLState(t, err); code != sqlStateAbortedTransaction {
			t.Fatalf("witnessed SQLSTATE %s, want 25P02", code)
		}
		translated, ok := TranslateError(err)
		if !ok {
			t.Fatal("25P02 not translated")
		}
		translatedErr := translated.(*TranslatedError)
		if translatedErr.Condition != ConditionAbortedTransaction {
			t.Fatalf("condition = %s, want aborted_transaction", translatedErr.Condition)
		}
		if !IsAbortedTransaction(translated) {
			t.Fatal("policy predicate must recognize the aborted transaction")
		}
		// The translation must not claim the earlier write was rolled back.
		if strings.Contains(strings.ToLower(translated.Error()), "rollback") {
			t.Fatalf("25P02 rendering claims rollback: %s", translated.Error())
		}
		s.rebScrubbedRendering(t, "25P02", translated)
		if err := tx.Rollback(s.ctx); err != nil {
			s.fail("explicit rollback", err)
		}
		// Convergence witness (observed state, not inferred by the error):
		// the explicit ROLLBACK removed both tentative writes.
		var count int
		if err := s.iso.QueryRow(s.ctx,
			"SELECT count(*) FROM "+s.rebQualified(table)).Scan(&count); err != nil {
			s.fail("witness abort", err)
		}
		if count != 0 {
			t.Fatalf("rows after explicit rollback = %d, want 0", count)
		}
	})

	t.Run("admin_shutdown_57P01", func(t *testing.T) {
		s := newRebService(t)
		s.canaries = []string{s.schema}
		client := s.client(t, 2)
		victim := s.acquire(t, client)
		var pid int64
		if err := victim.QueryRow(s.ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
			s.fail("read victim pid", err)
		}
		if _, err := s.admin.Exec(s.ctx, "SELECT pg_terminate_backend($1)", pid); err != nil {
			s.fail("terminate victim", err)
		}
		time.Sleep(300 * time.Millisecond)
		var one int
		err := victim.QueryRow(s.ctx, "SELECT pg_sleep(5), 1").Scan(new(string), &one)
		if err == nil {
			t.Fatal("terminated session kept answering: non-vacuity control broken")
		}
		if code := rebWitnessSQLState(t, err); code != sqlStateAdminShutdown {
			t.Fatalf("witnessed SQLSTATE %s, want 57P01", code)
		}
		translated, ok := TranslateError(err)
		if !ok {
			t.Fatal("57P01 not translated")
		}
		translatedErr := translated.(*TranslatedError)
		if translatedErr.Condition != ConditionAdminShutdown {
			t.Fatalf("condition = %s, want admin_shutdown", translatedErr.Condition)
		}
		userErr := rebUserErr(t, translated)
		if userErr.Code() != string(dataframeerrors.CodeBackendUnavailable) || !userErr.Retryable() {
			t.Fatalf("admin shutdown = %s retryable=%v, want retryable BACKEND_UNAVAILABLE", userErr.Code(), userErr.Retryable())
		}
		s.rebScrubbedRendering(t, "57P01", translated)
		// Control: the service still admits fresh sessions; only the
		// terminated one died.
		fresh := s.acquire(t, client)
		if err := fresh.QueryRow(s.ctx, "SELECT 1").Scan(&one); err != nil || one != 1 {
			t.Fatalf("fresh session failed: %s", s.scrub(err))
		}
	})

	t.Run("connection_lost", func(t *testing.T) {
		s := newRebService(t)
		s.canaries = []string{s.schema}
		client := s.client(t, 2)
		victim := s.acquire(t, client)
		var pid int64
		if err := victim.QueryRow(s.ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
			s.fail("read victim pid", err)
		}
		if _, err := s.admin.Exec(s.ctx, "SELECT pg_terminate_backend($1)", pid); err != nil {
			s.fail("terminate victim", err)
		}
		time.Sleep(300 * time.Millisecond)
		// Drain the queued FATAL so the transport is genuinely dead
		// client-side; the next use must surface connection loss.
		var sink int
		_ = victim.QueryRow(s.ctx, "SELECT 1").Scan(&sink)
		err := victim.QueryRow(s.ctx, "SELECT 1").Scan(&sink)
		if err == nil {
			t.Fatal("dead connection still answering: non-vacuity control broken")
		}
		if !errors.Is(err, pgconn.ErrConnClosed) {
			t.Fatalf("dead connection must surface pgconn.ErrConnClosed, got %s", s.scrub(err))
		}
		translated, ok := TranslateError(err)
		if !ok {
			t.Fatal("dead-connection failure not translated")
		}
		translatedErr := translated.(*TranslatedError)
		if translatedErr.Condition != ConditionConnectionLost {
			t.Fatalf("condition = %s, want connection_lost", translatedErr.Condition)
		}
		userErr := rebUserErr(t, translated)
		if userErr.Code() != string(dataframeerrors.CodeBackendUnavailable) || !userErr.Retryable() {
			t.Fatalf("connection lost = %s retryable=%v, want retryable BACKEND_UNAVAILABLE", userErr.Code(), userErr.Retryable())
		}
		s.rebScrubbedRendering(t, "connection_lost", translated)
	})

	t.Run("connection_refused", func(t *testing.T) {
		s := newRebService(t)
		u, err := url.Parse(s.dsn)
		if err != nil {
			s.fail("parse dsn", err)
		}
		u.Host = "127.0.0.1:59999"
		_, err = Open(s.ctx, Config{
			DSN:            u.String(),
			MaxConns:       1,
			MinConns:       0,
			AcquireTimeout: 5 * time.Second,
		})
		if err == nil {
			t.Fatal("open against a closed port succeeded: non-vacuity control broken")
		}
		if !IsRefusedConnection(err) {
			t.Fatalf("expected a refused connection, got %s", s.scrub(err))
		}
		translated, ok := TranslateError(err)
		if !ok {
			t.Fatal("connect refused not translated")
		}
		if !errors.Is(translated, ErrConnect) {
			t.Fatal("translated refused must match ErrConnect (pg-store-01 contract)")
		}
		userErr := rebUserErr(t, translated)
		if userErr.Code() != string(dataframeerrors.CodeBackendUnavailable) || !userErr.Retryable() {
			t.Fatalf("refused = %s retryable=%v, want retryable BACKEND_UNAVAILABLE", userErr.Code(), userErr.Retryable())
		}
		s.rebScrubbedRendering(t, "refused", translated)
		// Control: Open against the live isolated database succeeds.
		if control := s.client(t, 1); control == nil {
			t.Fatal("control Open failed")
		}
	})

	t.Run("acquire_timeout_under_pool_pressure", func(t *testing.T) {
		s := newRebService(t)
		s.canaries = []string{s.schema}
		client := s.client(t, 1)
		held, err := client.Acquire(s.ctx)
		if err != nil {
			s.fail("hold connection", err)
		}
		actx, cancel := context.WithTimeout(s.ctx, 150*time.Millisecond)
		defer cancel()
		_, err = client.Acquire(actx)
		if err == nil {
			held.Release()
			t.Fatal("unbounded acquire succeeded: non-vacuity control broken")
		}
		if !errors.Is(err, ErrConnect) || !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("acquire timeout = %s, want ErrConnect + DeadlineExceeded", s.scrub(err))
		}
		translated, ok := TranslateError(err)
		if !ok {
			t.Fatal("acquire timeout not translated")
		}
		if !errors.Is(translated, context.DeadlineExceeded) {
			t.Fatal("translated acquire timeout must stay Is-visible as DeadlineExceeded")
		}
		userErr := rebUserErr(t, translated)
		if userErr.Code() != string(dataframeerrors.CodeBackendUnavailable) || !userErr.Retryable() {
			t.Fatalf("acquire timeout = %s retryable=%v, want retryable BACKEND_UNAVAILABLE", userErr.Code(), userErr.Retryable())
		}
		s.rebScrubbedRendering(t, "acquire timeout", translated)
		held.Release()
	})

	t.Run("ambiguous_commit", func(t *testing.T) {
		s := newRebService(t)
		table := `"reb_commit_canary_table"`
		s.canaries = []string{s.schema, "reb_commit_canary_table", "reb_ambiguous_canary"}
		if _, err := s.iso.Exec(s.ctx,
			"CREATE TABLE "+s.rebQualified(table)+" (id text PRIMARY KEY)"); err != nil {
			s.fail("create commit table", err)
		}
		client := s.client(t, 2)
		victim := s.acquire(t, client)
		tx, err := victim.BeginTx(s.ctx, pgx.TxOptions{})
		if err != nil {
			s.fail("begin tx", err)
		}
		if _, err := tx.Exec(s.ctx,
			"INSERT INTO "+s.rebQualified(table)+" VALUES ('reb_ambiguous_canary')"); err != nil {
			s.fail("in-tx insert", err)
		}
		var pid int64
		if err := victim.QueryRow(s.ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
			s.fail("read pid", err)
		}
		if _, err := s.admin.Exec(s.ctx, "SELECT pg_terminate_backend($1)", pid); err != nil {
			s.fail("terminate", err)
		}
		time.Sleep(300 * time.Millisecond)
		err = tx.Commit(s.ctx)
		if err == nil {
			t.Fatal("commit through a terminated session succeeded: non-vacuity control broken")
		}
		translated, ok := TranslateCommitError(err)
		if !ok {
			t.Fatal("commit failure not translated")
		}
		translatedErr := translated.(*TranslatedError)
		if !translatedErr.AmbiguousCommit || !errors.Is(translated, ErrCommitOutcomeUnknown) {
			t.Fatalf("commit failure = %#v, want ambiguous with ErrCommitOutcomeUnknown", translatedErr)
		}
		if userErr := rebUserErr(t, translated); userErr.Retryable() {
			t.Fatal("ambiguous commit must never be retryable")
		}
		_, mapped := rebHttpJSON(t, translated)
		if mapped.Body.Error.Retryable {
			t.Fatal("ambiguous commit must not advertise retry at HTTP")
		}
		s.rebScrubbedRendering(t, "ambiguous commit", translated)
		// The outcome itself stays unasserted on purpose: the boundary
		// never infers rollback, so the test only asserts that the error
		// surface refuses to claim either answer.
		for _, forbidden := range []string{"rolled back", "committed", "no write", "not written"} {
			if strings.Contains(strings.ToLower(translated.Error()), forbidden) {
				t.Fatalf("ambiguous commit rendering claims %q: %s", forbidden, translated.Error())
			}
		}
	})

	t.Run("definitive_commit_rejection", func(t *testing.T) {
		s := newRebService(t)
		table := `"reb_defer_canary_table"`
		constraint := "reb_defer_canary_ux"
		s.canaries = []string{s.schema, "reb_defer_canary_table", constraint, "reb_defer_dup_canary"}
		if _, err := s.iso.Exec(s.ctx, "CREATE TABLE "+s.rebQualified(table)+
			" (id text CONSTRAINT "+constraint+" UNIQUE DEFERRABLE INITIALLY DEFERRED)"); err != nil {
			s.fail("create deferred-unique table", err)
		}
		client := s.client(t, 1)
		conn := s.acquire(t, client)
		tx, err := conn.BeginTx(s.ctx, pgx.TxOptions{})
		if err != nil {
			s.fail("begin tx", err)
		}
		if _, err := tx.Exec(s.ctx,
			"INSERT INTO "+s.rebQualified(table)+" VALUES ('reb_defer_dup_canary')"); err != nil {
			s.fail("first deferred insert", err)
		}
		if _, err := tx.Exec(s.ctx,
			"INSERT INTO "+s.rebQualified(table)+" VALUES ('reb_defer_dup_canary')"); err != nil {
			s.fail("second deferred insert", err)
		}
		err = tx.Commit(s.ctx)
		if err == nil {
			t.Fatal("deferred constraint accepted a duplicate: non-vacuity control broken")
		}
		translated, ok := TranslateCommitError(err)
		if !ok {
			t.Fatal("deferred commit rejection not translated")
		}
		translatedErr := translated.(*TranslatedError)
		if translatedErr.AmbiguousCommit {
			t.Fatal("a server-answered commit rejection must not be ambiguous")
		}
		if errors.Is(translated, ErrCommitOutcomeUnknown) {
			t.Fatal("a server-answered commit rejection must not claim unknown outcome")
		}
		if translatedErr.Condition != ConditionUniqueViolation || !IsUniqueViolation(translated) {
			t.Fatalf("condition = %s, want unique_violation recognized by the predicate", translatedErr.Condition)
		}
		s.rebScrubbedRendering(t, "deferred rejection", translated)
	})
}

// TestZZZeroLeftoverRebPostgresErrorFixtures is the ownership-scoped
// zero-leftover audit: exact names this file created, plus its own
// verify_reb_* prefix count against the pre-test baseline. It never inspects
// foreign prefixes, so sibling workers sweeping their own fixtures cannot
// trip it.
func TestZZZeroLeftoverRebPostgresErrorFixtures(t *testing.T) {
	raw := rebURL(t)
	rebOwnedMu.Lock()
	owned := make(map[string]string, len(rebOwnedByName))
	for db, schema := range rebOwnedByName {
		owned[db] = schema
	}
	rebOwnedMu.Unlock()
	if len(owned) == 0 {
		t.Skip("this process created no verify_reb_* fixtures")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		t.Fatalf("connect to test service for audit: %v", err)
	}
	defer admin.Close()

	for db, schema := range owned {
		if !strings.HasPrefix(db, rebFixturePrefix) {
			t.Fatalf("registry holds foreign database %q", db)
		}
		var count int
		if err := admin.QueryRow(ctx, "SELECT COUNT(*) FROM pg_database WHERE datname = $1", db).Scan(&count); err != nil {
			t.Errorf("audit owned database %s: %v", db, err)
			continue
		}
		if count != 0 {
			t.Errorf("leftover owned database %s after all cleanups", db)
			if schema != "" {
				u, err := url.Parse(raw)
				if err == nil {
					if q := u.Query(); q.Get("dbname") != "" {
						q.Del("dbname")
						u.RawQuery = q.Encode()
					}
					u.Path = "/" + db
					if probe, err := pgxpool.New(ctx, u.String()); err == nil {
						var schemaCount int
						if err := probe.QueryRow(ctx, "SELECT COUNT(*) FROM pg_namespace WHERE nspname = $1", schema).Scan(&schemaCount); err != nil {
							t.Errorf("audit owned schema %s in %s: %v", schema, db, err)
						} else if schemaCount != 0 {
							t.Errorf("leftover owned schema %s in leftover database %s", schema, db)
						}
						probe.Close()
					}
				}
			}
		}
	}

	var visible int64
	if err := admin.QueryRow(ctx,
		"SELECT count(*) FROM pg_database WHERE datname LIKE 'verify\\_reb\\_%' ESCAPE '\\'").
		Scan(&visible); err != nil {
		t.Fatalf("audit verify_reb_* database count: %v", err)
	}
	if visible != rebBaselineCount {
		t.Errorf("verify_reb_* databases = %d, want baseline %d", visible, rebBaselineCount)
	}
}
