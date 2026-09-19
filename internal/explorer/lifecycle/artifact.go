package lifecycle

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/calypr/loom/internal/dataframe/publication"
	dataframepublished "github.com/calypr/loom/internal/dataframe/published"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/projectid"
)

const (
	defaultArtifactTTL      = 24 * time.Hour
	defaultArtifactFilename = "loom-dataset-artifact-v1.zip"
	defaultArtifactMaxRows  = int64(10_000_000)
	defaultArtifactMaxBytes = int64(2 << 30)
)

// ArtifactRequest identifies the exact published revision and output that
// should be copied into a durable, downloadable artifact. The capability
// token is intentionally not transport-visible: lifecycle obtains the
// immutable receipt token server-side and reauthorizes the current caller.
type ArtifactRequest struct {
	Project        string
	ExplorerID     string
	RevisionID     string
	OutputID       string
	IdempotencyKey string
}

// ArtifactResult returns the durable record. A PREPARING or FAILED record can
// be returned for an idempotent retry; callers must only offer a download for
// a COMPLETE record.
type ArtifactResult struct {
	Record *explorer.ArtifactRecord
}

// ArtifactOpenRequest identifies an artifact. Lifecycle reauthorizes the
// current caller against the receipt token retained in the server-side
// artifact record; capability internals never cross this request boundary.
type ArtifactOpenRequest struct {
	Project    string
	ExplorerID string
	ArtifactID string
}

// PrepareArtifact authorizes and streams one exact execution/output into an
// ArtifactStore. The stage is committed only after WriteArtifact has closed
// every member and the exact reader has completed without pin loss.
func (s *Service) PrepareArtifact(ctx context.Context, request ArtifactRequest) (ArtifactResult, error) {
	if s.config.ArtifactStore == nil || s.config.PublishedReader == nil {
		return ArtifactResult{}, unavailable("artifact", "ARTIFACT_UNAVAILABLE", "artifact export is not configured", nil)
	}
	if err := validateArtifactRequest(request); err != nil {
		return ArtifactResult{}, err
	}
	project := projectid.Canonical(request.Project)
	receipt, revision, authorized, materialization, quality, err := s.resolveArtifactSource(ctx, request, project)
	if err != nil {
		return ArtifactResult{}, err
	}
	columns := artifactColumns(materialization)
	if len(columns) == 0 {
		return ArtifactResult{}, unprocessable("artifact", "NO_EXPORTABLE_COLUMNS", "the published output has no exportable columns", nil)
	}

	executionID := strings.TrimSpace(revision.Publication.ExecutionID)
	identity := explorer.ArtifactRecord{
		Project:                  project,
		ExplorerID:               revision.ExplorerID,
		RevisionID:               revision.ID,
		OutputID:                 request.OutputID,
		ReceiptID:                receipt.ID,
		ExecutionID:              executionID,
		DatasetGeneration:        materialization.DatasetGeneration,
		SchemaDigest:             materialization.SchemaDigest,
		AuthorizationScopeDigest: receipt.AuthorizationScopeDigest,
		SnapshotToken:            receipt.SnapshotToken,
		IdempotencyKey:           strings.TrimSpace(request.IdempotencyKey),
		State:                    explorer.ArtifactPreparing,
		Filename:                 defaultArtifactFilename,
		MediaType:                "application/zip",
		CreatedAt:                s.now(),
		ExpiresAt:                s.now().Add(s.artifactTTL()),
	}
	artifactID, err := explorer.ArtifactID(identity)
	if err != nil {
		return ArtifactResult{}, malformed("artifact", err.Error(), err)
	}
	identity.ID = artifactID

	stage, existing, err := s.config.ArtifactStore.Begin(ctx, identity)
	if err != nil {
		return ArtifactResult{}, unavailable("artifact", "ARTIFACT_STAGE_FAILED", "artifact staging could not be created", err)
	}
	if existing != nil {
		if err := validateArtifactRecordIdentity(*existing, identity); err != nil {
			return ArtifactResult{}, conflict("artifact", "ARTIFACT_ID_COLLISION", "artifact id is bound to different immutable identity", nil, err)
		}
		switch existing.State {
		case explorer.ArtifactComplete:
			copy := *existing
			return ArtifactResult{Record: &copy}, nil
		case explorer.ArtifactPreparing:
			return ArtifactResult{}, conflict("artifact", "ARTIFACT_PREPARING", "an artifact with this idempotency key is already preparing", nil, nil)
		case explorer.ArtifactFailed:
			return ArtifactResult{}, unavailable("artifact", "ARTIFACT_FAILED_RETRYABLE", "the prior artifact preparation failed; retry with a new idempotency key", nil)
		default:
			return ArtifactResult{}, unavailable("artifact", "ARTIFACT_STATE_INVALID", "artifact has an unsupported state", nil)
		}
	}
	if stage == nil {
		return ArtifactResult{}, internal("artifact", "ARTIFACT_STAGE_INVALID", "artifact store returned no stage", nil)
	}

	selectionMetadata, interpretationMetadata, provenanceMetadata, qualityMetadata, metadataErr := artifactMetadata(receipt, revision, materialization, request.OutputID, quality)
	if metadataErr != nil {
		return s.abortArtifactStage(ctx, stage, "ARTIFACT_METADATA_INVALID", metadataErr)
	}
	exactRequest := dataframepublished.ExactExportRequest{
		ExecutionID:       executionID,
		OutputID:          request.OutputID,
		Project:           project,
		DatasetGeneration: materialization.DatasetGeneration,
		ReceiptID:         receipt.ID,
		SchemaDigest:      materialization.SchemaDigest,
		Columns:           artifactColumnNames(columns),
		AuthResourcePaths: append([]string(nil), authorized.Scope.AuthResourcePaths...),
		Unrestricted:      authorized.Scope.Unrestricted(),
		ReaderID:          identity.ID,
		PinExpiresAt:      identity.ExpiresAt,
	}
	var streamResult dataframepublished.ExactExportResult
	encoded, encodeErr := dataframepublished.WriteArtifact(ctx, stage, dataframepublished.ArtifactRequest{
		Identity: dataframepublished.ArtifactIdentity{
			Project:           project,
			DatasetGeneration: materialization.DatasetGeneration,
			ReceiptID:         receipt.ID,
			ExecutionID:       executionID,
			OutputID:          request.OutputID,
			RevisionID:        revision.ID,
		},
		Columns:                columns,
		SelectionMetadata:      selectionMetadata,
		InterpretationMetadata: interpretationMetadata,
		Provenance:             provenanceMetadata,
		Quality:                qualityMetadata,
		MaxRows:                s.artifactMaxRows(),
		MaxBytes:               s.artifactMaxBytes(),
	}, func(visit dataframepublished.ArtifactRowVisitor) error {
		var err error
		streamResult, err = s.config.PublishedReader.StreamExactExport(ctx, exactRequest, dataframepublished.ExactExportVisitor(visit))
		return err
	})
	if encodeErr != nil || !encoded.Complete {
		if encodeErr == nil {
			encodeErr = fmt.Errorf("artifact encoder did not complete")
		}
		return s.abortArtifactStage(ctx, stage, artifactFailureCode(encodeErr), encodeErr)
	}
	if streamResult.ExecutionID != executionID || streamResult.OutputID != request.OutputID {
		err := fmt.Errorf("exact reader returned a different execution/output identity")
		return s.abortArtifactStage(ctx, stage, "EXACT_IDENTITY_CHANGED", err)
	}
	completed, err := stage.Commit(ctx, explorer.ArtifactCompletion{
		ArchiveSHA256: encoded.ArchiveSHA256,
		Bytes:         encoded.ArchiveBytes,
		Rows:          encoded.Rows,
		Features:      len(columns),
		CompletedAt:   s.now(),
	})
	if err != nil {
		return s.abortArtifactStage(ctx, stage, "ARTIFACT_COMMIT_FAILED", err)
	}
	return ArtifactResult{Record: completed}, nil
}

// OpenArtifact performs all identity and current-capability checks before
// returning the archive. The store itself is still required to enforce the
// COMPLETE/expiry check at the final open operation.
func (s *Service) OpenArtifact(ctx context.Context, request ArtifactOpenRequest) (io.ReadCloser, *explorer.ArtifactRecord, error) {
	if s.config.ArtifactStore == nil {
		return nil, nil, unavailable("artifact", "ARTIFACT_UNAVAILABLE", "artifact export is not configured", nil)
	}
	if strings.TrimSpace(request.Project) == "" || strings.TrimSpace(request.ExplorerID) == "" || strings.TrimSpace(request.ArtifactID) == "" {
		return nil, nil, malformed("artifact", "project, explorerId, and artifactId are required", nil)
	}
	record, err := s.config.ArtifactStore.Get(ctx, request.ArtifactID)
	if err != nil || record == nil {
		return nil, nil, notFound("artifact", "ARTIFACT_NOT_FOUND", "artifact was not found", err)
	}
	project := projectid.Canonical(request.Project)
	if projectid.Canonical(record.Project) != project || record.ExplorerID != request.ExplorerID {
		return nil, nil, notFound("artifact", "ARTIFACT_NOT_FOUND", "artifact was not found", explorer.ErrNotFound)
	}
	if record.State != explorer.ArtifactComplete {
		return nil, record, unavailable("artifact", "ARTIFACT_NOT_COMPLETE", "artifact is not complete", nil)
	}
	if !record.ExpiresAt.After(s.now()) {
		return nil, record, unavailable("artifact", "ARTIFACT_EXPIRED", "artifact has expired", nil)
	}
	receipt, err := s.lookupReceipt(ctx, project, request.ExplorerID, record.ReceiptID)
	if err != nil {
		return nil, record, err
	}
	if err := s.validateReceiptRoute(receipt, project, request.ExplorerID); err != nil {
		return nil, record, err
	}
	if err := validateArtifactReceiptIdentity(*record, receipt); err != nil {
		return nil, record, conflict("artifact", "ARTIFACT_IDENTITY_CHANGED", "artifact no longer matches its immutable receipt", nil, err)
	}
	authorized, snapshot, err := s.resolveExecutionCapability(ctx, project, record.SnapshotToken)
	if err != nil {
		return nil, record, forbidden("artifact", "current caller is not authorized for this artifact", err)
	}
	if err := validateReceiptCapability(receipt, snapshot); err != nil {
		return nil, record, conflict("artifact", "RECEIPT_STALE", "the artifact receipt is no longer authorized", nil, err)
	}
	if err := validateAuthorizedReceiptExecution(receipt, authorized); err != nil {
		return nil, record, forbidden("artifact", "current caller is not authorized for this artifact", err)
	}
	revision, err := s.store.GetRevision(ctx, record.RevisionID)
	if err != nil || revision == nil {
		return nil, record, notFound("artifact", "ARTIFACT_REVISION_NOT_FOUND", "artifact revision was not found", err)
	}
	if err := validateArtifactRevisionIdentity(*record, *revision); err != nil {
		return nil, record, conflict("artifact", "ARTIFACT_IDENTITY_CHANGED", "artifact no longer matches its immutable revision", nil, err)
	}
	reader, opened, err := s.config.ArtifactStore.Open(ctx, request.ArtifactID)
	if err != nil || reader == nil || opened == nil || opened.State != explorer.ArtifactComplete || !opened.ExpiresAt.After(s.now()) {
		if err == nil {
			err = fmt.Errorf("artifact store returned an incomplete or expired archive")
		}
		return nil, record, unavailable("artifact", "ARTIFACT_UNAVAILABLE", "artifact is not available for download", err)
	}
	return reader, opened, nil
}

func (s *Service) resolveArtifactSource(ctx context.Context, request ArtifactRequest, project string) (*explorer.CompilationReceipt, *explorer.Revision, AuthorizedCapability, dataframepublished.Materialization, publication.QualityReport, error) {
	revision, err := s.store.GetRevision(ctx, request.RevisionID)
	if err != nil || revision == nil {
		return nil, nil, AuthorizedCapability{}, dataframepublished.Materialization{}, publication.QualityReport{}, notFound("artifact", "REVISION_NOT_FOUND", "published revision was not found", err)
	}
	if projectid.Canonical(revision.Project) != project || revision.ExplorerID != request.ExplorerID || revision.ID != request.RevisionID {
		return nil, nil, AuthorizedCapability{}, dataframepublished.Materialization{}, publication.QualityReport{}, notFound("artifact", "REVISION_NOT_FOUND", "published revision was not found", explorer.ErrNotFound)
	}
	if strings.TrimSpace(revision.CompilationReceiptID) == "" || strings.TrimSpace(revision.Publication.ExecutionID) == "" {
		return nil, nil, AuthorizedCapability{}, dataframepublished.Materialization{}, publication.QualityReport{}, conflict("artifact", "REVISION_NOT_PUBLISHED", "revision has no exact receipt/execution identity", nil, nil)
	}
	receipt, err := s.lookupReceipt(ctx, project, request.ExplorerID, revision.CompilationReceiptID)
	if err != nil {
		return nil, nil, AuthorizedCapability{}, dataframepublished.Materialization{}, publication.QualityReport{}, err
	}
	if err := s.validateReceiptRoute(receipt, project, request.ExplorerID); err != nil {
		return nil, nil, AuthorizedCapability{}, dataframepublished.Materialization{}, publication.QualityReport{}, err
	}
	if err := validateReceiptOutputContract(receipt, request.OutputID); err != nil {
		return nil, nil, AuthorizedCapability{}, dataframepublished.Materialization{}, publication.QualityReport{}, unprocessable("artifact", "OUTPUT_NOT_PUBLISHED", "requested output is not part of the immutable receipt", err)
	}
	if receipt.ID != revision.CompilationReceiptID || receipt.SourceGeneration != revision.SourceGeneration || receipt.SourceGeneration == "" {
		return nil, nil, AuthorizedCapability{}, dataframepublished.Materialization{}, publication.QualityReport{}, conflict("artifact", "REVISION_IDENTITY_CHANGED", "revision and receipt identities do not match", nil, nil)
	}
	authorized, snapshot, err := s.resolveExecutionCapability(ctx, project, receipt.SnapshotToken)
	if err != nil {
		return nil, nil, AuthorizedCapability{}, dataframepublished.Materialization{}, publication.QualityReport{}, forbidden("artifact", "current caller is not authorized for this artifact", err)
	}
	if err := validateReceiptCapability(receipt, snapshot); err != nil {
		return nil, nil, AuthorizedCapability{}, dataframepublished.Materialization{}, publication.QualityReport{}, conflict("artifact", "RECEIPT_STALE", "the artifact receipt is no longer authorized", nil, err)
	}
	if err := validateAuthorizedReceiptExecution(receipt, authorized); err != nil {
		return nil, nil, AuthorizedCapability{}, dataframepublished.Materialization{}, publication.QualityReport{}, forbidden("artifact", "current caller is not authorized for this artifact", err)
	}
	if err := validateArtifactRevisionReceipt(*revision, *receipt); err != nil {
		return nil, nil, AuthorizedCapability{}, dataframepublished.Materialization{}, publication.QualityReport{}, conflict("artifact", "REVISION_IDENTITY_CHANGED", "revision does not match its immutable receipt", nil, err)
	}
	quality, err := artifactQualityReport(*revision, request.OutputID, *receipt)
	if err != nil {
		return nil, nil, AuthorizedCapability{}, dataframepublished.Materialization{}, publication.QualityReport{}, unprocessable("quality", "QUALITY_INCOMPLETE", "artifact export requires complete, passed quality evidence", err)
	}
	materialization, err := s.config.PublishedReader.ExactExecutionMaterialization(ctx, revision.Publication.ExecutionID, request.OutputID)
	if err != nil {
		return nil, nil, AuthorizedCapability{}, dataframepublished.Materialization{}, publication.QualityReport{}, unavailable("artifact", "MATERIALIZATION_UNAVAILABLE", "the exact published output is not available", err)
	}
	if err := validateArtifactMaterialization(materialization, *revision, *receipt, request.OutputID); err != nil {
		return nil, nil, AuthorizedCapability{}, dataframepublished.Materialization{}, publication.QualityReport{}, conflict("artifact", "MATERIALIZATION_IDENTITY_CHANGED", "the exact published output no longer matches the revision", nil, err)
	}
	return receipt, revision, authorized, materialization, quality, nil
}

func validateArtifactRequest(request ArtifactRequest) error {
	for label, value := range map[string]string{"project": request.Project, "explorerId": request.ExplorerID, "revisionId": request.RevisionID, "outputId": request.OutputID, "idempotencyKey": request.IdempotencyKey} {
		if strings.TrimSpace(value) == "" {
			return malformed("artifact", label+" is required", nil)
		}
	}
	return nil
}

func (s *Service) artifactTTL() time.Duration {
	if s.config.ArtifactTTL > 0 {
		return s.config.ArtifactTTL
	}
	return defaultArtifactTTL
}

func (s *Service) artifactMaxRows() int64 {
	if s.config.ArtifactMaxRows > 0 {
		return s.config.ArtifactMaxRows
	}
	return defaultArtifactMaxRows
}

func (s *Service) artifactMaxBytes() int64 {
	if s.config.ArtifactMaxBytes > 0 {
		return s.config.ArtifactMaxBytes
	}
	return defaultArtifactMaxBytes
}

func artifactColumns(materialization dataframepublished.Materialization) []dataframepublished.ArtifactColumn {
	columns := make([]dataframepublished.ArtifactColumn, 0, len(materialization.Columns))
	for _, column := range materialization.Columns {
		name := strings.TrimSpace(column.Name)
		if name == "" || name == "auth_resource_path" || strings.HasPrefix(name, "__loom_") {
			continue
		}
		columns = append(columns, dataframepublished.ArtifactColumn{Name: name, LogicalType: column.LogicalType, Nullable: column.Nullable, Repeated: column.Repeated})
	}
	return columns
}

func artifactColumnNames(columns []dataframepublished.ArtifactColumn) []string {
	names := make([]string, len(columns))
	for i := range columns {
		names[i] = columns[i].Name
	}
	return names
}

func artifactQualityReport(revision explorer.Revision, outputID string, receipt explorer.CompilationReceipt) (publication.QualityReport, error) {
	var found *publication.QualityReport
	for i := range revision.QualityReports {
		if revision.QualityReports[i].Output == outputID {
			if found != nil {
				return publication.QualityReport{}, fmt.Errorf("duplicate quality reports for output %q", outputID)
			}
			copy := revision.QualityReports[i]
			found = &copy
		}
	}
	if found == nil {
		return publication.QualityReport{}, fmt.Errorf("quality report for output %q is missing", outputID)
	}
	if found.ReceiptID != receipt.ID || found.Project != receipt.Project || found.DatasetGeneration != receipt.SourceGeneration || found.ScopeDigest != receipt.AuthorizationScopeDigest || found.Completeness != publication.QualityComplete || found.Verdict != publication.QualityPassed || found.PolicyVersion != publication.DefaultQualityPolicyVersion {
		return publication.QualityReport{}, fmt.Errorf("quality report for output %q is not complete and receipt-bound", outputID)
	}
	return *found, nil
}

func artifactMetadata(receipt *explorer.CompilationReceipt, revision *explorer.Revision, materialization dataframepublished.Materialization, outputID string, quality publication.QualityReport) (json.RawMessage, json.RawMessage, json.RawMessage, json.RawMessage, error) {
	selection := struct {
		Recipe            any    `json:"recipe"`
		DatasetGeneration string `json:"datasetGeneration"`
	}{Recipe: receipt.Bundle, DatasetGeneration: receipt.SourceGeneration}
	selectionRaw, err := json.Marshal(selection)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	interpretations, err := json.Marshal(receipt.ResolvedInterpretations)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	provenance, err := json.Marshal(struct {
		Project           string `json:"project"`
		ExplorerID        string `json:"explorerId"`
		RevisionID        string `json:"revisionId"`
		ReceiptID         string `json:"receiptId"`
		ExecutionID       string `json:"executionId"`
		OutputID          string `json:"outputId"`
		DatasetGeneration string `json:"datasetGeneration"`
		SchemaDigest      string `json:"schemaDigest"`
		ScopeDigest       string `json:"authorizationScopeDigest"`
	}{Project: receipt.Project, ExplorerID: revision.ExplorerID, RevisionID: revision.ID, ReceiptID: receipt.ID, ExecutionID: materialization.Revision, OutputID: outputID, DatasetGeneration: materialization.DatasetGeneration, SchemaDigest: materialization.SchemaDigest, ScopeDigest: receipt.AuthorizationScopeDigest})
	if err != nil {
		return nil, nil, nil, nil, err
	}
	qualityRaw, err := json.Marshal([]publication.QualityReport{quality})
	if err != nil {
		return nil, nil, nil, nil, err
	}
	return selectionRaw, interpretations, provenance, qualityRaw, nil
}

func validateArtifactMaterialization(materialization dataframepublished.Materialization, revision explorer.Revision, receipt explorer.CompilationReceipt, outputID string) error {
	if projectid.Canonical(materialization.Project) != projectid.Canonical(receipt.Project) || materialization.DatasetGeneration != receipt.SourceGeneration || materialization.ReceiptID != receipt.ID || materialization.Revision != revision.Publication.ExecutionID || materialization.Name == "" || outputID == "" {
		return fmt.Errorf("materialization identity does not match receipt/revision")
	}
	return nil
}

func validateArtifactReceiptIdentity(record explorer.ArtifactRecord, receipt *explorer.CompilationReceipt) error {
	if receipt == nil || record.ReceiptID != receipt.ID || record.Project != projectid.Canonical(receipt.Project) || record.DatasetGeneration != receipt.SourceGeneration || record.AuthorizationScopeDigest != receipt.AuthorizationScopeDigest || record.SnapshotToken != receipt.SnapshotToken {
		return fmt.Errorf("artifact record does not match receipt")
	}
	return nil
}

func validateArtifactRevisionReceipt(revision explorer.Revision, receipt explorer.CompilationReceipt) error {
	if revision.CompilationReceiptID != receipt.ID || revision.SourceGeneration != receipt.SourceGeneration || projectid.Canonical(revision.Project) != projectid.Canonical(receipt.Project) {
		return fmt.Errorf("revision does not match receipt")
	}
	return nil
}

func validateArtifactRevisionIdentity(record explorer.ArtifactRecord, revision explorer.Revision) error {
	if record.RevisionID != revision.ID || record.Project != projectid.Canonical(revision.Project) || record.ExplorerID != revision.ExplorerID || record.ExecutionID != revision.Publication.ExecutionID || record.DatasetGeneration != revision.SourceGeneration {
		return fmt.Errorf("artifact record does not match revision")
	}
	return nil
}

func validateArtifactRecordIdentity(existing, expected explorer.ArtifactRecord) error {
	if existing.ID != expected.ID || existing.Project != expected.Project || existing.ExplorerID != expected.ExplorerID || existing.RevisionID != expected.RevisionID || existing.OutputID != expected.OutputID || existing.ReceiptID != expected.ReceiptID || existing.ExecutionID != expected.ExecutionID || existing.DatasetGeneration != expected.DatasetGeneration || existing.SchemaDigest != expected.SchemaDigest || existing.AuthorizationScopeDigest != expected.AuthorizationScopeDigest || existing.SnapshotToken != expected.SnapshotToken || existing.IdempotencyKey != expected.IdempotencyKey {
		return fmt.Errorf("artifact identities differ")
	}
	return nil
}

func artifactFailureCode(err error) string {
	if err == nil {
		return "ARTIFACT_PREPARATION_FAILED"
	}
	if strings.Contains(err.Error(), "pin") || strings.Contains(err.Error(), "PIN") {
		return "EXECUTION_READ_PIN_LOST"
	}
	return "ARTIFACT_PREPARATION_FAILED"
}

func (s *Service) abortArtifactStage(ctx context.Context, stage explorer.ArtifactStage, code string, cause error) (ArtifactResult, error) {
	_, abortErr := stage.Abort(context.WithoutCancel(ctx), code)
	if abortErr != nil {
		return ArtifactResult{}, unavailable("artifact", "ARTIFACT_ABORT_FAILED", "artifact preparation failed and its stage could not be aborted", fmt.Errorf("%v: abort: %w", cause, abortErr))
	}
	return ArtifactResult{}, unavailable("artifact", code, "artifact preparation failed; no complete artifact was published", cause)
}
