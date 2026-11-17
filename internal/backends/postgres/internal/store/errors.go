package postgres

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

const (
	sqlStateSerializationFailure = "40001"
	sqlStateDeadlockDetected     = "40P01"
	sqlStateUniqueViolation      = "23505"
	sqlStateForeignKeyViolation  = "23503"
	sqlStateUndefinedTable       = "42P01"
	sqlStateAbortedTransaction   = "25P02"
	sqlStateQueryCanceled        = "57014"
	sqlStateAdminShutdown        = "57P01"
	sqlStateCrashShutdown        = "57P02"
	sqlStateCannotConnectNow     = "57P03"
	sqlStateIdleInTxTerminated   = "25P01"
	sqlStateIdleInTxTerminated18 = "25P03"
)

type DriverCondition string

const (
	ConditionConnectionFailure    DriverCondition = "connection_failure"
	ConditionConnectionLost       DriverCondition = "connection_lost"
	ConditionAdminShutdown        DriverCondition = "admin_shutdown"
	ConditionCrashShutdown        DriverCondition = "crash_shutdown"
	ConditionCannotConnectNow     DriverCondition = "cannot_connect_now"
	ConditionSessionTerminated    DriverCondition = "session_terminated"
	ConditionQueryCanceled        DriverCondition = "query_canceled"
	ConditionSerializationFailure DriverCondition = "serialization_failure"
	ConditionDeadlockDetected     DriverCondition = "deadlock_detected"
	ConditionUniqueViolation      DriverCondition = "unique_violation"
	ConditionAbortedTransaction   DriverCondition = "aborted_transaction"
	ConditionTransactionClosed    DriverCondition = "transaction_closed"
	ConditionRequestCanceled      DriverCondition = "request_canceled"
	ConditionCommitRejected       DriverCondition = "commit_rejected"
	ConditionServerRejected       DriverCondition = "server_rejected"
)

var ErrCommitOutcomeUnknown = errors.New("postgres: commit outcome unknown")

var ErrUniqueConflict = errors.New("postgres: unique conflict")

type TranslatedError struct {
	Condition       DriverCondition
	SQLState        string
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

func (e *TranslatedError) Cause() error {
	if e == nil {
		return nil
	}
	return e.cause
}

type translateSpec struct {
	condition DriverCondition
	code      dataframeerrors.ErrorCode
	retryable bool
	sentinels []error
	ambiguous bool
}

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

func unavailableSpec(condition DriverCondition) translateSpec {
	return translateSpec{
		condition: condition,
		code:      dataframeerrors.CodeBackendUnavailable,
		retryable: true,
		sentinels: []error{ErrConnect},
	}
}

func internalSpec(condition DriverCondition, sentinels ...error) translateSpec {
	return translateSpec{
		condition: condition,
		code:      dataframeerrors.CodeInternalError,
		retryable: false,
		sentinels: sentinels,
	}
}

func TranslateError(err error) (translated error, ok bool) {
	return translate(err, false)
}

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
		return newTranslated(internalSpec(ConditionCommitRejected), "", err), true
	}
	if errors.Is(err, pgx.ErrTxClosed) {
		return newTranslated(internalSpec(ConditionTransactionClosed), "", err), true
	}

	if pgErr, ok := pgErrorFrom(err); ok {
		if commitPhase && commitOutcomeIsUncertain(pgErr) {
			spec := internalSpec(shutdownCondition(pgErr.Code), ErrCommitOutcomeUnknown, ErrConnect)
			spec.ambiguous = true
			return newTranslated(spec, pgErr.Code, err), true
		}
		return translateSQLState(pgErr, err)
	}

	if isPGTransportFailure(err) {
		if !commitPhase && errors.Is(err, context.Canceled) {
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

func pgErrorFrom(err error) (*pgconn.PgError, bool) {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr == nil {
		return nil, false
	}
	return pgErr, true
}

func translateSQLState(pgErr *pgconn.PgError, raw error) (error, bool) {
	switch {
	case strings.HasPrefix(pgErr.Code, "08"):
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
		return newTranslated(internalSpec(ConditionUniqueViolation), pgErr.Code, raw), true
	case pgErr.Code == sqlStateAbortedTransaction:
		return newTranslated(internalSpec(ConditionAbortedTransaction), pgErr.Code, raw), true
	default:
		return newTranslated(internalSpec(ConditionServerRejected), pgErr.Code, raw), true
	}
}

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

func isSessionEndSQLState(code string) bool {
	switch code {
	case sqlStateAdminShutdown, sqlStateCrashShutdown, sqlStateCannotConnectNow,
		sqlStateIdleInTxTerminated, sqlStateIdleInTxTerminated18:
		return true
	default:
		return strings.HasPrefix(code, "08")
	}
}

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

func appendContextSentinels(spec *translateSpec, err error) {
	if errors.Is(err, context.Canceled) {
		spec.sentinels = append(spec.sentinels, context.Canceled)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		spec.sentinels = append(spec.sentinels, context.DeadlineExceeded)
	}
}

func IsUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	var translated *TranslatedError
	if errors.As(err, &translated) && translated != nil && translated.Condition == ConditionUniqueViolation {
		return true
	}
	if hasSQLState(err, sqlStateUniqueViolation) {
		return true
	}
	var conflict *UniqueConflictError
	return errors.As(err, &conflict)
}

func IsAbortedTransaction(err error) bool {
	if err == nil {
		return false
	}
	var translated *TranslatedError
	if errors.As(err, &translated) && translated != nil && translated.Condition == ConditionAbortedTransaction {
		return true
	}
	return hasSQLState(err, sqlStateAbortedTransaction)
}

func RetryableTransactionConflict(err error) bool {
	if err == nil {
		return false
	}
	var translated *TranslatedError
	if errors.As(err, &translated) && translated != nil {
		switch translated.Condition {
		case ConditionSerializationFailure, ConditionDeadlockDetected:
			return true
		}
	}
	return hasSQLState(err, sqlStateSerializationFailure) || hasSQLState(err, sqlStateDeadlockDetected)
}

func IsUniqueConstraintViolation(err error, constraints ...string) bool {
	return matchesDriverViolation(err, sqlStateUniqueViolation, constraints)
}

func IsForeignKeyConstraintViolation(err error, constraints ...string) bool {
	return matchesDriverViolation(err, sqlStateForeignKeyViolation, constraints)
}

func IsUndefinedTable(err error) bool {
	pgErr, ok := pgErrorFrom(err)
	return ok && pgErr.Code == sqlStateUndefinedTable
}

func IsDeadlockDetected(err error) bool {
	pgErr, ok := pgErrorFrom(err)
	return ok && pgErr.Code == sqlStateDeadlockDetected
}

func matchesDriverViolation(err error, code string, constraints []string) bool {
	pgErr, ok := pgErrorFrom(err)
	if !ok || pgErr.Code != code || len(constraints) == 0 {
		return false
	}
	for _, name := range constraints {
		if pgErr.ConstraintName == name {
			return true
		}
	}
	return false
}

func hasSQLState(err error, code string) bool {
	if err == nil {
		return false
	}
	if pgErr, ok := pgErrorFrom(err); ok {
		return pgErr.Code == code
	}
	var translated *TranslatedError
	if errors.As(err, &translated) && translated != nil {
		if translated.SQLState == code {
			return true
		}
		if causePgErr, ok := pgErrorFrom(translated.cause); ok {
			return causePgErr.Code == code
		}
	}
	return false
}

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

func AsUniqueConflict(err error) (*UniqueConflictError, bool) {
	if !IsUniqueViolation(err) {
		return nil, false
	}
	return &UniqueConflictError{cause: err}, true
}
