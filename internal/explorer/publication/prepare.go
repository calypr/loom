package publication

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"time"

	dataframepublished "github.com/calypr/loom/internal/dataframe/published"

	"github.com/calypr/loom/internal/dataframe/artifactformat"
	"github.com/calypr/loom/internal/dataframe/publication"
	"github.com/calypr/loom/internal/dataframe/spec"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringcore"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/explorer/lifecycle/support"
	"github.com/calypr/loom/internal/explorer/receiptmodel"
	"github.com/calypr/loom/internal/projectid"
)

const (
	defaultArtifactTTL            = 24 * time.Hour
	defaultArtifactFilename       = "loom-dataset-artifact-v2.zip"
	defaultArtifactMaxRows  int64 = 10_000_000
	defaultArtifactMaxBytes int64 = 2 << 30
)

func (s *Service) PrepareArtifact(ctx context.Context, request ArtifactRequest) (ArtifactResult, error) {
	if s.config.ArtifactStore == nil || s.config.PublishedReader == nil {
		return ArtifactResult{}, support.Unavailable("artifact", "ARTIFACT_UNAVAILABLE", "artifact export is not configured", nil)
	}
	if err := validateArtifactRequest(request); err != nil {
		return ArtifactResult{}, err
	}
	project := projectid.Canonical(request.Project)
	receipt, revision, authorized, materialization, quality, err := s.resolveArtifactSource(ctx, request, project)
	if err != nil {
		return ArtifactResult{}, err
	}
	descriptor, columns, err := artifactDescriptor(receipt, materialization, request.OutputID)
	if err != nil {
		return ArtifactResult{}, support.Conflict("artifact", "ARTIFACT_CONTRACT_INVALID", "published output contract cannot describe the materialized artifact", nil, err)
	}
	if len(columns) == 0 {
		return ArtifactResult{}, support.Unprocessable("artifact", "NO_EXPORTABLE_COLUMNS", "the published output has no exportable columns", nil)
	}
	requestedFormat := artifactformat.ArtifactFormatAuto
	if !artifactColumnsContainRowIdentity(descriptor, columns) {
		requestedFormat = artifactformat.ArtifactFormatJSONL
	}
	format, err := artifactformat.ResolveArtifactFormat(requestedFormat, columns)
	if err != nil {
		return ArtifactResult{}, support.Malformed("artifact", err.Error(), err)
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
		Format:                   format,
		Filename:                 defaultArtifactFilename,
		MediaType:                "application/zip",
		CreatedAt:                s.now(),
		ExpiresAt:                s.now().Add(s.artifactTTL()),
	}
	artifactID, err := explorer.ArtifactID(identity)
	if err != nil {
		return ArtifactResult{}, support.Malformed("artifact", err.Error(), err)
	}
	identity.ID = artifactID

	stage, existing, err := s.config.ArtifactStore.Begin(ctx, identity)
	if err != nil {
		return ArtifactResult{}, support.Unavailable("artifact", "ARTIFACT_STAGE_FAILED", "artifact staging could not be created", err)
	}
	if existing != nil {
		if err := validateArtifactRecordIdentity(*existing, identity); err != nil {
			return ArtifactResult{}, support.Conflict("artifact", "ARTIFACT_ID_COLLISION", "artifact id is bound to different immutable identity", nil, err)
		}
		switch existing.State {
		case explorer.ArtifactComplete:
			copy := *existing
			return ArtifactResult{Record: &copy}, nil
		case explorer.ArtifactPreparing:
			return ArtifactResult{}, support.Conflict("artifact", "ARTIFACT_PREPARING", "an artifact with this idempotency key is already preparing", nil, nil)
		case explorer.ArtifactFailed:
			return ArtifactResult{}, support.Unavailable("artifact", "ARTIFACT_FAILED_RETRYABLE", "the prior artifact preparation failed; retry with a new idempotency key", nil)
		default:
			return ArtifactResult{}, support.Unavailable("artifact", "ARTIFACT_STATE_INVALID", "artifact has an unsupported state", nil)
		}
	}
	if stage == nil {
		return ArtifactResult{}, support.Internal("artifact", "ARTIFACT_STAGE_INVALID", "artifact store returned no stage", nil)
	}

	selectionMetadata, interpretationMetadata, provenanceMetadata, qualityMetadata, metadataErr := artifactMetadata(receipt, revision, materialization, request.OutputID, quality)
	if metadataErr != nil {
		return s.abortArtifactStage(ctx, stage, "ARTIFACT_METADATA_INVALID", metadataErr)
	}
	exactRequest := dataframepublished.ExactExportRequest{
		ExecutionID:        executionID,
		OutputID:           request.OutputID,
		Project:            project,
		DatasetGeneration:  materialization.DatasetGeneration,
		ReceiptID:          receipt.ID,
		SchemaDigest:       materialization.SchemaDigest,
		Columns:            artifactColumnNames(columns),
		AuthResourcePaths:  append([]string(nil), authorized.Scope.AuthResourcePaths...),
		Unrestricted:       authorized.Scope.Unrestricted(),
		IncludeRowIdentity: format == artifactformat.ArtifactFormatJSONL,
		ReaderID:           identity.ID,
		PinExpiresAt:       identity.ExpiresAt,
	}
	var streamResult dataframepublished.ExactExportResult
	encoded, encodeErr := artifactformat.WriteArtifact(ctx, stage, artifactformat.ArtifactRequest{
		Identity: artifactformat.ArtifactIdentity{
			Project:              project,
			DatasetGeneration:    materialization.DatasetGeneration,
			ReceiptID:            receipt.ID,
			ExecutionID:          executionID,
			OutputID:             request.OutputID,
			RevisionID:           revision.ID,
			SchemaDigest:         materialization.SchemaDigest,
			OutputContractDigest: receipt.OutputContractDigest,
		},
		Descriptor:             descriptor,
		Columns:                columns,
		Format:                 format,
		SelectionMetadata:      selectionMetadata,
		InterpretationMetadata: interpretationMetadata,
		Provenance:             provenanceMetadata,
		Quality:                qualityMetadata,
		MaxRows:                s.artifactMaxRows(),
		MaxBytes:               s.artifactMaxBytes(),
	}, func(visit artifactformat.ArtifactRowVisitor) error {
		var err error
		artifactVisit := visit
		if descriptor.RowGrain == string(spec.RowGrainGroups) {
			artifactVisit = func(row map[string]any) error {
				identity, identityErr := canonicalGroupArtifactIdentity(row["__loom_row_id"])
				if identityErr != nil {
					return fmt.Errorf("group artifact row identity: %w", identityErr)
				}
				copy := make(map[string]any, len(row))
				for key, value := range row {
					copy[key] = value
				}
				copy["__loom_row_id"] = identity
				return visit(copy)
			}
		}
		streamResult, err = s.config.PublishedReader.StreamExactExport(ctx, exactRequest, dataframepublished.ExactExportVisitor(artifactVisit))
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

func (s *Service) resolveArtifactSource(ctx context.Context, request ArtifactRequest, project string) (*receiptmodel.CompilationReceipt, *explorer.Revision, ports.AuthorizedCapability, dataframepublished.Materialization, publication.QualityReport, error) {
	revision, err := s.store.GetRevision(ctx, request.RevisionID)
	if err != nil {
		if errors.Is(err, explorer.ErrNotFound) || errors.Is(err, fs.ErrNotExist) {
			return nil, nil, ports.AuthorizedCapability{}, dataframepublished.Materialization{}, publication.QualityReport{}, support.NotFound("artifact", "REVISION_NOT_FOUND", "published revision was not found", err)
		}
		return nil, nil, ports.AuthorizedCapability{}, dataframepublished.Materialization{}, publication.QualityReport{}, support.Unavailable("artifact", "REVISION_STORE_UNAVAILABLE", "published revision could not be loaded", err)
	}
	if revision == nil {
		return nil, nil, ports.AuthorizedCapability{}, dataframepublished.Materialization{}, publication.QualityReport{}, support.NotFound("artifact", "REVISION_NOT_FOUND", "published revision was not found", nil)
	}
	if projectid.Canonical(revision.Project) != project || revision.ExplorerID != request.ExplorerID || revision.ID != request.RevisionID {
		return nil, nil, ports.AuthorizedCapability{}, dataframepublished.Materialization{}, publication.QualityReport{}, support.NotFound("artifact", "REVISION_NOT_FOUND", "published revision was not found", explorer.ErrNotFound)
	}
	if strings.TrimSpace(revision.CompilationReceiptID) == "" || strings.TrimSpace(revision.Publication.ExecutionID) == "" {
		return nil, nil, ports.AuthorizedCapability{}, dataframepublished.Materialization{}, publication.QualityReport{}, support.Conflict("artifact", "REVISION_NOT_PUBLISHED", "revision has no exact receipt/execution identity", nil, nil)
	}
	receipt, err := s.core.LookupReceipt(ctx, project, request.ExplorerID, revision.CompilationReceiptID)
	if err != nil {
		return nil, nil, ports.AuthorizedCapability{}, dataframepublished.Materialization{}, publication.QualityReport{}, err
	}
	if err := s.core.ValidateArchivedReceiptRoute(receipt, project, request.ExplorerID); err != nil {
		return nil, nil, ports.AuthorizedCapability{}, dataframepublished.Materialization{}, publication.QualityReport{}, err
	}
	if err := support.ValidateReceiptOutputContract(receipt, request.OutputID); err != nil {
		return nil, nil, ports.AuthorizedCapability{}, dataframepublished.Materialization{}, publication.QualityReport{}, support.Unprocessable("artifact", "OUTPUT_NOT_PUBLISHED", "requested output is not part of the immutable receipt", err)
	}
	if receipt.ID != revision.CompilationReceiptID || receipt.SourceGeneration != revision.SourceGeneration || receipt.SourceGeneration == "" {
		return nil, nil, ports.AuthorizedCapability{}, dataframepublished.Materialization{}, publication.QualityReport{}, support.Conflict("artifact", "REVISION_IDENTITY_CHANGED", "revision and receipt identities do not match", nil, nil)
	}
	authorized, snapshot, err := s.resolveExecutionCapability(ctx, project, receipt.SnapshotToken)
	if err != nil {
		return nil, nil, ports.AuthorizedCapability{}, dataframepublished.Materialization{}, publication.QualityReport{}, authoringcore.CapabilityResolutionFailure("artifact", "the artifact capability is unavailable or unauthorized", err)
	}
	if err := authoringcore.ValidateReceiptCapability(receipt, snapshot); err != nil {
		return nil, nil, ports.AuthorizedCapability{}, dataframepublished.Materialization{}, publication.QualityReport{}, support.Conflict("artifact", "RECEIPT_STALE", "the artifact receipt is no longer authorized", nil, err)
	}
	if err := support.ValidateAuthorizedReceiptExecution(receipt, authorized); err != nil {
		return nil, nil, ports.AuthorizedCapability{}, dataframepublished.Materialization{}, publication.QualityReport{}, support.Forbidden("artifact", "current caller is not authorized for this artifact", err)
	}
	if err := validateArtifactRevisionReceipt(*revision, *receipt); err != nil {
		return nil, nil, ports.AuthorizedCapability{}, dataframepublished.Materialization{}, publication.QualityReport{}, support.Conflict("artifact", "REVISION_IDENTITY_CHANGED", "revision does not match its immutable receipt", nil, err)
	}
	quality, err := artifactQualityReport(*revision, request.OutputID, *receipt)
	if err != nil {
		return nil, nil, ports.AuthorizedCapability{}, dataframepublished.Materialization{}, publication.QualityReport{}, support.Unprocessable("quality", "QUALITY_INCOMPLETE", "artifact export requires complete, passed quality evidence: "+err.Error(), err)
	}
	materialization, err := s.config.PublishedReader.ExactExecutionMaterialization(ctx, revision.Publication.ExecutionID, request.OutputID)
	if err != nil {
		return nil, nil, ports.AuthorizedCapability{}, dataframepublished.Materialization{}, publication.QualityReport{}, support.Unavailable("artifact", "MATERIALIZATION_UNAVAILABLE", "the exact published output is not available", err)
	}
	if err := validateArtifactMaterialization(materialization, *revision, *receipt, authorized.Scope, request.OutputID); err != nil {
		return nil, nil, ports.AuthorizedCapability{}, dataframepublished.Materialization{}, publication.QualityReport{}, support.Conflict("artifact", "MATERIALIZATION_IDENTITY_CHANGED", "the exact published output no longer matches the revision", nil, err)
	}
	return receipt, revision, authorized, materialization, quality, nil
}

func validateArtifactRequest(request ArtifactRequest) error {
	for label, value := range map[string]string{"project": request.Project, "explorerId": request.ExplorerID, "revisionId": request.RevisionID, "outputId": request.OutputID, "idempotencyKey": request.IdempotencyKey} {
		if strings.TrimSpace(value) == "" {
			return support.Malformed("artifact", label+" is required", nil)
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

func (s *Service) abortArtifactStage(ctx context.Context, stage explorer.ArtifactStage, code string, cause error) (ArtifactResult, error) {
	_, abortErr := stage.Abort(context.WithoutCancel(ctx), code)
	if abortErr != nil {
		return ArtifactResult{}, support.Unavailable("artifact", "ARTIFACT_ABORT_FAILED", "artifact preparation failed and its stage could not be aborted", fmt.Errorf("%v: abort: %w", cause, abortErr))
	}
	return ArtifactResult{}, support.Unavailable("artifact", code, "artifact preparation failed; no complete artifact was published", cause)
}
