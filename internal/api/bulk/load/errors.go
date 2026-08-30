package load

import (
	"errors"

	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/ingest"
)

// NormalizeError translates ingest-owned failures at the ingest HTTP boundary
// so the shared HTTP package does not depend on the loader implementation.
func NormalizeError(err error) error {
	if err == nil {
		return nil
	}
	var preflight *ingest.PreflightError
	if errors.As(err, &preflight) {
		return dataframeerrors.Wrap(err, dataframeerrors.CodeIngestPreflightFailed, "", dataframeerrors.WithDetails(preflightErrorDetails(preflight)))
	}
	var incomplete *ingest.GenerationLoadIncompleteError
	if errors.As(err, &incomplete) {
		return dataframeerrors.Wrap(err, dataframeerrors.CodeGenerationLoadIncomplete, "", dataframeerrors.WithDetails(map[string]any{
			"validationErrors": incomplete.ValidationErrors,
			"generationErrors": incomplete.GenerationErrors,
			"edgeErrors":       incomplete.EdgeErrors,
		}))
	}
	var activation *ingest.ActivationOutcomeError
	if errors.As(err, &activation) {
		return dataframeerrors.Wrap(err, dataframeerrors.CodeGenerationActivationUnknown, "")
	}
	return err
}

func preflightErrorDetails(err *ingest.PreflightError) map[string]any {
	if err == nil || len(err.Report.Issues) == 0 {
		return nil
	}
	issues := make([]any, 0, len(err.Report.Issues))
	for _, issue := range err.Report.Issues {
		item := map[string]any{"code": issue.Code}
		if issue.File != "" {
			item["file"] = issue.File
		}
		if issue.ResourceType != "" {
			item["resourceType"] = issue.ResourceType
		}
		if issue.Row > 0 {
			item["row"] = issue.Row
		}
		issues = append(issues, item)
	}
	return map[string]any{"issues": issues}
}
