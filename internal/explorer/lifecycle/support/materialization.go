package support

import (
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
)

func ClassifyMaterializationError(stage string, err error) error {
	userErr, ok := dataframeerrors.AsUserError(err)
	if !ok {
		return nil
	}
	if dataframeerrors.IsFeatureResolutionCode(userErr.Code()) {
		instruction := "Update the feature policy, then publish again."
		if userErr.Code() == string(dataframeerrors.CodeRelationshipCardinalityViolation) {
			instruction = "Choose how multiple related values should be reduced, then publish again."
		} else if userErr.Code() == string(dataframeerrors.CodeConstructionExpansionEmpty) {
			instruction = "Choose the empty-list or no-match policy, then publish again."
		}
		message := dataframeerrors.PublicMessage(err) + ". " + instruction + " The active revision was retained."
		return FailureDetails(ports.ClassUnprocessable, stage, userErr.Code(), message, userErr.Details(), err)
	}
	var message string
	switch userErr.Code() {
	case string(dataframeerrors.CodeQueryMemoryLimitExceeded):
		message = "Explorer materialization exceeded the ArangoDB query memory limit. Reduce the Explorer query complexity or ask an operator to increase ArangoDB's query-memory-limit, then publish again. The active revision was retained."
	case string(dataframeerrors.CodeQueryResourceLimitExceeded):
		message = "ArangoDB rejected Explorer materialization because the query exceeded a configured resource limit. Check the ArangoDB server log for the exact limit, then adjust the query or server configuration before publishing again. The active revision was retained."
	case string(dataframeerrors.CodeQueryBackendOutOfMemory):
		message = "ArangoDB ran out of memory while materializing the Explorer. Reduce the Explorer query complexity or ask an operator to increase the memory available to ArangoDB, then publish again. The active revision was retained."
	default:
		return nil
	}
	return FailureDetails(ports.ClassUnavailable, stage, userErr.Code(), message, userErr.Details(), err)
}
