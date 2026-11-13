package arango

import (
	"errors"
	"fmt"
	"strings"

	"github.com/arangodb/go-driver/v2/arangodb/shared"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
)

func isQueryMemoryLimitExceeded(err error) bool {
	if !isQueryResourceLimitExceeded(err) {
		return false
	}
	ok, arangoErr := shared.IsArangoError(err)
	return ok && strings.Contains(strings.ToLower(arangoErr.ErrorMessage), "memory")
}

func isQueryResourceLimitExceeded(err error) bool {
	return shared.IsArangoErrorWithErrorNum(err, shared.ErrResourceLimit)
}

func isQueryOutOfMemory(err error) bool {
	return shared.IsArangoErrorWithErrorNum(err, shared.ErrOutOfMemory)
}

func isQueryUserAssertion(err error, code string) bool {
	if strings.TrimSpace(code) == "" {
		return false
	}
	ok, arangoErr := shared.IsArangoError(err)
	return ok && arangoErr.ErrorNum == shared.ErrQueryUserAssert && strings.Contains(arangoErr.ErrorMessage, "AQL: "+code)
}

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

func translateQueryErrorAtSeam(err error) error {
	translated, ok := TranslateQueryError(err)
	if !ok {
		return err
	}
	return translated
}

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

func IsArangoErrorResponse(err error) bool {
	_, ok := arangoErrorFrom(err)
	return ok
}

func IsArangoPermissionResponse(err error) bool {
	arangoErr, ok := arangoErrorFrom(err)
	if !ok {
		return false
	}
	return arangoErr.Code == shared.ErrHttpUnauthorized ||
		arangoErr.Code == shared.ErrHttpForbidden ||
		arangoErr.ErrorNum == shared.ErrForbidden
}

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
