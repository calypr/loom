package postgres

// This file is the PostgreSQL runtime error boundary. It translates driver
// and server failures into stable Loom errors at the adapter seam so that
// server and client paths can present safe messages without ever rendering
// SQL text, bound argument values, DSN material, or server-provided
// messages, constraint names, or schema names.
//
// Classification is structural — SQLSTATE codes from *pgconn.PgError, driver
// sentinel identities, and severity — never English text matching. The
// boundary composes with the pool sentinels of this package (ErrConnect and
// friends from client.go) and with the merged execution seam: it recognizes
// driver failures wrapped by adapter envelopes as long as the identity
// stays reachable through errors.Is/errors.As.
//
// The raw driver cause is retained unexported and reachable only through
// Cause, never through Unwrap, errors.Is/errors.As, or any rendered form.
// No production logging call site consumes Cause yet: the server diagnostic
// logger currently has no PostgreSQL failure path (the server refuses to
// start against a PostgreSQL backend and the PG row-executor composition
// lands in a later ticket), so this package intentionally ships as the
// boundary API. The composition ticket must route translated failures into
// the existing diagnostic logger — reading Cause behind the redaction and
// length guards there — before the raw cause reaches any log; until then
// translated failures carry only the stable condition and SQLSTATE.

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"

	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// SQLSTATEs recognized by the boundary. Values are pinned to the PostgreSQL
// 18 server (see errors_test.go for the real-server witnesses).
const (
	sqlStateSerializationFailure = "40001" // could not serialize access
	sqlStateDeadlockDetected     = "40P01" // deadlock detected
	sqlStateUniqueViolation      = "23505" // unique_violation
	sqlStateAbortedTransaction   = "25P02" // current transaction is aborted
	sqlStateQueryCanceled        = "57014" // statement canceled by server policy
	sqlStateAdminShutdown        = "57P01" // terminating connection due to administrator command
	sqlStateCrashShutdown        = "57P02" // terminating connection due to crash shutdown
	sqlStateCannotConnectNow     = "57P03" // rejecting connections due to recovery
	sqlStateIdleInTxTerminated   = "25P01" // idle-in-transaction session kill (legacy code)
	sqlStateIdleInTxTerminated18 = "25P03" // idle-in-transaction session kill (PostgreSQL 18)
)

// DriverCondition names one stable classification of a PostgreSQL failure at
// the adapter boundary. Condition values are a frozen contract: new values
// may be added; existing values never change meaning.
type DriverCondition string

const (
	// ConditionConnectionFailure: the server could not be reached or the
	// pool could not hand out a connection.
	ConditionConnectionFailure DriverCondition = "connection_failure"
	// ConditionConnectionLost: an established connection's transport died.
	ConditionConnectionLost DriverCondition = "connection_lost"
	// ConditionAdminShutdown: the server terminated the session for an
	// administrative reason (SQLSTATE 57P01).
	ConditionAdminShutdown DriverCondition = "admin_shutdown"
	// ConditionCrashShutdown: the server terminated sessions for a crash
	// shutdown (SQLSTATE 57P02).
	ConditionCrashShutdown DriverCondition = "crash_shutdown"
	// ConditionCannotConnectNow: the server is up but rejecting
	// connections, e.g. during recovery (SQLSTATE 57P03).
	ConditionCannotConnectNow DriverCondition = "cannot_connect_now"
	// ConditionSessionTerminated: the server killed the session under a
	// policy timeout such as idle-in-transaction (SQLSTATE 25P01/25P03).
	ConditionSessionTerminated DriverCondition = "session_terminated"
	// ConditionQueryCanceled: the server canceled the statement under its
	// own policy (SQLSTATE 57014), e.g. statement_timeout.
	ConditionQueryCanceled DriverCondition = "query_canceled"
	// ConditionSerializationFailure: a serializable transaction must be
	// retried only where operation policy permits (SQLSTATE 40001).
	ConditionSerializationFailure DriverCondition = "serialization_failure"
	// ConditionDeadlockDetected: the server chose this transaction as the
	// deadlock victim; retry is policy-only (SQLSTATE 40P01).
	ConditionDeadlockDetected DriverCondition = "deadlock_detected"
	// ConditionUniqueViolation: a unique constraint refused a write
	// (SQLSTATE 23505). This generic condition is deliberately not a
	// domain conflict; only targeted CAS/insert callers that can prove the
	// statement's meaning may upgrade it via AsUniqueConflict.
	ConditionUniqueViolation DriverCondition = "unique_violation"
	// ConditionAbortedTransaction: the current transaction is aborted and
	// refuses statements until it ends (SQLSTATE 25P02). The boundary
	// never infers that prior writes were rolled back; the caller must
	// end the transaction explicitly.
	ConditionAbortedTransaction DriverCondition = "aborted_transaction"
	// ConditionTransactionClosed: an operation was attempted on a
	// transaction the client already ended (pgx.ErrTxClosed).
	ConditionTransactionClosed DriverCondition = "transaction_closed"
	// ConditionRequestCanceled: the caller canceled while waiting for a
	// pool connection. This is the caller giving up, not an outage: it is
	// never marked retryable.
	ConditionRequestCanceled DriverCondition = "request_canceled"
	// ConditionCommitRejected: the server definitively rejected the
	// commit of an in-flight transaction.
	ConditionCommitRejected DriverCondition = "commit_rejected"
	// ConditionServerRejected: a server error the boundary does not
	// classify further. It is an internal error, never a public detail.
	ConditionServerRejected DriverCondition = "server_rejected"
)

// ErrCommitOutcomeUnknown marks a failed COMMIT whose effect cannot be
// determined: the statement may or may not have reached the server. Callers
// must treat the write as possible, never assume rollback, and never treat
// this error as an invitation to blindly resubmit.
var ErrCommitOutcomeUnknown = errors.New("postgres: commit outcome unknown")

// ErrUniqueConflict marks the domain-conflict form of a unique violation
// whose meaning a targeted CAS/insert caller has proven. See AsUniqueConflict.
var ErrUniqueConflict = errors.New("postgres: unique conflict")

// TranslatedError is a PostgreSQL failure classified by the boundary. Its
// rendered forms are safe for any surface: Error reports only the stable
// condition and SQLSTATE. Errors.Is/errors.As reach only curated, secret-free
// sentinels and the embedded safe typed error. The raw driver cause is
// available to server-side logging through Cause and is never rendered or
// Is/As-visible.
type TranslatedError struct {
	// Condition is the stable classification of the failure.
	Condition DriverCondition
	// SQLState is the five-character SQLSTATE from the server, or "" for
	// client-side transport and transaction failures.
	SQLState string
	// AmbiguousCommit reports a failed COMMIT whose outcome the server
	// never confirmed. Such an error is never retryable and never
	// warrants an assumed rollback.
	AmbiguousCommit bool

	cause   error
	unwraps []error
}

func (e *TranslatedError) Error() string {
	if e == nil {
		return "postgres: translated driver failure"
	}
	if e.SQLState != "" {
		return fmt.Sprintf("postgres: %s (SQLSTATE %s)", e.Condition, e.SQLState)
	}
	return fmt.Sprintf("postgres: %s", e.Condition)
}

func (e *TranslatedError) Unwrap() []error {
	if e == nil {
		return nil
	}
	return e.unwraps
}

// Cause returns the raw driver or server error. It can contain SQL text,
// constraint names, or DSN material and must never be rendered into a
// client-visible surface; a consumer must confine it to server-side logging
// behind redaction and length guards. No production consumer exists yet
// (see the file header); this accessor keeps the information lossless for
// the composition ticket that adds one.
func (e *TranslatedError) Cause() error {
	if e == nil {
		return nil
	}
	return e.cause
}

// translateSpec configures one translation's public semantics.
type translateSpec struct {
	condition DriverCondition
	code      dataframeerrors.ErrorCode
	retryable bool
	// sentinels are curated secret-free errors added to Unwrap besides the
	// typed error itself.
	sentinels []error
	ambiguous bool
}

// newTranslated builds a TranslatedError. The typed error carries only
// code-derived public messages and fixed, safe details.
func newTranslated(spec translateSpec, sqlState string, cause error) *TranslatedError {
	typedCause := spec.code == dataframeerrors.CodeBackendUnavailable
	var typedWrapped error
	if typedCause {
		typedWrapped = ErrConnect
	}
	details := map[string]any{"backend": "postgres", "condition": string(spec.condition)}
	if sqlState != "" {
		details["sqlstate"] = sqlState
	}
	typed := dataframeerrors.Wrap(typedWrapped, spec.code, "",
		dataframeerrors.WithRetryable(spec.retryable),
		dataframeerrors.WithDetails(details),
	)
	unwraps := make([]error, 0, len(spec.sentinels)+1)
	unwraps = append(unwraps, spec.sentinels...)
	unwraps = append(unwraps, typed)
	return &TranslatedError{
		Condition:       spec.condition,
		SQLState:        sqlState,
		AmbiguousCommit: spec.ambiguous,
		cause:           cause,
		unwraps:         unwraps,
	}
}

// unavailableSpec builds the retryable backend-unavailable classification
// used for connectivity conditions.
func unavailableSpec(condition DriverCondition) translateSpec {
	return translateSpec{
		condition: condition,
		code:      dataframeerrors.CodeBackendUnavailable,
		retryable: true,
		sentinels: []error{ErrConnect},
	}
}

// internalSpec builds the non-retryable internal classification used for
// conditions that must not invite blanket retries.
func internalSpec(condition DriverCondition, sentinels ...error) translateSpec {
	return translateSpec{
		condition: condition,
		code:      dataframeerrors.CodeInternalError,
		retryable: false,
		sentinels: sentinels,
	}
}

// TranslateError maps PostgreSQL driver and server failures reaching the
// adapter boundary onto stable Loom errors:
//
//   - Known connectivity failures (pool open/acquire failures, driver
//     connect failures, dead transports, server shutdown classes) become
//     retryable backend-unavailable, still matching ErrConnect and any
//     relevant context sentinel.
//   - Serialization failures and deadlocks become non-retryable internal
//     errors. Retry is a decision for the operation's policy, expressed via
//     RetryableTransactionConflict; the boundary never retries and never
//     marks these blanket-retryable.
//   - A bare unique violation becomes a redacted internal error, never a
//     domain conflict. Only targeted CAS/insert callers that can prove the
//     statement's meaning may upgrade it via AsUniqueConflict.
//   - An aborted transaction never gets a rollback claim; use
//     IsAbortedTransaction to stop issuing statements and end the
//     transaction explicitly.
//   - A server-canceled statement (57014, e.g. statement_timeout) becomes
//     the query-resource-limit classification.
//
// Idempotence and composition: nil yields (nil, false); an error that
// already carries a translated condition or a Loom typed error is returned
// unchanged with ok=true; anything not recognized as PostgreSQL-owned is
// returned unchanged with ok=false, preserving existing classification
// behavior (including Arango).
func TranslateError(err error) (translated error, ok bool) {
	return translate(err, false)
}

// TranslateCommitError classifies a failure observed while committing a
// transaction. Unlike query failures, a COMMIT failure can be ambiguous:
// the server may or may not have applied the transaction. An ambiguous
// outcome is marked with AmbiguousCommit and ErrCommitOutcomeUnknown, is
// never retryable, and must never be treated as "the write did not happen"
// — only the server can say that, and it did not.
//
// Definitive outcomes stay definitive: a non-fatal server error answered
// the commit (it was rejected and rolled back), a failure the driver
// guarantees happened before any bytes were sent never reached the server,
// and pgx's own rollback detection (ErrTxCommitRollback) is a confirmed
// rejection.
func TranslateCommitError(err error) (translated error, ok bool) {
	return translate(err, true)
}

func translate(err error, commitPhase bool) (error, bool) {
	if err == nil {
		return nil, false
	}
	var already *TranslatedError
	if errors.As(err, &already) {
		return err, true
	}
	if _, ok := dataframeerrors.AsUserError(err); ok {
		return err, true
	}

	if errors.Is(err, pgx.ErrTxCommitRollback) {
		// The driver confirmed the commit resulted in rollback: definitive.
		return newTranslated(internalSpec(ConditionCommitRejected), "", err), true
	}
	if errors.Is(err, pgx.ErrTxClosed) {
		return newTranslated(internalSpec(ConditionTransactionClosed), "", err), true
	}

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		if commitPhase && commitOutcomeIsUncertain(pgErr) {
			// The session died mid-commit. The COMMIT may or may not have
			// been applied: leave the outcome ambiguous, never infer
			// rollback, never mark retryable.
			spec := internalSpec(shutdownCondition(pgErr.Code), ErrCommitOutcomeUnknown, ErrConnect)
			spec.ambiguous = true
			return newTranslated(spec, pgErr.Code, err), true
		}
		return translateSQLState(pgErr, err)
	}

	if isPGTransportFailure(err) {
		if !commitPhase && errors.Is(err, context.Canceled) {
			// The caller gave up while waiting for the pool: same public
			// semantics as any other canceled request, never retryable.
			spec := translateSpec{
				condition: ConditionRequestCanceled,
				code:      dataframeerrors.CodeClientCanceled,
				retryable: false,
				sentinels: []error{ErrConnect, context.Canceled},
			}
			return newTranslated(spec, "", err), true
		}
		condition := ConditionConnectionFailure
		if errors.Is(err, pgconn.ErrConnClosed) {
			condition = ConditionConnectionLost
		}
		spec := unavailableSpec(condition)
		appendContextSentinels(&spec, err)
		if commitPhase {
			if pgconn.SafeToRetry(err) {
				// The driver guarantees COMMIT was never sent: a retry
				// policy may decide what is safe; nothing is retried here.
				return newTranslated(spec, "", err), true
			}
			spec = internalSpec(condition, ErrCommitOutcomeUnknown, ErrConnect)
			spec.ambiguous = true
			appendContextSentinels(&spec, err)
			return newTranslated(spec, "", err), true
		}
		return newTranslated(spec, "", err), true
	}

	return err, false
}

// translateSQLState classifies one server SQLSTATE.
func translateSQLState(pgErr *pgconn.PgError, raw error) (error, bool) {
	switch {
	case strings.HasPrefix(pgErr.Code, "08"): // class 08: connection exceptions
		return newTranslated(unavailableSpec(ConditionConnectionLost), pgErr.Code, raw), true
	case pgErr.Code == sqlStateAdminShutdown:
		return newTranslated(unavailableSpec(ConditionAdminShutdown), pgErr.Code, raw), true
	case pgErr.Code == sqlStateCrashShutdown:
		return newTranslated(unavailableSpec(ConditionCrashShutdown), pgErr.Code, raw), true
	case pgErr.Code == sqlStateCannotConnectNow:
		return newTranslated(unavailableSpec(ConditionCannotConnectNow), pgErr.Code, raw), true
	case pgErr.Code == sqlStateIdleInTxTerminated, pgErr.Code == sqlStateIdleInTxTerminated18:
		return newTranslated(unavailableSpec(ConditionSessionTerminated), pgErr.Code, raw), true
	case pgErr.Code == sqlStateQueryCanceled:
		// Server-policy cancellation (e.g. statement_timeout). Classified as
		// a query resource limit, mirroring the Arango limit classification;
		// resubmission is a caller decision, not a blanket retry.
		return newTranslated(translateSpec{
			condition: ConditionQueryCanceled,
			code:      dataframeerrors.CodeQueryResourceLimitExceeded,
			retryable: false,
		}, pgErr.Code, raw), true
	case pgErr.Code == sqlStateSerializationFailure:
		return newTranslated(internalSpec(ConditionSerializationFailure), pgErr.Code, raw), true
	case pgErr.Code == sqlStateDeadlockDetected:
		return newTranslated(internalSpec(ConditionDeadlockDetected), pgErr.Code, raw), true
	case pgErr.Code == sqlStateUniqueViolation:
		// Generic: redacted internal error. Domain conflicts belong to
		// targeted CAS/insert callers via AsUniqueConflict.
		return newTranslated(internalSpec(ConditionUniqueViolation), pgErr.Code, raw), true
	case pgErr.Code == sqlStateAbortedTransaction:
		return newTranslated(internalSpec(ConditionAbortedTransaction), pgErr.Code, raw), true
	default:
		return newTranslated(internalSpec(ConditionServerRejected), pgErr.Code, raw), true
	}
}

// commitOutcomeIsUncertain reports whether a server error answered COMMIT
// in a way that leaves the transaction outcome unknown. The session-death
// SQLSTATE classes (08*, 57P01/02/03, 25P01/25P03) terminate the session by
// protocol definition, so the commit may or may not have been applied even
// if the severity field reads otherwise. Severity is classified on the
// unlocalized field (V) first because the localized Severity field (S) is
// translated under non-English lc_messages (e.g. "SCHWERWIEGEND" for FATAL):
// matching English text on S would defeat the ambiguous-COMMIT guarantee
// and violate the structural-classification rule. Severity is consulted
// only when the server sent no unlocalized severity.
func commitOutcomeIsUncertain(pgErr *pgconn.PgError) bool {
	if isSessionEndSQLState(pgErr.Code) {
		return true
	}
	severity := pgErr.SeverityUnlocalized
	if severity == "" {
		severity = pgErr.Severity
	}
	return strings.EqualFold(severity, "FATAL") || strings.EqualFold(severity, "PANIC")
}

// isSessionEndSQLState reports SQLSTATEs whose messages terminate the
// session: connection exceptions (class 08) and the shutdown/idle-kill
// classes.
func isSessionEndSQLState(code string) bool {
	switch code {
	case sqlStateAdminShutdown, sqlStateCrashShutdown, sqlStateCannotConnectNow,
		sqlStateIdleInTxTerminated, sqlStateIdleInTxTerminated18:
		return true
	default:
		return strings.HasPrefix(code, "08")
	}
}

// shutdownCondition maps a session-death SQLSTATE observed mid-commit to its
// stable condition name.
func shutdownCondition(code string) DriverCondition {
	switch code {
	case sqlStateAdminShutdown:
		return ConditionAdminShutdown
	case sqlStateCrashShutdown:
		return ConditionCrashShutdown
	case sqlStateCannotConnectNow:
		return ConditionCannotConnectNow
	case sqlStateIdleInTxTerminated, sqlStateIdleInTxTerminated18:
		return ConditionSessionTerminated
	default:
		if strings.HasPrefix(code, "08") {
			return ConditionConnectionLost
		}
		return ConditionServerRejected
	}
}

// isPGTransportFailure reports whether err is a driver- or pool-level
// connectivity failure, recognized structurally through types and sentinels.
func isPGTransportFailure(err error) bool {
	var clientConnect *ConnectError
	if errors.As(err, &clientConnect) {
		return true
	}
	var driverConnect *pgconn.ConnectError
	if errors.As(err, &driverConnect) {
		return true
	}
	if errors.Is(err, ErrConnect) || errors.Is(err, pgconn.ErrConnClosed) || errors.Is(err, driver.ErrBadConn) {
		return true
	}
	return false
}

// appendContextSentinels adds context sentinels to a spec so callers keep
// the existing cancellation and deadline semantics: an acquire deadline or
// cancellation stays Is-visible as the matching context error.
func appendContextSentinels(spec *translateSpec, err error) {
	if errors.Is(err, context.Canceled) {
		spec.sentinels = append(spec.sentinels, context.Canceled)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		spec.sentinels = append(spec.sentinels, context.DeadlineExceeded)
	}
}

// IsUniqueViolation reports whether err carries a PostgreSQL
// unique_violation (SQLSTATE 23505), translated or raw. Recognition is the
// precondition for a targeted CAS/insert path to upgrade the failure into a
// domain conflict via AsUniqueConflict; generic query paths must not.
func IsUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	var translated *TranslatedError
	if errors.As(err, &translated) && translated.Condition == ConditionUniqueViolation {
		return true
	}
	if hasSQLState(err, sqlStateUniqueViolation) {
		return true
	}
	var conflict *UniqueConflictError
	return errors.As(err, &conflict)
}

// IsAbortedTransaction reports whether err carries PostgreSQL's
// in_failed_sql_transaction (SQLSTATE 25P02): the transaction is aborted
// and refuses further statements until it ends. This never establishes
// whether prior statements wrote anything; end the transaction explicitly.
func IsAbortedTransaction(err error) bool {
	if err == nil {
		return false
	}
	var translated *TranslatedError
	if errors.As(err, &translated) && translated.Condition == ConditionAbortedTransaction {
		return true
	}
	return hasSQLState(err, sqlStateAbortedTransaction)
}

// RetryableTransactionConflict reports whether err is a serialization
// failure or deadlock: conditions whose retry is safe for read-only or
// re-derivable operations at the discretion of the operation's policy. The
// boundary itself never retries and never marks these errors retryable;
// blanket retry stays out.
func RetryableTransactionConflict(err error) bool {
	if err == nil {
		return false
	}
	var translated *TranslatedError
	if errors.As(err, &translated) {
		switch translated.Condition {
		case ConditionSerializationFailure, ConditionDeadlockDetected:
			return true
		}
	}
	return hasSQLState(err, sqlStateSerializationFailure) || hasSQLState(err, sqlStateDeadlockDetected)
}

// hasSQLState reaches the SQLSTATE through adapter wrappers, translated raw
// causes, and bare driver errors without any text matching.
func hasSQLState(err error, code string) bool {
	if err == nil {
		return false
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == code
	}
	var translated *TranslatedError
	if errors.As(err, &translated) {
		if translated.SQLState == code {
			return true
		}
		var causePgErr *pgconn.PgError
		if errors.As(translated.cause, &causePgErr) {
			return causePgErr.Code == code
		}
	}
	return false
}

// UniqueConflictError is the domain-conflict form of a proven unique
// violation. Its rendered text is safe; it unwraps to ErrUniqueConflict and
// the original failure so targeted callers can map it onto their own
// conflict semantics.
type UniqueConflictError struct {
	cause error
}

func (e *UniqueConflictError) Error() string {
	return fmt.Sprintf("postgres: unique conflict (SQLSTATE %s)", sqlStateUniqueViolation)
}

func (e *UniqueConflictError) Unwrap() []error {
	if e == nil {
		return nil
	}
	return []error{ErrUniqueConflict, e.cause}
}

// AsUniqueConflict upgrades a unique violation to a domain conflict, but
// only for a targeted CAS/insert path that can prove the statement's
// meaning — code that issued the guarded write itself and knows which
// constraint it owns. Callers that cannot prove the statement's meaning
// must not use this: a bare unique violation stays the redacted internal
// error that TranslateError produces, because an unproven 23505 says
// nothing trustworthy about the caller's intended write.
func AsUniqueConflict(err error) (*UniqueConflictError, bool) {
	if !IsUniqueViolation(err) {
		return nil, false
	}
	return &UniqueConflictError{cause: err}, true
}
