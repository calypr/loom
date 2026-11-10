package arango

// This file is the ArangoDB runtime error boundary. It is the adapter-side
// home for interpreting driver failures: shared execution and HTTP/GraphQL
// transport consume Loom errors, and only this adapter reads ArangoDB error
// numbers and messages. Translation runs at the one cursor choke point
// (queryRowsWithOptions), so every row-query entry path — QueryRows,
// QueryRowsWithMaxRuntime, and the transaction callback's RowQueryer —
// classifies identically. Classification semantics are a verbatim port of the
// former server composition classifier: same codes, same public messages,
// same safe details, same non-inference controls. The original cause stays
// wrapped so errors.Is/errors.As continue to reach the driver error, matching
// pre-move behavior. The raw ArangoDB message reaches server-side logs only
// through DiagnosticCause, behind the transport redaction rules.

import (
	"errors"
	"fmt"
	"strings"

	"github.com/arangodb/go-driver/v2/arangodb/shared"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
)

// isQueryMemoryLimitExceeded reports whether ArangoDB rejected a query because
// it exceeded the configured query memory limit.
func isQueryMemoryLimitExceeded(err error) bool {
	if !isQueryResourceLimitExceeded(err) {
		return false
	}
	ok, arangoErr := shared.IsArangoError(err)
	return ok && strings.Contains(strings.ToLower(arangoErr.ErrorMessage), "memory")
}

// isQueryResourceLimitExceeded reports whether ArangoDB rejected a query at a
// configured resource limit. Callers can use isQueryMemoryLimitExceeded first
// when they need the narrower diagnosis.
func isQueryResourceLimitExceeded(err error) bool {
	return shared.IsArangoErrorWithErrorNum(err, shared.ErrResourceLimit)
}

// isQueryOutOfMemory reports whether ArangoDB ran out of memory while
// executing a query. This is distinct from rejecting a query at a configured
// memory limit.
func isQueryOutOfMemory(err error) bool {
	return shared.IsArangoErrorWithErrorNum(err, shared.ErrOutOfMemory)
}

// isQueryUserAssertion reports whether AQL aborted through ASSERT with the
// exact stable code emitted by Loom's typed physical plan.
func isQueryUserAssertion(err error, code string) bool {
	if strings.TrimSpace(code) == "" {
		return false
	}
	ok, arangoErr := shared.IsArangoError(err)
	return ok && arangoErr.ErrorNum == shared.ErrQueryUserAssert && strings.Contains(arangoErr.ErrorMessage, "AQL: "+code)
}

// TranslateQueryError maps ArangoDB query failures onto stable Loom errors at
// the adapter seam:
//
//   - AQL ASSERT failures carrying one of Loom's typed physical-plan codes
//     become that code's typed user error, non-retryable. An ASSERT with an
//     unrecognized message is never inferred into a domain code.
//   - A query rejected at a configured memory limit becomes
//     QUERY_MEMORY_LIMIT_EXCEEDED; any other configured resource limit
//     becomes QUERY_RESOURCE_LIMIT_EXCEEDED; a database out-of-memory abort
//     becomes QUERY_BACKEND_OUT_OF_MEMORY. None is marked retryable;
//     resubmission stays a caller decision.
//
// Idempotence and composition: nil yields (nil, false); an error that already
// carries a Loom user error is returned unchanged with ok=true, so repeated
// application is stable; anything not recognized as ArangoDB-owned is
// returned unchanged with ok=false, preserving identity for visitor errors,
// context sentinels, and other adapters' errors. Classified errors keep the
// original wrapped: errors.Is/errors.As continue to reach the driver error
// exactly as they did when the server classifier wrapped it.
func TranslateQueryError(err error) (translated error, ok bool) {
	if err == nil {
		return nil, false
	}
	if _, done := dataframeerrors.AsUserError(err); done {
		return err, true
	}
	switch {
	case isQueryUserAssertion(err, string(dataframeerrors.CodeTablePivotCellCardinality)):
		return dataframeerrors.Wrap(err, dataframeerrors.CodeTablePivotCellCardinality, ""), true
	case isQueryUserAssertion(err, string(dataframeerrors.CodeTablePivotUnlistedCategory)):
		return dataframeerrors.Wrap(err, dataframeerrors.CodeTablePivotUnlistedCategory, ""), true
	case isQueryUserAssertion(err, string(dataframeerrors.CodeConstructionRowValueMultipleValues)):
		return dataframeerrors.Wrap(err, dataframeerrors.CodeConstructionRowValueMultipleValues, ""), true
	case isQueryUserAssertion(err, string(dataframeerrors.CodeConstructionExpansionEmpty)):
		return dataframeerrors.Wrap(err, dataframeerrors.CodeConstructionExpansionEmpty, ""), true
	case isQueryUserAssertion(err, string(dataframeerrors.CodeExplicitGroupUnassignedMember)):
		return dataframeerrors.Wrap(err, dataframeerrors.CodeExplicitGroupUnassignedMember, ""), true
	case isQueryUserAssertion(err, string(dataframeerrors.CodeRelationshipCardinalityViolation)):
		return dataframeerrors.Wrap(err, dataframeerrors.CodeRelationshipCardinalityViolation, ""), true
	case isQueryUserAssertion(err, string(dataframeerrors.CodeTemporalAnchorInvalid)):
		return dataframeerrors.Wrap(err, dataframeerrors.CodeTemporalAnchorInvalid, ""), true
	case isQueryUserAssertion(err, string(dataframeerrors.CodeTemporalPrecisionUnsupported)):
		return dataframeerrors.Wrap(err, dataframeerrors.CodeTemporalPrecisionUnsupported, ""), true
	case isQueryUserAssertion(err, string(dataframeerrors.CodeTemporalTieAmbiguous)):
		return dataframeerrors.Wrap(err, dataframeerrors.CodeTemporalTieAmbiguous, ""), true
	case isQueryUserAssertion(err, string(dataframeerrors.CodeUnitIdentityUnknown)):
		return dataframeerrors.Wrap(err, dataframeerrors.CodeUnitIdentityUnknown, ""), true
	case isQueryUserAssertion(err, string(dataframeerrors.CodeUnitDimensionIncompatible)):
		return dataframeerrors.Wrap(err, dataframeerrors.CodeUnitDimensionIncompatible, ""), true
	case isQueryMemoryLimitExceeded(err):
		return dataframeerrors.Wrap(
			err,
			dataframeerrors.CodeQueryMemoryLimitExceeded,
			"",
			dataframeerrors.WithDetails(map[string]any{"backend": "arangodb", "resource": "query_memory"}),
		), true
	case isQueryResourceLimitExceeded(err):
		return dataframeerrors.Wrap(
			err,
			dataframeerrors.CodeQueryResourceLimitExceeded,
			"",
			dataframeerrors.WithDetails(map[string]any{"backend": "arangodb", "resource": "query"}),
		), true
	case isQueryOutOfMemory(err):
		return dataframeerrors.Wrap(
			err,
			dataframeerrors.CodeQueryBackendOutOfMemory,
			"",
			dataframeerrors.WithDetails(map[string]any{"backend": "arangodb", "resource": "memory"}),
		), true
	default:
		return err, false
	}
}

// translateQueryErrorAtSeam applies the boundary to a row-query return value,
// leaving unrecognized errors byte-identical.
func translateQueryErrorAtSeam(err error) error {
	translated, ok := TranslateQueryError(err)
	if !ok {
		return err
	}
	return translated
}

// arangoErrorFrom is the only place this boundary pulls a shared.ArangoError
// out of a chain for rendering. A typed-nil pointer carries no fields: like
// the PostgreSQL boundary's pgErrorFrom, it is reported as absent.
func arangoErrorFrom(err error) (shared.ArangoError, bool) {
	if err == nil {
		return shared.ArangoError{}, false
	}
	var value shared.ArangoError
	if errors.As(err, &value) {
		return value, true
	}
	var pointer *shared.ArangoError
	if errors.As(err, &pointer) && pointer != nil {
		return *pointer, true
	}
	return shared.ArangoError{}, false
}

// DiagnosticCause renders a server-side-safe diagnostic string for an
// ArangoDB failure ("ArangoDB error number N (HTTP C): message") for the
// approved internal consumer: the server diagnostic logger, behind its
// redaction and length guards. It is the adapter's own vendor rendering so
// composition never imports driver types; it must never reach a
// client-visible payload.
func DiagnosticCause(err error) (string, bool) {
	arangoErr, ok := arangoErrorFrom(err)
	if !ok {
		return "", false
	}
	cause := fmt.Sprintf("ArangoDB error number %d (HTTP %d)", arangoErr.ErrorNum, arangoErr.Code)
	if arangoErr.ErrorMessage != "" {
		cause += ": " + arangoErr.ErrorMessage
	}
	return cause, true
}
