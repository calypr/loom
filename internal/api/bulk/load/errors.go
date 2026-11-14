package load

import (
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/ingest"
)

// normalizeError translates ingest-owned failures at the ingest HTTP boundary
// so the shared HTTP package does not depend on the loader implementation.
func normalizeError(err error) error {
	if err == nil {
		return nil
	}
	var row *ingest.RowError
	if errors.As(err, &row) {
		code := dataframeerrors.CodeInternalError
		if row.Category == "validation" {
			code = dataframeerrors.CodeInvalidData
		}
		return dataframeerrors.Wrap(err, code, "",
			dataframeerrors.WithPublicMessage(row.PublicMessage()),
			dataframeerrors.WithDetails(map[string]any{
				"file": row.File, "row": row.Row, "resourceType": row.ResourceType, "category": row.Category,
			}))
	}
	var sourceValidation *ingest.SourceFileValidationError
	if errors.As(err, &sourceValidation) {
		return dataframeerrors.Wrap(err, dataframeerrors.CodeInvalidData, "",
			dataframeerrors.WithPublicMessage("The source file contains invalid rows."),
			dataframeerrors.WithDetails(sourceLoadValidationDetails(sourceValidation)))
	}
	if errors.Is(err, ingest.ErrSourceLoadNotFound) {
		return dataframeerrors.Wrap(err, dataframeerrors.CodeDatasetNotFound, "",
			dataframeerrors.WithPublicMessage("The source-load operation was not found."))
	}
	if errors.Is(err, ingest.ErrSourceLoadInProgress) {
		return dataframeerrors.Wrap(err, dataframeerrors.CodePublicationInProgress, "",
			dataframeerrors.WithPublicMessage("The source-load operation is already in progress; retry shortly."),
			dataframeerrors.WithRetryable(true))
	}
	if errors.Is(err, ingest.ErrSourceLoadIncomplete) {
		return dataframeerrors.Wrap(err, dataframeerrors.CodePublicationConflict, "",
			dataframeerrors.WithPublicMessage("The source-load operation is incomplete; all selected files must be accepted before publication."))
	}
	var resourceIncomplete *ingest.LoadIncompleteError
	if errors.As(err, &resourceIncomplete) {
		return dataframeerrors.Wrap(err, dataframeerrors.CodeGenerationLoadIncomplete, "",
			dataframeerrors.WithDetails(map[string]any{
				"validationErrors": resourceIncomplete.ValidationErrors,
				"generationErrors": resourceIncomplete.GenerationErrors,
				"edgeErrors":       resourceIncomplete.EdgeErrors,
			}))
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
	if errors.Is(err, dataset.ErrGenerationNotReady) {
		return dataframeerrors.Wrap(err, dataframeerrors.CodeGenerationNotReady, "")
	}
	var activation *ingest.ActivationOutcomeError
	if errors.As(err, &activation) {
		return dataframeerrors.Wrap(err, dataframeerrors.CodeGenerationActivationUnknown, "")
	}
	var sourceConflict *ingest.SourcePublishConflictError
	if errors.As(err, &sourceConflict) {
		return dataframeerrors.Wrap(err, dataframeerrors.CodePublicationConflict, "")
	}
	var migrationRequired *ingest.SourcePublishMigrationRequiredError
	if errors.As(err, &migrationRequired) {
		return dataframeerrors.Wrap(err, dataframeerrors.CodePublicationConflict, "", dataframeerrors.WithDetails(map[string]any{"migrationRequired": true}))
	}
	var sourceLimit *ingest.SourcePublishLimitError
	if errors.As(err, &sourceLimit) {
		return dataframeerrors.Wrap(err, dataframeerrors.CodeExportLimitExceeded, "",
			dataframeerrors.WithPublicMessage(fmt.Sprintf("Metadata publishing exceeds Loom's %s limit (maximum %d).", sourceLimit.Limit, sourceLimit.Maximum)),
			dataframeerrors.WithDetails(map[string]any{"limit": sourceLimit.Limit, "maximum": sourceLimit.Maximum}))
	}
	return err
}

func sourceLoadValidationDetails(validation *ingest.SourceFileValidationError) map[string]any {
	if validation == nil {
		return map[string]any{"issues": []any{}, "totalErrors": int64(0), "truncated": false}
	}
	const maxIssues = 100
	issues := make([]any, 0, min(len(validation.Issues), maxIssues))
	for _, sample := range validation.Issues[:min(len(validation.Issues), maxIssues)] {
		filename := filepath.Base(filepath.Clean(strings.TrimSpace(sample.File)))
		if filename == "." || strings.ContainsAny(filename, "\r\n\x00") {
			filename = "source.ndjson"
		}
		resourceType := strings.TrimSpace(sample.ResourceType)
		if resourceType == "" || strings.ContainsAny(resourceType, "\r\n\x00") {
			resourceType = "unknown"
		}
		category := sample.Category
		switch category {
		case "validation", "generation", "edge":
		default:
			category = "unknown"
		}
		issues = append(issues, map[string]any{
			"filename": filename, "row": sample.Line, "resourceType": resourceType,
			"category": category, "message": safeSourceLoadIssueMessage(sample),
		})
	}
	totalErrors := validation.TotalErrors
	if totalErrors < int64(len(validation.Issues)) {
		totalErrors = int64(len(validation.Issues))
	}
	return map[string]any{
		"issues": issues, "totalErrors": totalErrors,
		"truncated": validation.Truncated || len(validation.Issues) > maxIssues,
	}
}

func safeSourceLoadIssueMessage(sample ingest.RowErrorSample) string {
	if sample.Category == "validation" {
		if field, ok := missingRequiredField(sample); ok {
			return fmt.Sprintf("Required field %q is missing.", field)
		}
		return "This row does not match the required resource schema."
	}
	if sample.Category == "generation" {
		return "This row could not be mapped into the candidate graph."
	}
	if sample.Category == "edge" {
		return "A reference in this row could not be represented in the candidate graph."
	}
	return "Loom could not process this row."
}

func missingRequiredField(sample ingest.RowErrorSample) (string, bool) {
	message := strings.TrimSpace(sample.Message)
	const shortPrefix = "required field '"
	const shortSuffix = "' is missing"
	if strings.HasPrefix(message, shortPrefix) && strings.HasSuffix(message, shortSuffix) {
		field := strings.TrimSuffix(strings.TrimPrefix(message, shortPrefix), shortSuffix)
		return field, field != "" && safeSchemaFieldName(field)
	}

	filename := strings.TrimSpace(sample.File)
	resourceType := strings.TrimSpace(sample.ResourceType)
	if filename == "" || resourceType == "" || strings.ContainsAny(filename+resourceType, "\r\n\x00") {
		return "", false
	}
	prefix := fmt.Sprintf("%s row %d: required field ", filename, sample.Line)
	suffix := " is missing from " + resourceType + "."
	if !strings.HasPrefix(message, prefix) || !strings.HasSuffix(message, suffix) {
		return "", false
	}
	quoted := strings.TrimSuffix(strings.TrimPrefix(message, prefix), suffix)
	field, err := strconv.Unquote(quoted)
	if err != nil || !safeSchemaFieldName(field) {
		return "", false
	}
	return field, true
}

func safeSchemaFieldName(value string) bool {
	for _, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || character == '_' {
			continue
		}
		return false
	}
	return true
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
